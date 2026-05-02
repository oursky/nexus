package bundle

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"sync"

	"github.com/oursky/nexus/packages/nexus/internal/infra/dockercompose"
)

const vmGuestIP = "192.168.127.2"

// discoverBundlePorts scans the workspace directory for docker-compose files
// and returns the unique host ports that should be forwarded from localhost
// to the VM guest.
func discoverBundlePorts(workspaceDir string) []int {
	ports, err := dockercompose.DiscoverPublishedPortsFromYAML(workspaceDir)
	if err != nil {
		return nil
	}
	seen := make(map[int]bool)
	var result []int
	for _, p := range ports {
		if seen[p.HostPort] {
			continue
		}
		seen[p.HostPort] = true
		result = append(result, p.HostPort)
	}
	return result
}

// portForwardSet manages a set of TCP listeners that proxy connections from
// localhost to the VM guest.
type portForwardSet struct {
	mu        sync.Mutex
	listeners []net.Listener
	wg        sync.WaitGroup
}

// startPortForwards starts TCP listeners on localhost for each port in ports.
// Incoming connections are proxied to the VM guest IP. Returns a cancel
// function that stops all listeners and waits for active proxies to finish.
func startPortForwards(ctx context.Context, ports []int) func() {
	pf := &portForwardSet{}
	for _, port := range ports {
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bundle run: port forward: cannot bind %s: %v\n", addr, err)
			continue
		}
		pf.listeners = append(pf.listeners, ln)
		pf.wg.Add(1)
		go pf.serve(ctx, ln, port)
	}
	if len(pf.listeners) > 0 {
		fmt.Fprintf(os.Stderr, "bundle run: port forwards active: %v\n", ports)
	}
	return func() {
		pf.mu.Lock()
		for _, ln := range pf.listeners {
			_ = ln.Close()
		}
		pf.mu.Unlock()
		pf.wg.Wait()
	}
}

func (pf *portForwardSet) serve(ctx context.Context, listener net.Listener, guestPort int) {
	defer pf.wg.Done()
	for {
		clientConn, err := listener.Accept()
		if err != nil {
			// Listener closed — exit.
			return
		}
		go pf.handleConn(ctx, clientConn, guestPort)
	}
}

func (pf *portForwardSet) handleConn(ctx context.Context, clientConn net.Conn, guestPort int) {
	defer clientConn.Close()

	guestAddr := fmt.Sprintf("%s:%d", vmGuestIP, guestPort)
	d := net.Dialer{}
	upstream, err := d.DialContext(ctx, "tcp", guestAddr)
	if err != nil {
		return
	}
	defer upstream.Close()

	proxyConns(clientConn, upstream)
}

func proxyConns(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(b, a)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(a, b)
		done <- struct{}{}
	}()
	<-done
	_ = a.Close()
	_ = b.Close()
}
