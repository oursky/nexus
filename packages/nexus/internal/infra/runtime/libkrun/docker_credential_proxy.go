//go:build linux

package libkrun

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// dockerCredHelperSockPath returns the Unix socket path for the Docker credential
// helper proxy of a workspace (the path libkrun maps to guest vsock port 10793).
func dockerCredHelperSockPath(workDir string) string {
	return filepath.Join(workDir, fmt.Sprintf("vsock_%d.sock", DockerCredHelperVSockPort))
}

// startDockerCredHelperProxy starts a Unix socket listener that proxies Docker
// credential helper requests from the guest to the host's credential helpers.
//
// Wire protocol (guest → host per connection):
//
//	verb\n                    — Docker credential helper verb (get, list, store, erase)
//	<4-byte big-endian len>   — length of the stdin payload
//	<payload bytes>           — stdin to pass to the credential helper
//
// Response (host → guest per connection):
//
//	<1-byte exit code>        — 0 = success, non-zero = error
//	<4-byte big-endian len>   — length of stdout
//	<stdout bytes>            — output from the credential helper
func startDockerCredHelperProxy(sockPath string) {
	_ = os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		log.Printf("[libkrun] docker cred proxy: listen on %s: %v", sockPath, err)
		return
	}

	log.Printf("[libkrun] docker cred proxy listening on %s", sockPath)

	go func() {
		defer ln.Close()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleDockerCredConn(conn)
		}
	}()
}

func handleDockerCredConn(conn net.Conn) {
	defer conn.Close()

	// Read verb line.
	var verbBuf bytes.Buffer
	oneByte := make([]byte, 1)
	for {
		n, err := conn.Read(oneByte)
		if n > 0 {
			if oneByte[0] == '\n' {
				break
			}
			verbBuf.WriteByte(oneByte[0])
		}
		if err != nil {
			log.Printf("[libkrun] docker cred proxy: read verb: %v", err)
			return
		}
	}
	verb := strings.TrimSpace(verbBuf.String())
	if verb == "" {
		log.Printf("[libkrun] docker cred proxy: empty verb")
		return
	}

	// Read 4-byte big-endian payload length.
	var payloadLen uint32
	if err := binary.Read(conn, binary.BigEndian, &payloadLen); err != nil {
		log.Printf("[libkrun] docker cred proxy: read payload len: %v", err)
		return
	}

	// Read payload.
	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(conn, payload); err != nil {
		log.Printf("[libkrun] docker cred proxy: read payload: %v", err)
		return
	}

	// Resolve which credential helper to use on the host.
	helperName, inlineResp, err := resolveDockerCredential(verb, payload)
	if err != nil {
		log.Printf("[libkrun] docker cred proxy: resolve: %v", err)
		sendDockerCredResponse(conn, 1, []byte(err.Error()))
		return
	}

	// If inline auths satisfied the request directly, return the pre-built response.
	if inlineResp != nil {
		sendDockerCredResponse(conn, 0, inlineResp)
		return
	}

	// Execute the credential helper.
	helperBin := "docker-credential-" + helperName
	cmd := exec.Command(helperBin, verb)
	cmd.Stdin = bytes.NewReader(payload)
	out, cmdErr := cmd.Output()

	exitCode := byte(0)
	if cmdErr != nil {
		if ee, ok := cmdErr.(*exec.ExitError); ok {
			exitCode = byte(ee.ExitCode())
			// Use stderr as the output on error.
			out = ee.Stderr
		} else {
			exitCode = 1
			out = []byte(cmdErr.Error())
		}
	}

	sendDockerCredResponse(conn, exitCode, out)
}

// dockerConfig is the parsed ~/.docker/config.json structure.
type dockerConfig struct {
	CredsStore  string                      `json:"credsStore"`
	CredHelpers map[string]string           `json:"credHelpers"`
	Auths       map[string]dockerConfigAuth `json:"auths"`
}

// dockerConfigAuth holds the inline credential fields for a registry.
type dockerConfigAuth struct {
	Auth     string `json:"auth"` // base64("user:pass")
	Username string `json:"username"`
	Password string `json:"password"`
	Email    string `json:"email"`
}

// resolveDockerCredential is the single entry point for all credential resolution.
//
// It returns one of:
//   - (helperName, nil, nil)       — delegate to docker-credential-<helperName>
//   - ("", responseJSON, nil)      — inline creds resolved; responseJSON is ready to send
//   - ("", nil, err)               — no credentials found
//
// Resolution order (mirrors Docker's own precedence):
//  1. Per-registry credHelpers entry
//  2. Global credsStore helper
//  3. Inline auths in config.json
func resolveDockerCredential(verb string, payload []byte) (helperName string, inlineResp []byte, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", nil, fmt.Errorf("home dir: %w", err)
	}
	cfgPath := filepath.Join(home, ".docker", "config.json")
	cfgData, err := os.ReadFile(cfgPath)
	if err != nil {
		return "", nil, fmt.Errorf("read docker config: %w", err)
	}

	var cfg dockerConfig
	if err := json.Unmarshal(cfgData, &cfg); err != nil {
		return "", nil, fmt.Errorf("parse docker config: %w", err)
	}

	// "list" verb: return all known credentials.
	// Try helper first; fall back to synthesising from inline auths.
	if verb == "list" {
		if cfg.CredsStore != "" {
			return cfg.CredsStore, nil, nil
		}
		if len(cfg.CredHelpers) > 0 {
			// Pick any helper — list is global; just use the first one found.
			for _, h := range cfg.CredHelpers {
				return h, nil, nil
			}
		}
		// Synthesise list from inline auths.
		result := map[string]string{}
		for regURL, a := range cfg.Auths {
			user, _, ok := decodeInlineAuth(a)
			if ok && user != "" {
				result[regURL] = user
			}
		}
		if len(result) > 0 {
			out, _ := json.Marshal(result)
			return "", out, nil
		}
		return "", nil, fmt.Errorf("no credentials configured")
	}

	// All other verbs (get, store, erase): extract registry key from payload.
	registry := extractRegistry(payload)

	// 1. Per-registry credHelpers.
	if h, ok := cfg.CredHelpers[registry]; ok {
		return h, nil, nil
	}

	// 2. Global credsStore.
	if cfg.CredsStore != "" {
		return cfg.CredsStore, nil, nil
	}

	// 3. Inline auths — only meaningful for "get".
	if verb == "get" {
		// Try exact URL match first, then hostname-only match.
		for _, key := range []string{payload2URL(payload), registry} {
			if a, ok := cfg.Auths[key]; ok {
				resp, ok := buildGetResponse(key, a)
				if ok {
					return "", resp, nil
				}
			}
		}
		// Also try with https:// prefix added/stripped for robustness.
		candidates := []string{
			"https://" + registry,
			registry,
		}
		for _, key := range candidates {
			if a, ok := cfg.Auths[key]; ok {
				resp, ok := buildGetResponse(key, a)
				if ok {
					return "", resp, nil
				}
			}
		}
	}

	return "", nil, fmt.Errorf("no credentials found for %q", registry)
}

// extractRegistry normalises a docker credential helper payload to a bare hostname.
// Docker sends either a plain URL string or a JSON object with a ServerURL field.
func extractRegistry(payload []byte) string {
	s := strings.TrimSpace(string(payload))
	// JSON payload (store/erase sends {"ServerURL":"...","Username":"...","Secret":"..."})
	if strings.HasPrefix(s, "{") {
		var obj struct {
			ServerURL string `json:"ServerURL"`
		}
		if json.Unmarshal(payload, &obj) == nil && obj.ServerURL != "" {
			s = obj.ServerURL
		}
	}
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	if idx := strings.IndexByte(s, '/'); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

// payload2URL returns the raw URL string from a get/erase payload (no JSON parsing).
func payload2URL(payload []byte) string {
	return strings.TrimSpace(string(payload))
}

// decodeInlineAuth decodes the base64 "auth" field or uses explicit username/password.
func decodeInlineAuth(a dockerConfigAuth) (username, password string, ok bool) {
	if a.Username != "" && a.Password != "" {
		return a.Username, a.Password, true
	}
	if a.Auth != "" {
		decoded, err := base64.StdEncoding.DecodeString(a.Auth)
		if err != nil {
			return "", "", false
		}
		parts := strings.SplitN(string(decoded), ":", 2)
		if len(parts) == 2 {
			return parts[0], parts[1], true
		}
	}
	return "", "", false
}

// buildGetResponse builds the JSON response for a "get" verb from inline auth config.
// Returns the JSON bytes and true if successful, or nil and false if no usable creds.
func buildGetResponse(serverURL string, a dockerConfigAuth) ([]byte, bool) {
	username, password, ok := decodeInlineAuth(a)
	if !ok {
		return nil, false
	}
	resp := map[string]string{
		"ServerURL": serverURL,
		"Username":  username,
		"Secret":    password,
	}
	if a.Email != "" {
		resp["Email"] = a.Email
	}
	out, err := json.Marshal(resp)
	if err != nil {
		return nil, false
	}
	return out, true
}

func sendDockerCredResponse(conn net.Conn, exitCode byte, data []byte) {
	buf := make([]byte, 1+4+len(data))
	buf[0] = exitCode
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(data)))
	copy(buf[5:], data)
	_, _ = conn.Write(buf)
}
