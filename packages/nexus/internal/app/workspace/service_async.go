package workspace

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/oursky/nexus/packages/nexus/internal/domain/spotlight"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
)

// runStartAsyncTimeout is the maximum time for the entire start operation including VM spawn.
const runStartAsyncTimeout = 6 * time.Minute

// defaultReadinessDeadline is how long to wait for guest provisioning before
// marking the workspace running anyway. It can be overridden by
// NEXUS_READINESS_TIMEOUT_SECONDS for faster failure in CI or tests.
const defaultReadinessDeadline = 3 * time.Minute

func readinessDeadlineDuration() time.Duration {
	if v := os.Getenv("NEXUS_READINESS_TIMEOUT_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return defaultReadinessDeadline
}

// runStartAsync performs the slow driver.Start work in the background and
// transitions the workspace to running (success) or created (failure).
// If the driver supports WorkspaceReady, it polls until tools are fully
// provisioned before marking the workspace as running (keeping it in the
// starting state during package installation to block premature PTY access).
func (s *Service) runStartAsync(ws *workspace.Workspace) {
	// Recover from panics so the workspace doesn't get stuck in StateStarting.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[workspace] runStartAsync: panic for workspace=%s: %v", ws.ID, r)
			// Try to reset workspace to Created state so it can be retried.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if current, err := s.repo.Get(ctx, ws.ID); err == nil {
				current.State = workspace.StateCreated
				current.UpdatedAt = time.Now().UTC()
				_ = s.repo.Update(ctx, current)
			}
			s.notifyReady(ws.ID)
		}
	}()

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
		s.notifyReady(id)
		return
	}

	current = s.waitForReadiness(ctx, current)
	if current == nil {
		resetCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if w, err := s.repo.Get(resetCtx, id); err == nil && w.State == workspace.StateStarting {
			w.State = workspace.StateCreated
			w.UpdatedAt = time.Now().UTC()
			_ = s.repo.Update(resetCtx, w)
		}
		s.notifyReady(id)
		return
	}

	s.markWorkspaceRunning(ctx, current)
	s.autoForwardPorts(ctx, current)
}

func enrichDriverStartError(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "bwrap") || strings.Contains(msg, "bubblewrap"):
		return fmt.Errorf("installation corrupted: process sandbox requires bubblewrap (bwrap) — install it or re-run install.sh: %w", err)
	case strings.Contains(msg, "sandbox-exec"):
		return fmt.Errorf("installation corrupted: macOS seatbelt (sandbox-exec) is required — re-run install.sh: %w", err)
	case strings.Contains(msg, "libkrun.so") || strings.Contains(msg, "libkrun"):
		return fmt.Errorf("installation corrupted: libkrun VM libraries missing or broken — re-run install.sh or `nexus daemon start`: %w", err)
	default:
		return err
	}
}

func (s *Service) performStart(ctx context.Context, ws *workspace.Workspace) (*workspace.Workspace, bool) {
	var startErr error
	if s.registry != nil {
		driver, ok := s.registry.Driver(ws.Backend)
		backendLabel := ws.Backend
		if backendLabel == "" {
			backendLabel = s.registry.DefaultBackend()
		}
		if !ok {
			startErr = fmt.Errorf("installation corrupted: %q runtime is not available on this host — re-run install.sh and ensure the libkrun VM stack is provisioned", backendLabel)
		} else {
			startErr = driver.Start(ctx, ws)
			startErr = enrichDriverStartError(startErr)
		}
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
	readinessDeadline := time.Now().Add(readinessDeadlineDuration())
	probeAttempt := 0
	log.Printf("[workspace] runStartAsync: waiting for workspace %s to be ready (tool provisioning)...", id)

	for {
		probeAttempt++
		elapsed := time.Since(readinessDeadline.Add(-readinessDeadlineDuration())).Round(time.Second)
		log.Printf("[workspace] runStartAsync: readiness probe start attempt=%d workspace=%s elapsed=%s", probeAttempt, id, elapsed)

		ready, err := s.runReadinessProbe(ctx, rd, &wsCopy)
		if ready {
			log.Printf("[workspace] runStartAsync: workspace %s is ready", id)
			break
		}
		if time.Now().After(readinessDeadline) {
			log.Printf("[workspace] runStartAsync: readiness timeout for %s after %s; proceeding to running", id, readinessDeadlineDuration())
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

	// Populate RootPath so sync drivers can resolve the workspace filesystem path.
	// This is the authoritative source for "where does this workspace's code live?".
	if current.RootPath == "" {
		if workspace.UsesGuestVM(current.Backend) {
			// VM backends (libkrun) mount the project at /workspace inside the guest.
			current.RootPath = "/workspace"
		} else {
			// Process/sandbox backends use the local repo path as the workspace root.
			current.RootPath = current.Repo
		}
	}

	current.State = workspace.StateRunning
	current.UpdatedAt = time.Now().UTC()
	if err := s.repo.Update(ctx, current); err != nil {
		log.Printf("[workspace] runStartAsync: persist running failed for %s: %v", current.ID, err)
	}
	// Mount any attached volumes.
	s.mountVolumes(ctx, current)
	// Start sync for volumes with sync enabled.
	s.startVolumeSync(ctx, current)
	// Notify any callers blocked in WaitReady.
	s.notifyReady(current.ID)
}

func (s *Service) mountVolumes(ctx context.Context, ws *workspace.Workspace) {
	if s.volumeMounter == nil {
		return
	}
	attachments, err := s.volumeMounter.ListWorkspaceAttachments(ctx, ws.ID)
	if err != nil {
		log.Printf("[workspace] mountVolumes: list attachments failed for %s: %v", ws.ID, err)
		return
	}
	if len(attachments) == 0 {
		return
	}
	log.Printf("[workspace] mountVolumes: mounting %d volume(s) for %s", len(attachments), ws.ID)
	for _, att := range attachments {
		mountPath := filepath.Join(ws.RootPath, att.MountPath)
		if err := os.MkdirAll(mountPath, 0755); err != nil {
			log.Printf("[workspace] mountVolumes: mkdir failed for %s at %s: %v", att.VolumeID, mountPath, err)
			continue
		}
		volumePath := s.volumeMounter.MountPath(att.VolumeID)
		// For now, create a symlink or bind mount would be needed.
		// For process backend, we'll create a symlink.
		// For VM backend, the driver would need to handle this.
		if workspace.UsesGuestVM(ws.Backend) {
			log.Printf("[workspace] mountVolumes: VM backend volume mount not yet implemented for %s", att.VolumeID)
			continue
		}
		// Process backend: create symlink
		if err := os.Symlink(volumePath, mountPath); err != nil {
			if os.IsExist(err) {
				// Remove existing and retry
				os.Remove(mountPath)
				err = os.Symlink(volumePath, mountPath)
			}
			if err != nil {
				log.Printf("[workspace] mountVolumes: symlink failed for %s -> %s: %v", volumePath, mountPath, err)
				continue
			}
		}
		log.Printf("[workspace] mountVolumes: mounted %s at %s", att.VolumeID, mountPath)
	}
}

func (s *Service) startVolumeSync(ctx context.Context, ws *workspace.Workspace) {
	if s.volumeSyncStarter == nil {
		return
	}
	attachments, err := s.volumeMounter.ListWorkspaceAttachments(ctx, ws.ID)
	if err != nil {
		log.Printf("[workspace] startVolumeSync: list attachments failed for %s: %v", ws.ID, err)
		return
	}
	if len(attachments) == 0 {
		return
	}
	for _, att := range attachments {
		vol, err := s.volumeSyncStarter.GetVolume(ctx, att.VolumeID)
		if err != nil {
			log.Printf("[workspace] startVolumeSync: get volume failed for %s: %v", att.VolumeID, err)
			continue
		}
		if vol.Sync == nil || !vol.Sync.Enabled {
			continue
		}
		log.Printf("[workspace] startVolumeSync: starting sync for volume %s (workspace %s)", att.VolumeID, ws.ID)
		sessionID, err := s.volumeSyncStarter.StartVolumeSync(ctx, ws.ID, att.VolumeID)
		if err != nil {
			log.Printf("[workspace] startVolumeSync: start sync failed for %s: %v", att.VolumeID, err)
			continue
		}
		if err := s.volumeSyncStarter.UpdateSyncSession(ctx, att.VolumeID, sessionID, "syncing"); err != nil {
			log.Printf("[workspace] startVolumeSync: update sync session failed for %s: %v", att.VolumeID, err)
		}
	}
}

func (s *Service) stopVolumeSync(ctx context.Context, ws *workspace.Workspace) {
	if s.volumeSyncStarter == nil {
		return
	}
	attachments, err := s.volumeMounter.ListWorkspaceAttachments(ctx, ws.ID)
	if err != nil {
		log.Printf("[workspace] stopVolumeSync: list attachments failed for %s: %v", ws.ID, err)
		return
	}
	for _, att := range attachments {
		vol, err := s.volumeSyncStarter.GetVolume(ctx, att.VolumeID)
		if err != nil {
			continue
		}
		if vol.Sync == nil || !vol.Sync.Enabled || vol.Sync.SessionID == "" {
			continue
		}
		log.Printf("[workspace] stopVolumeSync: stopping sync for volume %s (workspace %s)", att.VolumeID, ws.ID)
		if err := s.volumeSyncStarter.StopVolumeSync(ctx, att.VolumeID); err != nil {
			log.Printf("[workspace] stopVolumeSync: stop sync failed for %s: %v", att.VolumeID, err)
		}
	}
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
