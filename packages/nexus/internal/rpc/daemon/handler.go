// Package daemon provides JSON-RPC handlers for daemon info and settings.
package daemon

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"

	rpce "github.com/oursky/nexus/packages/nexus/internal/rpc/errors"
	"github.com/oursky/nexus/packages/nexus/internal/rpc/registry"
)

// NodeInfoProvider supplies node identity and capability data.
type NodeInfoProvider interface {
	NodeName() string
	NodeTags() []string
	Capabilities() []Capability
}

// Capability describes a daemon capability.
type Capability struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
}

// SSHKeyProvider supplies the daemon's SSH public key for cross-host sync.
type SSHKeyProvider interface {
	PublicKey() (string, error)
}

// Option configures optional Handler dependencies.
type Option func(*Handler)

// WithLogPath sets the daemon log file path exposed via daemon.log.tail.
func WithLogPath(path string) Option {
	return func(h *Handler) { h.logPath = path }
}

// WithSSHKeyProvider sets the SSH key provider for daemon.sync-ssh-key.
func WithSSHKeyProvider(p SSHKeyProvider) Option {
	return func(h *Handler) { h.sshKey = p }
}

// WithMacTunnelConfig sets the callback invoked when daemon.set-mac-tunnel is called.
func WithMacTunnelConfig(fn func(port int, macUser, macPath string)) Option {
	return func(h *Handler) { h.setMacTunnelConfig = fn }
}

// MacTunnelGetter allows the handler to read stored tunnel config for status responses.
type MacTunnelGetter interface {
	GetMacTunnelConfig() (port int, macUser, macPath string, ok bool)
}

// WithMacTunnelGetter sets the getter used to include tunnel info in daemon.status.
func WithMacTunnelGetter(g MacTunnelGetter) Option {
	return func(h *Handler) { h.macTunnelGetter = g }
}

// Handler exposes daemon.* and node.* RPC methods.
type Handler struct {
	node               NodeInfoProvider
	logPath            string
	sshKey             SSHKeyProvider
	setMacTunnelConfig func(port int, macUser, macPath string)
	macTunnelGetter    MacTunnelGetter
}

// New creates a Handler backed by the given NodeInfoProvider.
// node may be nil, in which case node.info returns empty data.
func New(node NodeInfoProvider, opts ...Option) *Handler {
	h := &Handler{node: node}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Register wires all handled methods into reg.
func (h *Handler) Register(reg registry.Registry) {
	reg.Register("node.info", h.nodeInfo)
	reg.Register("daemon.log.tail", h.daemonLogTail)
	reg.Register("daemon.sync-ssh-key", h.syncSSHKey)
	reg.Register("daemon.set-mac-tunnel", h.setMacTunnel)
	reg.Register("daemon.status", h.daemonStatus)
}

// ── DTOs ─────────────────────────────────────────────────────────────────────

type nodeIdentity struct {
	Name string   `json:"name,omitempty"`
	Tags []string `json:"tags,omitempty"`
}

type nodeInfoResult struct {
	Node         nodeIdentity `json:"node"`
	Capabilities []Capability `json:"capabilities"`
}

// ── handlers ─────────────────────────────────────────────────────────────────

func (h *Handler) nodeInfo(_ context.Context, raw json.RawMessage) (any, error) {
	res := &nodeInfoResult{
		Capabilities: []Capability{},
	}
	if h.node != nil {
		res.Node = nodeIdentity{
			Name: h.node.NodeName(),
			Tags: h.node.NodeTags(),
		}
		res.Capabilities = h.node.Capabilities()
		if res.Capabilities == nil {
			res.Capabilities = []Capability{}
		}
	}
	return res, nil
}

func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	norm := raw
	if len(norm) == 0 || string(norm) == "null" {
		norm = []byte("{}")
	}
	if err := json.Unmarshal(norm, &v); err != nil {
		return v, rpce.InvalidParams("daemon.invalid_params", "invalid params: "+err.Error())
	}
	return v, nil
}

// ── daemon.log.tail ───────────────────────────────────────────────────────────

type logTailReq struct {
	Lines int `json:"lines,omitempty"`
}

type logTailRes struct {
	Lines []string `json:"lines"`
	Path  string   `json:"path"`
}

func (h *Handler) daemonLogTail(_ context.Context, raw json.RawMessage) (any, error) {
	req, err := decode[logTailReq](raw)
	if err != nil {
		return nil, err
	}
	if req.Lines <= 0 {
		req.Lines = 200
	}
	if h.logPath == "" {
		return &logTailRes{Lines: []string{}, Path: ""}, nil
	}
	lines, err := tailLines(h.logPath, req.Lines)
	if err != nil {
		return &logTailRes{Lines: []string{}, Path: h.logPath}, nil
	}
	return &logTailRes{Lines: lines, Path: h.logPath}, nil
}

// tailLines reads up to maxLines lines from the end of a file.
// It reads at most 256 KB from the end to avoid loading large files.
type syncSSHKeyRes struct {
	PublicKey string `json:"publicKey"`
}

func (h *Handler) syncSSHKey(_ context.Context, raw json.RawMessage) (any, error) {
	if h.sshKey == nil {
		return nil, rpce.Internal("daemon.ssh_not_configured", "SSH key management not configured")
	}
	pubKey, err := h.sshKey.PublicKey()
	if err != nil {
		return nil, rpce.Internal("daemon.ssh_key_error", "failed to get SSH public key: "+err.Error())
	}
	return &syncSSHKeyRes{PublicKey: pubKey}, nil
}

// ── daemon.set-mac-tunnel ────────────────────────────────────────────────────

type setMacTunnelReq struct {
	ReversePort int    `json:"reversePort"`
	MacUser     string `json:"macUser"`
	MacPath     string `json:"macPath"`
}

type setMacTunnelRes struct {
	OK   bool `json:"ok"`
	Port int  `json:"port"`
}

func (h *Handler) setMacTunnel(_ context.Context, raw json.RawMessage) (any, error) {
	req, err := decode[setMacTunnelReq](raw)
	if err != nil {
		return nil, err
	}
	if req.ReversePort == 0 {
		return nil, rpce.InvalidParams("daemon.missing_reverse_port", "missing reversePort")
	}
	if h.setMacTunnelConfig != nil {
		h.setMacTunnelConfig(req.ReversePort, req.MacUser, req.MacPath)
	}
	return &setMacTunnelRes{OK: true, Port: req.ReversePort}, nil
}

// ── daemon.status ─────────────────────────────────────────────────────────────

func (h *Handler) daemonStatus(_ context.Context, _ json.RawMessage) (any, error) {
	result := map[string]any{}
	if h.macTunnelGetter != nil {
		if port, user, path, ok := h.macTunnelGetter.GetMacTunnelConfig(); ok {
			result["macTunnel"] = map[string]any{
				"port":    port,
				"macUser": user,
				"macPath": path,
			}
		}
	}
	return result, nil
}

func tailLines(path string, maxLines int) ([]string, error) {	const bufCap = 256 * 1024
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	offset := fi.Size() - int64(bufCap)
	if offset < 0 {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	text := strings.TrimRight(string(data), "\n")
	if text == "" {
		return []string{}, nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return lines, nil
}
