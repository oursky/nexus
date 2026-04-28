package tunnel

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"time"
)

type Config struct {
	SSHTarget   string
	SSHPort     int
	SSHIdentity string
	RemotePort  int
	LocalPort   int
}

type Manager struct {
	cfg       Config
	localPort int
	cancel    context.CancelFunc
	done      chan struct{}
}

func New(cfg Config) *Manager {
	return &Manager{
		cfg:       cfg,
		localPort: cfg.LocalPort,
	}
}

func (m *Manager) Start(ctx context.Context) error {
	if m.localPort == 0 {
		p, err := freePort()
		if err != nil {
			return fmt.Errorf("tunnel: find free port: %w", err)
		}
		m.localPort = p
	}

	childCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.done = make(chan struct{})

	started := make(chan error, 1)
	go m.runLoop(childCtx, started)

	select {
	case err := <-started:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) runLoop(ctx context.Context, started chan<- error) {
	defer close(m.done)

	backoffs := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 30 * time.Second}
	attempt := 0
	sentStarted := false

	for {
		err := m.runOnce(ctx)
		if !sentStarted {
			started <- err
			sentStarted = true
			if err != nil {
				return
			}
		}

		select {
		case <-ctx.Done():
			return
		default:
		}

		if err != nil {
			log.Printf("tunnel: ssh exited: %v; restarting", err)
		}

		delay := backoffs[attempt]
		if attempt < len(backoffs)-1 {
			attempt++
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

func (m *Manager) runOnce(ctx context.Context) error {
	args := m.buildSSHArgs()
	cmd := exec.CommandContext(ctx, "ssh", args...)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ssh start: %w", err)
	}

	if err := m.waitHealthy(ctx); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}

	return cmd.Wait()
}

func (m *Manager) buildSSHArgs() []string {
	lport := m.localPort
	cfg := m.cfg
	args := []string{
		"-N",
		"-o", "StrictHostKeyChecking=no",
		"-o", "ExitOnForwardFailure=yes",
		"-p", fmt.Sprintf("%d", cfg.SSHPort),
		"-L", fmt.Sprintf("%d:127.0.0.1:%d", lport, cfg.RemotePort),
	}
	if cfg.SSHIdentity != "" {
		args = append(args, "-i", cfg.SSHIdentity)
	}
	args = append(args, cfg.SSHTarget)
	return args
}

func (m *Manager) waitHealthy(ctx context.Context) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/healthz", m.localPort)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("tunnel: timed out waiting for healthz on port %d", m.localPort)
}

func (m *Manager) LocalPort() int {
	return m.localPort
}

func (m *Manager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	if m.done != nil {
		<-m.done
	}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}
