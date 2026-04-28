//go:build linux

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mdlayher/vsock"
	"golang.org/x/sys/unix"
)

// Request types
type execRequest struct {
	ID      string   `json:"id"`
	Type    string   `json:"type,omitempty"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	WorkDir string   `json:"workdir,omitempty"`
	Env     []string `json:"env,omitempty"`
	Stream  bool     `json:"stream,omitempty"`
	Data    string   `json:"data,omitempty"`
	Cols    int      `json:"cols,omitempty"`
	Rows    int      `json:"rows,omitempty"`
}

type execResponse struct {
	ID       string `json:"id"`
	Type     string `json:"type,omitempty"`
	Stream   string `json:"stream,omitempty"`
	Data     string `json:"data,omitempty"`
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

func serveConn(conn net.Conn) {
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	for {
		// Parse request
		var req execRequest
		if err := decoder.Decode(&req); err != nil {
			if err != io.EOF {
				log.Printf("Error decoding request: %v", err)
				// Try to send error response with request ID if available
				encoder.Encode(execResponse{ID: req.ID, ExitCode: 1, Stderr: fmt.Sprintf("decode error: %v", err)})
			}
			return
		}

		// Validate request ID is present
		if strings.TrimSpace(req.ID) == "" {
			log.Printf("Request missing ID field")
			encoder.Encode(execResponse{ExitCode: 1, Stderr: "request ID is required"})
			continue
		}

		if strings.TrimSpace(req.Type) != "" {
			handleShellRequest(req, encoder)
			continue
		}

		// Handle request
		resp := execResponse{}
		if req.Stream {
			resp = handleExecStreaming(req, encoder)
		} else {
			resp = handleExec(req)
		}

		// Send response
		if err := encoder.Encode(resp); err != nil {
			log.Printf("Error encoding response: %v", err)
			return
		}
	}
}

func handleShellRequest(req execRequest, encoder *json.Encoder) {
	switch req.Type {
	case "shell.open":
		handleShellOpen(req, encoder)
	case "shell.write":
		handleShellWrite(req, encoder)
	case "shell.resize":
		handleShellResize(req, encoder)
	case "shell.close":
		handleShellClose(req, encoder)
	default:
		_ = encoder.Encode(execResponse{ID: req.ID, Type: "result", ExitCode: 1, Stderr: "unknown shell request type"})
	}
}

// startSpotlightListener starts the spotlight port-forward vsock listener.
// Each accepted connection reads "FORWARD <port>\n" then proxies raw TCP to
// 127.0.0.1:<port> inside the guest. This lets the daemon host reach services
// that only listen on loopback without relying on bridge networking.
func startSpotlightListener() {
	port := defaultSpotlightVSockPort

	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		emitDiagnostic("spotlight listener: socket: %v", err)
		return
	}
	if err := unix.Bind(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_ANY, Port: port}); err != nil {
		_ = unix.Close(fd)
		emitDiagnostic("spotlight listener: bind vsock port %d: %v", port, err)
		return
	}
	if err := unix.Listen(fd, 64); err != nil {
		_ = unix.Close(fd)
		emitDiagnostic("spotlight listener: listen: %v", err)
		return
	}
	file := os.NewFile(uintptr(fd), "spotlight-vsock-listener")
	defer file.Close()

	listener, err := vsock.FileListener(file)
	if err != nil {
		_ = unix.Close(fd)
		emitDiagnostic("spotlight listener: FileListener: %v", err)
		return
	}
	defer listener.Close()

	emitDiagnostic("spotlight listener: ready on vsock port %d", port)
	for {
		conn, err := listener.Accept()
		if err != nil {
			emitDiagnostic("spotlight listener: accept: %v", err)
			return
		}
		go serveSpotlightForward(conn)
	}
}

// serveSpotlightForward reads "FORWARD <port>\n" and proxies to 127.0.0.1:<port>.
func serveSpotlightForward(conn net.Conn) {
	defer conn.Close()

	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		return
	}
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "FORWARD ") {
		_, _ = conn.Write([]byte("ERR invalid command\n"))
		return
	}
	portStr := strings.TrimPrefix(line, "FORWARD ")
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		_, _ = conn.Write([]byte("ERR invalid port\n"))
		return
	}

	target, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
	if err != nil {
		_, _ = conn.Write([]byte(fmt.Sprintf("ERR %v\n", err)))
		return
	}
	defer target.Close()

	if _, err := conn.Write([]byte("OK\n")); err != nil {
		return
	}

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(target, reader); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, target); done <- struct{}{} }()
	<-done
}

func resolveListener() (net.Listener, string, error) {
	if os.Getpid() == 1 || os.Getenv("AGENT_REQUIRE_VSOCK") == "1" {
		var lastErr error
		for attempt := 1; attempt <= 120; attempt++ {
			listener, err := listenVsock()
			if err == nil {
				emitDiagnostic("agent vsock listener ready after %d attempt(s)", attempt)
				return listener, "vsock", nil
			}
			lastErr = err
			if attempt == 1 || attempt%20 == 0 {
				emitDiagnostic("agent vsock listen attempt %d failed: %v", attempt, err)
			}
			time.Sleep(500 * time.Millisecond)
		}
		return nil, "", fmt.Errorf("listen vsock (required) failed: %w", lastErr)
	}

	if os.Getenv("AGENT_FORCE_TCP") == "1" {
		listener, err := listenTCP()
		return listener, "tcp", err
	}

	listener, err := listenVsock()
	if err == nil {
		return listener, "vsock", nil
	}

	tcpListener, tcpErr := listenTCP()
	if tcpErr != nil {
		return nil, "", fmt.Errorf("listen vsock: %w; listen tcp fallback: %v", err, tcpErr)
	}
	return tcpListener, "tcp-fallback", nil
}

func listenTCP() (net.Listener, error) {
	port := os.Getenv("AGENT_PORT")
	if port == "" {
		port = "8080"
	}
	return net.Listen("tcp", ":"+port)
}

func listenVsock() (net.Listener, error) {
	port := defaultAgentVSockPort
	if raw := strings.TrimSpace(os.Getenv("AGENT_VSOCK_PORT")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return nil, fmt.Errorf("invalid AGENT_VSOCK_PORT %q", raw)
		}
		port = uint32(parsed)
	}

	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, err
	}

	if err := unix.Bind(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_ANY, Port: port}); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}

	if err := unix.Listen(fd, 128); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}

	file := os.NewFile(uintptr(fd), "vsock-listener")
	defer file.Close()

	listener, err := vsock.FileListener(file)
	if err != nil {
		_ = unix.Close(fd)
		return nil, err
	}

	return listener, nil
}

// startSSHAgentProxy creates /tmp/ssh-agent.sock and forwards each connection
// to the host SSH agent via vsock CID 2 (the hypervisor/host), port 10790.
// This lets git and ssh inside the VM use the host's SSH agent without any
// private keys being present in the VM filesystem.
func startSSHAgentProxy() {
	const (
		sockPath  = "/tmp/ssh-agent.sock"
		hostCID   = uint32(2) // VMADDR_CID_HOST
		agentPort = vendingVSockPort
	)

	_ = os.Setenv("SSH_AUTH_SOCK", sockPath)
	_ = os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		emitDiagnostic("ssh-agent proxy: listen %s: %v", sockPath, err)
		return
	}
	if err := os.Chmod(sockPath, 0o600); err != nil {
		emitDiagnostic("ssh-agent proxy: chmod: %v", err)
	}
	emitDiagnostic("ssh-agent proxy: listening on %s → vsock host port %d", sockPath, agentPort)

	go func() {
		defer ln.Close()
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				host, err := vsock.Dial(hostCID, agentPort, nil)
				if err != nil {
					emitDiagnostic("ssh-agent proxy: vsock dial host:%d: %v", agentPort, err)
					return
				}
				defer host.Close()
				done := make(chan struct{}, 2)
				go func() { _, _ = io.Copy(host, c); done <- struct{}{} }()
				go func() { _, _ = io.Copy(c, host); done <- struct{}{} }()
				<-done
			}(client)
		}
	}()
}
