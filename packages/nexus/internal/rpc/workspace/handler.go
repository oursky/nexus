package workspace

import (
	"context"
	"encoding/json"

	appsvc "github.com/oursky/nexus/packages/nexus/internal/app/workspace"
	rpce "github.com/oursky/nexus/packages/nexus/internal/rpc/errors"
	"github.com/oursky/nexus/packages/nexus/internal/rpc/registry"
)

// SerialLogProvider returns the filesystem path to a workspace's VM serial/console log.
type SerialLogProvider interface {
	SerialLogPath(workspaceID string) (string, error)
}

// HandlerOption configures optional Handler dependencies.
type HandlerOption func(*Handler)

// WithSerialLogProvider wires a VM serial log provider into the workspace handler.
func WithSerialLogProvider(p SerialLogProvider) HandlerOption {
	return func(h *Handler) { h.serialLogProvider = p }
}

// Handler holds the workspace app service and registers all workspace RPC methods.
type Handler struct {
	svc               *appsvc.Service
	serialLogProvider SerialLogProvider
}

// NewHandler constructs a Handler backed by the given Service.
func NewHandler(svc *appsvc.Service, opts ...HandlerOption) *Handler {
	h := &Handler{svc: svc}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Register wires all workspace.* methods into reg.
func (h *Handler) Register(reg registry.Registry) {
	reg.Register("workspace.info", h.wrap(h.handleInfo))
	reg.Register("workspace.create", h.wrap(h.handleCreate))
	reg.Register("workspace.list", h.wrap(h.handleList))
	reg.Register("workspace.remove", h.wrap(h.handleRemove))
	reg.Register("workspace.stop", h.wrap(h.handleStop))
	reg.Register("workspace.start", h.wrap(h.handleStart))
	reg.Register("workspace.restore", h.wrap(h.handleRestore))
	reg.Register("workspace.fork", h.wrap(h.handleFork))
	reg.Register("workspace.ready", h.wrap(h.handleReady))
	reg.Register("workspace.discover-ports", h.wrap(h.handleDiscoverPorts))
	reg.Register("workspace.sshcheck", h.wrap(h.handleSSHCheck))
	reg.Register("workspace.serial-log", h.wrap(h.handleSerialLog))
	reg.Register("workspace.nexusfile", h.wrap(h.handleNexusfile))
	reg.Register("workspace.archive", h.wrap(h.handleArchive))
}

func (h *Handler) wrap(fn func(ctx context.Context, raw json.RawMessage) (any, error)) registry.HandlerFunc {
	return fn
}

func decode[Req any](raw json.RawMessage) (Req, error) {
	var req Req
	norm := raw
	if len(norm) == 0 || string(norm) == "null" {
		norm = []byte("{}")
	}
	if err := json.Unmarshal(norm, &req); err != nil {
		return req, rpce.InvalidParams("workspace.invalid_params", "invalid params: "+err.Error())
	}
	return req, nil
}
