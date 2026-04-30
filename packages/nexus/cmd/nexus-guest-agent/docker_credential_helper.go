//go:build linux

package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mdlayher/vsock"
)

const (
	dockerCredHostCID  = uint32(2) // VMADDR_CID_HOST
	dockerCredSockPath = "/tmp/nexus-docker-cred.sock"
)

// runDockerCredentialHelper implements the docker credential helper protocol,
// proxying all requests to the host daemon via vsock.
//
// Docker invokes credential helpers as:
//
//	docker-credential-nexus <verb>
//
// where verb is one of: get, store, erase, list.
// stdin carries the payload (e.g. registry URL for get/store/erase, empty for list).
// stdout should carry the response JSON; exit code 0 = success.
func runDockerCredentialHelper() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: docker-credential-nexus <verb>")
		os.Exit(1)
	}
	verb := strings.TrimSpace(os.Args[1])

	payload, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "docker-credential-nexus: read stdin: %v\n", err)
		os.Exit(1)
	}

	conn, err := vsock.Dial(dockerCredHostCID, dockerCredHelperVSockPort, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "docker-credential-nexus: dial host vsock %d: %v\n", dockerCredHelperVSockPort, err)
		os.Exit(1)
	}
	defer conn.Close()

	// Send: verb\n + 4-byte big-endian len + payload.
	var req bytes.Buffer
	req.WriteString(verb)
	req.WriteByte('\n')
	_ = binary.Write(&req, binary.BigEndian, uint32(len(payload)))
	req.Write(payload)
	if _, err := conn.Write(req.Bytes()); err != nil {
		fmt.Fprintf(os.Stderr, "docker-credential-nexus: send request: %v\n", err)
		os.Exit(1)
	}

	// Read response: 1-byte exit code + 4-byte len + data.
	exitByte := make([]byte, 1)
	if _, err := io.ReadFull(conn, exitByte); err != nil {
		fmt.Fprintf(os.Stderr, "docker-credential-nexus: read exit code: %v\n", err)
		os.Exit(1)
	}
	var respLen uint32
	if err := binary.Read(conn, binary.BigEndian, &respLen); err != nil {
		fmt.Fprintf(os.Stderr, "docker-credential-nexus: read response len: %v\n", err)
		os.Exit(1)
	}
	respData := make([]byte, respLen)
	if _, err := io.ReadFull(conn, respData); err != nil {
		fmt.Fprintf(os.Stderr, "docker-credential-nexus: read response: %v\n", err)
		os.Exit(1)
	}

	if exitByte[0] != 0 {
		fmt.Fprintf(os.Stderr, "%s", respData)
		os.Exit(int(exitByte[0]))
	}
	os.Stdout.Write(respData)
	os.Exit(0)
}

// startDockerCredHelperListener creates a Unix socket that docker-credential-nexus
// (running as a subprocess) can connect to. This is not used in the vsock-direct
// path but is kept for potential future use where the guest agent acts as a local
// relay instead of dialing vsock directly from each helper invocation.
//
// In the current implementation, docker-credential-nexus dials vsock directly —
// this function is a no-op stub that can be removed once the architecture is final.
func startDockerCredHelperListener() {
	// No-op: docker-credential-nexus dials vsock directly.
	emitDiagnostic("docker cred helper: vsock proxy ready on port %d", dockerCredHelperVSockPort)
}
