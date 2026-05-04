package workspace

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	appws "github.com/oursky/nexus/packages/nexus/internal/app/workspace"
	"github.com/oursky/nexus/packages/nexus/internal/domain/bundle"
	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
	rpce "github.com/oursky/nexus/packages/nexus/internal/rpc/errors"
)

// ── DTOs ──────────────────────────────────────────────────────────────────────

type infoReq struct {
	ID string `json:"id"`
}
type infoRes struct {
	Workspace *workspace.Workspace `json:"workspace"`
}

type createReq struct {
	Spec workspace.CreateSpec `json:"spec"`
}
type createRes struct {
	Workspace *workspace.Workspace `json:"workspace"`
}

type listRes struct {
	Workspaces []*workspace.Workspace `json:"workspaces"`
}

type removeReq struct {
	ID string `json:"id"`
}
type removeRes struct {
	Removed bool `json:"removed"`
}

type stopReq struct {
	ID string `json:"id"`
}
type stopRes struct {
	Stopped   bool                 `json:"stopped"`
	Workspace *workspace.Workspace `json:"workspace,omitempty"`
}

type startReq struct {
	ID string `json:"id"`
}
type startRes struct {
	Workspace *workspace.Workspace `json:"workspace"`
}

type restoreReq struct {
	ID string `json:"id"`
}
type restoreRes struct {
	Restored  bool                 `json:"restored"`
	Workspace *workspace.Workspace `json:"workspace,omitempty"`
}

type forkReq struct {
	ID                 string `json:"id"`
	ChildWorkspaceName string `json:"childWorkspaceName,omitempty"`
	ChildRef           string `json:"childRef"`
}
type forkRes struct {
	Forked    bool                 `json:"forked"`
	Workspace *workspace.Workspace `json:"workspace,omitempty"`
}

type readyReq struct {
	ID string `json:"id"`
}
type readyRes struct {
	Ready bool `json:"ready"`
}

// ── Handlers ──────────────────────────────────────────────────────────────────

func (h *Handler) handleInfo(ctx context.Context, raw json.RawMessage) (any, error) {
	req, err := decode[infoReq](raw)
	if err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, rpce.InvalidParams("workspace.invalid_params", "id is required")
	}
	ws, err := h.svc.Info(ctx, req.ID)
	if err != nil {
		return nil, mapErr(err)
	}
	return &infoRes{Workspace: ws}, nil
}

func (h *Handler) handleCreate(ctx context.Context, raw json.RawMessage) (any, error) {
	req, err := decode[createReq](raw)
	if err != nil {
		return nil, err
	}
	ws, err := h.svc.Create(ctx, req.Spec)
	if err != nil {
		return nil, mapErr(err)
	}
	return &createRes{Workspace: ws}, nil
}

func (h *Handler) handleList(ctx context.Context, raw json.RawMessage) (any, error) {
	all, err := h.svc.List(ctx)
	if err != nil {
		return nil, rpce.Internal("workspace.internal", err.Error())
	}
	return &listRes{Workspaces: all}, nil
}

func (h *Handler) handleRemove(ctx context.Context, raw json.RawMessage) (any, error) {
	req, err := decode[removeReq](raw)
	if err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, rpce.InvalidParams("workspace.invalid_params", "id is required")
	}
	if err := h.svc.Remove(ctx, req.ID); err != nil {
		return nil, mapErr(err)
	}
	return &removeRes{Removed: true}, nil
}

func (h *Handler) handleStop(ctx context.Context, raw json.RawMessage) (any, error) {
	req, err := decode[stopReq](raw)
	if err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, rpce.InvalidParams("workspace.invalid_params", "id is required")
	}
	ws, err := h.svc.Stop(ctx, req.ID)
	if err != nil {
		return nil, mapErr(err)
	}
	return &stopRes{Stopped: true, Workspace: ws}, nil
}

func (h *Handler) handleStart(ctx context.Context, raw json.RawMessage) (any, error) {
	req, err := decode[startReq](raw)
	if err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, rpce.InvalidParams("workspace.invalid_params", "id is required")
	}
	ws, err := h.svc.Start(ctx, req.ID)
	if err != nil {
		return nil, mapErr(err)
	}
	return &startRes{Workspace: ws}, nil
}

func (h *Handler) handleRestore(ctx context.Context, raw json.RawMessage) (any, error) {
	req, err := decode[restoreReq](raw)
	if err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, rpce.InvalidParams("workspace.invalid_params", "id is required")
	}
	ws, err := h.svc.Restore(ctx, req.ID)
	if err != nil {
		return nil, mapErr(err)
	}
	return &restoreRes{Restored: true, Workspace: ws}, nil
}

func (h *Handler) handleFork(ctx context.Context, raw json.RawMessage) (any, error) {
	req, err := decode[forkReq](raw)
	if err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, rpce.InvalidParams("workspace.invalid_params", "id is required")
	}
	ws, err := h.svc.Fork(ctx, req.ID, appws.ForkSpec{
		ChildWorkspaceName: req.ChildWorkspaceName,
		ChildRef:           req.ChildRef,
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return &forkRes{Forked: true, Workspace: ws}, nil
}

func (h *Handler) handleReady(ctx context.Context, raw json.RawMessage) (any, error) {
	req, err := decode[readyReq](raw)
	if err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, rpce.InvalidParams("workspace.invalid_params", "id is required")
	}
	ready, err := h.svc.Ready(ctx, req.ID)
	if err != nil {
		return nil, mapErr(err)
	}
	return &readyRes{Ready: ready}, nil
}

func mapErr(err error) error {
	if errors.Is(err, workspace.ErrNotFound) {
		return rpce.NotFound("workspace.not_found", "workspace not found")
	}
	if errors.Is(err, workspace.ErrInvalidTransition) {
		return rpce.InvalidParams("workspace.invalid_params", err.Error())
	}
	if rpcErr, ok := err.(*rpce.RPCError); ok {
		return rpcErr
	}
	return rpce.Internal("workspace.internal", fmt.Sprintf("internal error: %v", err))
}

// ── Discover Ports ──────────────────────────────────────────────────────────

type discoverPortsReq struct {
	ID string `json:"id"`
}

func (h *Handler) handleDiscoverPorts(ctx context.Context, raw json.RawMessage) (any, error) {
	req, err := decode[discoverPortsReq](raw)
	if err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, rpce.InvalidParams("workspace.invalid_params", "id is required")
	}
	ports, err := h.svc.DiscoverPorts(ctx, req.ID)
	if err != nil {
		return nil, mapErr(err)
	}
	return ports, nil
}

// ── Serial log ────────────────────────────────────────────────────────────────

type serialLogReq struct {
	ID    string `json:"id"`
	Lines int    `json:"lines,omitempty"`
}

type serialLogRes struct {
	Lines     []string `json:"lines"`
	Path      string   `json:"path"`
	Available bool     `json:"available"`
}

func (h *Handler) handleSerialLog(_ context.Context, raw json.RawMessage) (any, error) {
	req, err := decode[serialLogReq](raw)
	if err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, rpce.InvalidParams("workspace.invalid_params", "id is required")
	}
	if req.Lines <= 0 {
		req.Lines = 200
	}

	if h.serialLogProvider == nil {
		return &serialLogRes{Lines: []string{}, Path: "", Available: false}, nil
	}

	logPath, err := h.serialLogProvider.SerialLogPath(req.ID)
	if err != nil {
		// Workspace not running or no serial log — return empty result, not error.
		return &serialLogRes{Lines: []string{}, Path: "", Available: false}, nil
	}

	lines, err := tailLines(logPath, req.Lines)
	if err != nil {
		return &serialLogRes{Lines: []string{}, Path: logPath, Available: false}, nil
	}
	return &serialLogRes{Lines: lines, Path: logPath, Available: true}, nil
}

// tailLines reads up to maxLines lines from the end of a file.
// It reads at most 64 KB from the end: after JSON-encoding (ANSI escape
// sequences expand ~6×) the response stays well under 1 MB, which is the
// default URLSessionWebSocketTask limit on Apple platforms.
func tailLines(path string, maxLines int) ([]string, error) {
	const bufCap = 64 * 1024
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

// ── workspace.nexusfile ──────────────────────────────────────────────────────

type nexusfileReq struct {
	ID string `json:"id"`
}

type nexusfileRes struct {
	WorkspaceIntent bundle.WorkspaceIntent `json:"workspaceIntent"`
}

// ── workspace.archive ────────────────────────────────────────────────────────

type archiveReq struct {
	ID string `json:"id"`
}

type archiveRes struct {
	// ArchiveB64 is a base64-encoded tar.gz of the workspace repo directory.
	ArchiveB64 string `json:"archiveB64"`
}

// handleArchive creates a tar.gz of the workspace's repo directory and returns it
// as a base64-encoded string. The caller (CLI exporter) decodes and writes it into
// the bundle payload as payload/workspace.tar.gz.
func (h *Handler) handleArchive(ctx context.Context, raw json.RawMessage) (any, error) {
	req, err := decode[archiveReq](raw)
	if err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, rpce.InvalidParams("workspace.invalid_params", "id is required")
	}
	ws, err := h.svc.Info(ctx, req.ID)
	if err != nil {
		return nil, mapErr(err)
	}
	if ws.Repo == "" {
		return nil, rpce.InvalidParams("workspace.no_repo", "workspace has no repo directory")
	}

	// Create tar.gz of the repo directory in memory.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	// archiveSkipDirs are directory names that contain host-specific configuration
	// or secrets that must not be included in a portable bundle.
	archiveSkipDirs := map[string]bool{
		".git":         true, // version-control internals, large
		"hostconfig":   true, // host-specific volume/config overrides
		"node_modules": true, // reproducible via init; large
	}

	// archiveSkipFiles are exact file names that contain host-specific configuration
	// or secrets and must not be included in a portable bundle.
	archiveSkipFiles := map[string]bool{
		".env":                         true, // host secrets / local overrides
		"docker-compose.override.yml":  true, // host-specific compose overrides
		"docker-compose.override.yaml": true,
	}

	baseDir := filepath.Clean(ws.Repo)
	walkErr := filepath.Walk(baseDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// Skip host-specific directories.
		if info.IsDir() && archiveSkipDirs[info.Name()] {
			return filepath.SkipDir
		}
		// Skip host-specific files.
		if !info.IsDir() && archiveSkipFiles[info.Name()] {
			return nil
		}

		rel, relErr := filepath.Rel(baseDir, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}

		linkTarget := ""
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, _ = os.Readlink(path)
		}
		hdr, hdrErr := tar.FileInfoHeader(info, linkTarget)
		if hdrErr != nil {
			return hdrErr
		}
		hdr.Name = rel
		if info.IsDir() {
			hdr.Name += "/"
		}
		if hdrErr := tw.WriteHeader(hdr); hdrErr != nil {
			return hdrErr
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, fErr := os.Open(path)
		if fErr != nil {
			return fErr
		}
		defer f.Close()
		_, copyErr := io.Copy(tw, f)
		return copyErr
	})
	if walkErr != nil {
		return nil, rpce.Internal("workspace.archive_failed", fmt.Sprintf("walk repo dir: %v", walkErr))
	}
	if closeErr := tw.Close(); closeErr != nil {
		return nil, rpce.Internal("workspace.archive_failed", fmt.Sprintf("close tar: %v", closeErr))
	}
	if closeErr := gw.Close(); closeErr != nil {
		return nil, rpce.Internal("workspace.archive_failed", fmt.Sprintf("close gzip: %v", closeErr))
	}

	return &archiveRes{ArchiveB64: base64.StdEncoding.EncodeToString(buf.Bytes())}, nil
}

func (h *Handler) handleNexusfile(ctx context.Context, raw json.RawMessage) (any, error) {
	req, err := decode[nexusfileReq](raw)
	if err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, rpce.InvalidParams("workspace.invalid_params", "id is required")
	}
	ws, err := h.svc.Info(ctx, req.ID)
	if err != nil {
		return nil, mapErr(err)
	}
	if ws.Repo == "" {
		return &nexusfileRes{WorkspaceIntent: bundle.WorkspaceIntent{}}, nil
	}
	// Read the Nexusfile from the workspace repo checkout directory on this (daemon) host.
	// Non-fatal: if missing or unparseable, return empty intent.
	intent, intentErr := bundle.WorkspaceIntentFromNexusfile(ws.Repo)
	if intentErr != nil {
		return &nexusfileRes{WorkspaceIntent: bundle.WorkspaceIntent{}}, nil
	}
	return &nexusfileRes{WorkspaceIntent: intent}, nil
}
