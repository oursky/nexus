// Package net provides host-side network helpers for the VM runner.
package net

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	gvproxyVersion     = "v0.8.8"
	gvproxyDownloadURL = "https://github.com/containers/gvisor-tap-vsock/releases/download/" + gvproxyVersion + "/gvproxy-darwin"
)

// GVProxy manages a gvproxy subprocess for virtio-net backend.
type GVProxy struct {
	cmd        *exec.Cmd
	socketPath string
	logPath    string
	ctlSocket  string
}

// FindGVProxy returns the path to the gvproxy binary, searching in order:
//  1. PATH
//  2. ~/.cache/nexus/bin/gvproxy
//
// If the binary is not found and autoDownload is true, it will be downloaded
// to the cache directory.
func FindGVProxy(autoDownload bool) (string, error) {
	// 1. PATH
	if p, err := exec.LookPath("gvproxy"); err == nil {
		return p, nil
	}

	// 2. Cache dir
	cacheBin := cacheGVProxyPath()
	if _, err := os.Stat(cacheBin); err == nil {
		return cacheBin, nil
	}

	if !autoDownload {
		return "", fmt.Errorf("gvproxy not found in PATH or %s", cacheBin)
	}

	// 3. Download
	if err := downloadGVProxy(cacheBin); err != nil {
		return "", fmt.Errorf("download gvproxy: %w", err)
	}
	return cacheBin, nil
}

// StartGVProxy starts a gvproxy process listening on a Unix datagram socket.
// The socket is created at socketPath. The caller is responsible for stopping
// the process with Stop().
func StartGVProxy(gvproxyPath, socketPath string) (*GVProxy, error) {
	// Remove any stale sockets.
	_ = os.Remove(socketPath)
	ctlSocket := socketPath + ".ctl"
	_ = os.Remove(ctlSocket)

	logPath := socketPath + ".log"

	// Use a random high port for SSH to avoid conflicts with other gvproxy
	// instances. Port 0 is not accepted, so pick from 40000-60000.
	sshPort := 40000 + rand.Intn(20000)

	args := []string{
		"-mtu", "1500",
		"-ssh-port", fmt.Sprintf("%d", sshPort),
		"-listen-vfkit", "unixgram://" + socketPath,
		"-listen", "unix://" + ctlSocket,
		"-log-file", logPath,
	}

	cmd := exec.Command(gvproxyPath, args...)
	// Redirect to the log file rather than inheriting the daemon's pipe fds.
	// gvproxy already writes structured logs to -log-file; stdout/stderr just
	// carry startup noise that doesn't need to reach the daemon's output.
	if lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		cmd.Stdout = lf
		cmd.Stderr = lf
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start gvproxy: %w", err)
	}

	// Wait a moment for the sockets to be created.
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	g := &GVProxy{
		cmd:        cmd,
		socketPath: socketPath,
		logPath:    logPath,
		ctlSocket:  ctlSocket,
	}
	return g, nil
}

// SocketPath returns the Unix socket path that libkrun should connect to.
func (g *GVProxy) SocketPath() string {
	return g.socketPath
}

// ExposePort asks gvproxy to forward a host port to the same port inside the VM.
func (g *GVProxy) ExposePort(port int) error {
	return g.expose("tcp", fmt.Sprintf("127.0.0.1:%d", port), fmt.Sprintf("192.168.127.2:%d", port))
}

// ExposeTCPForward binds hostPort on 127.0.0.1 and forwards TCP to guestIP:guestPort
// (typically guest IP 192.168.127.2 inside gvproxy NAT).
func (g *GVProxy) ExposeTCPForward(hostPort, guestPort int) error {
	local := fmt.Sprintf("127.0.0.1:%d", hostPort)
	remote := fmt.Sprintf("192.168.127.2:%d", guestPort)
	return g.expose("tcp", local, remote)
}

// UnexposePort removes a previously exposed port forward.
func (g *GVProxy) UnexposePort(port int) error {
	return g.unexpose("tcp", fmt.Sprintf("127.0.0.1:%d", port))
}

func (g *GVProxy) ctlClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", g.ctlSocket)
			},
		},
		Timeout: 5 * time.Second,
	}
}

func (g *GVProxy) expose(protocol, local, remote string) error {
	payload := fmt.Sprintf(`{"local":"%s","remote":"%s","protocol":"%s"}`, local, remote, protocol)
	req, err := http.NewRequest("POST", "http://x/services/forwarder/expose", strings.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.ctlClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gvproxy expose %s->%s: HTTP %d: %s", local, remote, resp.StatusCode, string(body))
	}
	return nil
}

func (g *GVProxy) unexpose(protocol, local string) error {
	payload := fmt.Sprintf(`{"local":"%s","protocol":"%s"}`, local, protocol)
	req, err := http.NewRequest("POST", "http://x/services/forwarder/unexpose", strings.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.ctlClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gvproxy unexpose %s: HTTP %d: %s", local, resp.StatusCode, string(body))
	}
	return nil
}

// Stop terminates the gvproxy process.
func (g *GVProxy) Stop() error {
	if g.cmd == nil || g.cmd.Process == nil {
		return nil
	}
	_ = g.cmd.Process.Kill()
	_ = g.cmd.Wait()
	_ = os.Remove(g.socketPath)
	_ = os.Remove(g.ctlSocket)
	return nil
}

// LogPath returns the path to the gvproxy log file (for debugging).
func (g *GVProxy) LogPath() string {
	return g.logPath
}

func cacheGVProxyPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".cache", "nexus", "bin", "gvproxy")
}

func downloadGVProxy(dest string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("auto-download of gvproxy is only supported on darwin")
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create bin dir: %w", err)
	}

	tmp := dest + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	resp, err := http.Get(gvproxyDownloadURL)
	if err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("download gvproxy: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("download gvproxy: HTTP %d", resp.StatusCode)
	}

	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write gvproxy: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close gvproxy: %w", err)
	}

	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("chmod gvproxy: %w", err)
	}

	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename gvproxy: %w", err)
	}

	return nil
}
