package pty

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const scrollbackBytes = 128 * 1024 // 128 KB per session

// Notifier pushes server-initiated notifications to a connected client.
// Mirrored from transport to avoid a circular import.
type Notifier interface {
	Notify(method string, params any)
}

// SessionInfo is the serializable metadata for a PTY session.
type SessionInfo struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspaceId"`
	Name        string    `json:"name"`
	Shell       string    `json:"shell"`
	WorkDir     string    `json:"workDir"`
	Cols        int       `json:"cols"`
	Rows        int       `json:"rows"`
	CreatedAt   time.Time `json:"createdAt"`
	IsRemote    bool      `json:"isRemote"`
}

// Session represents an active PTY or remote shell session.
type Session struct {
	ID          string
	WorkspaceID string
	Name        string
	Shell       string
	WorkDir     string
	Cols        int
	Rows        int

	// Local PTY (process backend)
	Cmd  *exec.Cmd
	File *os.File

	// Remote shell (guest agent)
	RemoteConn net.Conn
	Enc        *json.Encoder
	Dec        *json.Decoder
	Remote     bool

	// Notifier is the live WebSocket connection that receives pty.data pushes.
	// It is mutable so that pty.reattach can redirect an existing session to a
	// freshly-reconnected client without restarting the stream goroutine.
	notifier   Notifier
	notifierMu sync.Mutex

	// scrollback holds the last scrollbackBytes of raw terminal output so that
	// pty.reattach can replay it to a freshly-reconnected client.
	scrollback   strings.Builder
	scrollbackMu sync.Mutex

	Mu      sync.Mutex
	Closing atomic.Bool
	Done    chan struct{}

	CreatedAt time.Time
}

// SetNotifier replaces the notifier that receives pty.data pushes for this session.
// Called by pty.reattach when a client reconnects to an existing session.
func (s *Session) SetNotifier(n Notifier) {
	s.notifierMu.Lock()
	s.notifier = n
	s.notifierMu.Unlock()
}

// GetNotifier returns the current notifier (never nil).
func (s *Session) GetNotifier() Notifier {
	s.notifierMu.Lock()
	n := s.notifier
	s.notifierMu.Unlock()
	if n == nil {
		return noopNotifier{}
	}
	return n
}

// AppendScrollback adds data to the session's scrollback ring, trimming from
// the front when it grows beyond scrollbackBytes.
func (s *Session) AppendScrollback(data string) {
	s.scrollbackMu.Lock()
	defer s.scrollbackMu.Unlock()
	s.scrollback.WriteString(data)
	if s.scrollback.Len() > scrollbackBytes {
		excess := s.scrollback.Len() - scrollbackBytes
		full := s.scrollback.String()
		s.scrollback.Reset()
		s.scrollback.WriteString(full[excess:])
	}
}

// Scrollback returns a snapshot of the buffered terminal output.
func (s *Session) Scrollback() string {
	s.scrollbackMu.Lock()
	defer s.scrollbackMu.Unlock()
	return s.scrollback.String()
}

type noopNotifier struct{}

func (noopNotifier) Notify(string, any) {}

// Info returns a serializable snapshot of session metadata.
func (s *Session) Info() SessionInfo {
	return SessionInfo{
		ID:          s.ID,
		WorkspaceID: s.WorkspaceID,
		Name:        s.Name,
		Shell:       s.Shell,
		WorkDir:     s.WorkDir,
		Cols:        s.Cols,
		Rows:        s.Rows,
		CreatedAt:   s.CreatedAt,
		IsRemote:    s.Remote,
	}
}
