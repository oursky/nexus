package clientstate

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// TUISession records a workspace that has been attached at least once in the TUI.
type TUISession struct {
	WorkspaceID   string
	WorkspaceName string
	LastAttached  time.Time
	TabOrder      int
}

// GetTUISessions returns all TUI sessions ordered by tab_order.
func (d *DB) GetTUISessions() ([]TUISession, error) {
	rows, err := d.db.Query(`
		SELECT workspace_id, workspace_name, last_attached, tab_order
		FROM tui_sessions ORDER BY tab_order
	`)
	if err != nil {
		return nil, fmt.Errorf("clientstate: get tui sessions: %w", err)
	}
	defer rows.Close()
	var sessions []TUISession
	for rows.Next() {
		var s TUISession
		var lastAttached sql.NullString
		if err := rows.Scan(&s.WorkspaceID, &s.WorkspaceName, &lastAttached, &s.TabOrder); err != nil {
			return nil, fmt.Errorf("clientstate: scan tui session: %w", err)
		}
		if lastAttached.Valid && lastAttached.String != "" {
			s.LastAttached, _ = time.Parse(time.RFC3339, lastAttached.String)
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

// UpsertTUISession inserts or updates a TUI session.
func (d *DB) UpsertTUISession(s TUISession) error {
	var lastAttached interface{}
	if !s.LastAttached.IsZero() {
		lastAttached = s.LastAttached.UTC().Format(time.RFC3339)
	} else {
		lastAttached = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := d.db.Exec(`
		INSERT INTO tui_sessions (workspace_id, workspace_name, last_attached, tab_order)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(workspace_id) DO UPDATE SET
			workspace_name=excluded.workspace_name,
			last_attached=excluded.last_attached,
			tab_order=excluded.tab_order
	`, s.WorkspaceID, s.WorkspaceName, lastAttached, s.TabOrder)
	if err != nil {
		return fmt.Errorf("clientstate: upsert tui session %q: %w", s.WorkspaceID, err)
	}
	return nil
}

// DeleteTUISession removes a TUI session by workspace ID.
func (d *DB) DeleteTUISession(workspaceID string) error {
	_, err := d.db.Exec(`DELETE FROM tui_sessions WHERE workspace_id=?`, workspaceID)
	if err != nil {
		return fmt.Errorf("clientstate: delete tui session %q: %w", workspaceID, err)
	}
	return nil
}

// ReplaceAllTUISessions atomically replaces all TUI session rows.
// Designed for the TUI's save-on-every-update pattern.
func (d *DB) ReplaceAllTUISessions(sessions []TUISession) error {
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("clientstate: replace tui sessions begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec(`DELETE FROM tui_sessions`); err != nil {
		return fmt.Errorf("clientstate: replace tui sessions delete: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, s := range sessions {
		lastAttached := now
		if !s.LastAttached.IsZero() {
			lastAttached = s.LastAttached.UTC().Format(time.RFC3339)
		}
		if _, err := tx.Exec(
			`INSERT INTO tui_sessions (workspace_id, workspace_name, last_attached, tab_order) VALUES (?, ?, ?, ?)`,
			s.WorkspaceID, s.WorkspaceName, lastAttached, s.TabOrder,
		); err != nil {
			return fmt.Errorf("clientstate: replace tui session %q: %w", s.WorkspaceID, err)
		}
	}
	return tx.Commit()
}

// GetTUIPref retrieves a TUI preference value by key. Returns "" if not set.
func (d *DB) GetTUIPref(key string) (string, error) {
	row := d.db.QueryRow(`SELECT value FROM tui_prefs WHERE key=?`, key)
	var value string
	err := row.Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("clientstate: get tui pref %q: %w", key, err)
	}
	return value, nil
}

// SetTUIPref stores a TUI preference value.
func (d *DB) SetTUIPref(key, value string) error {
	_, err := d.db.Exec(`
		INSERT INTO tui_prefs (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value
	`, key, value)
	if err != nil {
		return fmt.Errorf("clientstate: set tui pref %q: %w", key, err)
	}
	return nil
}
