package sshtunnel

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

// LocalProxy manages a set of local TCP port forwards without SSH.
// Each Forward binds LocalPort on 127.0.0.1 and proxies connections to
// RemoteHost:RemotePort on the local machine.
//
// This is used when the nexus daemon is running locally (no SSH tunnel needed)
// but workspace services are inside a VM (e.g. libkrun) reachable only through
// the daemon's ephemeral vsock proxy port — not the original service port.
//
// Example: workspace service on VM port 3000 → daemon binds :37355 as proxy.
// LocalProxy binds :3000 and forwards to :37355, so "curl localhost:3000" works.
type LocalProxy struct {
	mu        sync.Mutex
	listeners []net.Listener
	ctx       context.Context
	cancel    context.CancelFunc
}

// StartLocalProxy binds local ports for all given Forward specs and starts
// proxying. Ports already in use are evicted first; warnings list the PIDs
// that were evicted (for display in the TUI status line).
// Returns an error if any local port cannot be bound after eviction.
func StartLocalProxy(fwds []Forward) (*LocalProxy, []string, error) {
	ports := make([]int, 0, len(fwds))
	for _, f := range fwds {
		if f.LocalPort > 0 {
			ports = append(ports, f.LocalPort)
		}
	}
	warnings := CheckAndEvictPorts(ports)
	if len(warnings) > 0 {
		time.Sleep(300 * time.Millisecond)
	}

	ctx, cancel := context.WithCancel(context.Background())
	p := &LocalProxy{ctx: ctx, cancel: cancel}

	for _, f := range fwds {
		rh := f.RemoteHost
		if rh == "" {
			rh = "127.0.0.1"
		}
		lAddr := fmt.Sprintf("127.0.0.1:%d", f.LocalPort)
		rAddr := fmt.Sprintf("%s:%d", rh, f.RemotePort)

		ln, err := net.Listen("tcp", lAddr)
		if err != nil {
			_ = p.Close()
			return nil, warnings, fmt.Errorf("bind local proxy port %d: %w", f.LocalPort, err)
		}

		p.mu.Lock()
		p.listeners = append(p.listeners, ln)
		p.mu.Unlock()

		go p.serveProxy(ln, rAddr)
	}

	return p, warnings, nil
}

// Close stops all local proxy listeners and cancels in-flight connections.
func (p *LocalProxy) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	for _, ln := range p.listeners {
		_ = ln.Close()
	}
	p.listeners = nil
	return nil
}

// serveProxy accepts TCP connections on ln and proxies each to rAddr.
func (p *LocalProxy) serveProxy(ln net.Listener, rAddr string) {
	for {
		client, err := ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()

			upstream, err := (&net.Dialer{}).DialContext(p.ctx, "tcp", rAddr)
			if err != nil {
				return
			}
			defer upstream.Close()

			done := make(chan struct{}, 2)
			go func() { _, _ = io.Copy(upstream, c); done <- struct{}{} }()
			go func() { _, _ = io.Copy(c, upstream); done <- struct{}{} }()
			<-done
		}(client)
	}
}

// CheckAndEvictPorts evicts any processes listening on the given local TCP
// ports. Returns human-readable descriptions of each evicted process so the
// caller can display warnings (e.g. "evicted PID 12345 from port 8080").
func CheckAndEvictPorts(ports []int) []string {
	var warnings []string
	for _, p := range ports {
		pids := listenersOnPort(p)
		for _, pid := range pids {
			if pid <= 1 {
				continue
			}
			proc, err := os.FindProcess(pid)
			if err != nil {
				continue
			}
			warnings = append(warnings, fmt.Sprintf("evicted PID %d from port %d", pid, p))
			_ = proc.Signal(os.Interrupt)
			time.Sleep(200 * time.Millisecond)
			_ = proc.Kill()
		}
	}
	return warnings
}
