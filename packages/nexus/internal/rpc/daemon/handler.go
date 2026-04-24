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

// Option configures optional Handler dependencies.
type Option func(*Handler)

// WithLogPath sets the daemon log file path exposed via daemon.log.tail.
func WithLogPath(path string) Option {
	return func(h *Handler) { h.logPath = path }
}

// Handler exposes daemon.* and node.* RPC methods.
type Handler struct {
	node    NodeInfoProvider
	logPath string
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
		return v, rpce.InvalidParams("invalid params: " + err.Error())
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
func tailLines(path string, maxLines int) ([]string, error) {
	const bufCap = 256 * 1024
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
