package workspace

import (
	"context"
	"log"
	"time"

	"github.com/oursky/nexus/packages/nexus/internal/domain/spotlight"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
)

// runStartAsyncTimeout is the maximum time for the entire start operation including VM spawn.
const runStartAsyncTimeout = 6 * time.Minute

// runStartAsync performs the slow driver.Start work in the background and
// transitions the workspace to running (success) or created (failure).
// If the driver supports WorkspaceReady, it polls until tools are fully
// provisioned before marking the workspace as running (keeping it in the
// starting state during package installation to block premature PTY access).
func (s *Service) runStartAsync(ws *workspace.Workspace) {
	id := ws.ID

	// Apply a timeout to the start operation so it doesn't hang forever.
	ctx, cancel := context.WithTimeout(s.bgCtx, runStartAsyncTimeout)
	defer cancel()

	startTime := time.Now()
	log.Printf("[workspace] runStartAsync: start workspace=%s", id)
	defer func() {
		log.Printf("[workspace] runStartAsync: done workspace=%s (%s)", id, time.Since(startTime).Round(time.Millisecond))
	}()

	current, ok := s.performStart(ctx, ws)
	if !ok {
		return
	}

	current = s.waitForReadiness(ctx, current)
	if current == nil {
		return
	}

	s.markWorkspaceRunning(ctx, current)
	s.autoForwardPorts(ctx, current)
}

func (s *Service) performStart(ctx context.Context, ws *workspace.Workspace) (*workspace.Workspace, bool) {
	var startErr error
	if driver := s.driverFor(ws); driver != nil {
		startErr = driver.Start(ctx, ws)
	}

	current, fetchErr := s.repo.Get(ctx, ws.ID)
	if fetchErr != nil {
		log.Printf("[workspace] runStartAsync: workspace %s disappeared during start: %v", ws.ID, fetchErr)
		return nil, false
	}

	if startErr != nil {
		log.Printf("[workspace] runStartAsync: start failed for %s: %v", ws.ID, startErr)
		current.State = workspace.StateCreated
		current.UpdatedAt = time.Now().UTC()
		_ = s.repo.Update(ctx, current)
		return nil, false
	}
	return current, true
}

func (s *Service) waitForReadiness(ctx context.Context, current *workspace.Workspace) *workspace.Workspace {
	driver := s.driverFor(current)
	rd, ok := driver.(runtimeReadinessDriver)
	if !ok {
		return current
	}

	id := current.ID
	wsCopy := *current
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	readinessDeadline := time.Now().Add(3 * time.Minute)
	probeAttempt := 0
	log.Printf("[workspace] runStartAsync: waiting for workspace %s to be ready (tool provisioning)...", id)

	for {
		probeAttempt++
		elapsed := time.Since(readinessDeadline.Add(-3 * time.Minute)).Round(time.Second)
		log.Printf("[workspace] runStartAsync: readiness probe start attempt=%d workspace=%s elapsed=%s", probeAttempt, id, elapsed)

		ready, err := s.runReadinessProbe(ctx, rd, &wsCopy)
		if ready {
			log.Printf("[workspace] runStartAsync: workspace %s is ready", id)
			break
		}
		if time.Now().After(readinessDeadline) {
			log.Printf("[workspace] runStartAsync: readiness timeout for %s after %s; proceeding to running", id, 3*time.Minute)
			break
		}
		if err == context.Canceled {
			log.Printf("[workspace] runStartAsync: context cancelled waiting for %s readiness", id)
			return nil
		}
		select {
		case <-ctx.Done():
			log.Printf("[workspace] runStartAsync: context cancelled waiting for %s readiness", id)
			return nil
		case <-ticker.C:
		}
	}
	if cur, err := s.repo.Get(ctx, id); err == nil {
		return cur
	}
	return current
}

func (s *Service) runReadinessProbe(ctx context.Context, rd runtimeReadinessDriver, wsCopy *workspace.Workspace) (bool, error) {
	type probeResult struct {
		ready bool
		err   error
	}
	resultCh := make(chan probeResult, 1)
	probeCtx, cancelProbe := context.WithTimeout(ctx, 12*time.Second)
	go func() {
		ready, err := rd.WorkspaceReady(probeCtx, wsCopy)
		resultCh <- probeResult{ready: ready, err: err}
	}()

	var ready bool
	var err error
	probeStart := time.Now()
	select {
	case <-probeCtx.Done():
		err = probeCtx.Err()
	case result := <-resultCh:
		ready = result.ready
		err = result.err
	}
	cancelProbe()
	probeDur := time.Since(probeStart).Round(time.Millisecond)

	id := wsCopy.ID
	if err == context.DeadlineExceeded {
		log.Printf("[workspace] runStartAsync: readiness probe timeout workspace=%s duration=%s", id, probeDur)
	} else if err == context.Canceled {
		log.Printf("[workspace] runStartAsync: readiness probe canceled workspace=%s duration=%s", id, probeDur)
	} else if err != nil {
		log.Printf("[workspace] runStartAsync: readiness probe error workspace=%s duration=%s err=%v", id, probeDur, err)
	} else {
		log.Printf("[workspace] runStartAsync: readiness probe done workspace=%s duration=%s ready=%t", id, probeDur, ready)
	}
	return ready, err
}

func (s *Service) markWorkspaceRunning(ctx context.Context, current *workspace.Workspace) {
	if current.Backend == "" && s.registry != nil {
		current.Backend = s.registry.DefaultBackend()
	}
	current.State = workspace.StateRunning
	current.UpdatedAt = time.Now().UTC()
	if err := s.repo.Update(ctx, current); err != nil {
		log.Printf("[workspace] runStartAsync: persist running failed for %s: %v", current.ID, err)
	}
	// Notify any callers blocked in WaitReady.
	s.notifyReady(current.ID)
}

func (s *Service) autoForwardPorts(ctx context.Context, current *workspace.Workspace) {
	if s.forwardCreator == nil {
		return
	}
	// Spotlight port forwarding only works for VM backends.
	if !workspace.UsesGuestVM(current.Backend) {
		return
	}
	discoverCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	ports, err := s.DiscoverPorts(discoverCtx, current.ID)
	cancel()
	if err != nil {
		return
	}
	for _, p := range ports {
		_, err := s.forwardCreator.StartSpotlight(ctx, current.ID, spotlight.ExposeSpec{
			LocalPort:   p.RemotePort,
			RemotePort:  p.RemotePort,
			Protocol:    p.Protocol,
			WorkspaceID: current.ID,
		})
		if err != nil {
			log.Printf("[workspace] runStartAsync: auto-forward port %d for %s failed: %v", p.RemotePort, current.ID, err)
		}
	}
}
