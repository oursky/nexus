package clientstate

import (
	"fmt"
	"time"
)

// CachedWorkspace is a snapshot of a workspace entry for fast TUI startup
// without requiring a live daemon connection.
type CachedWorkspace struct {
	WorkspaceID string
	ProfileName string
	Name        string
	State       string
	Backend     string
	RepoPath    string
	CachedAt    time.Time
}

// CacheWorkspaces atomically replaces all cached workspace rows for the given
// profile with the supplied list.
func (d *DB) CacheWorkspaces(profileName string, workspaces []CachedWorkspace) error {
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("clientstate: cache workspaces begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec(`DELETE FROM workspace_cache WHERE profile_name=?`, profileName); err != nil {
		return fmt.Errorf("clientstate: cache workspaces delete: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, w := range workspaces {
		cachedAt := now
		if !w.CachedAt.IsZero() {
			cachedAt = w.CachedAt.UTC().Format(time.RFC3339)
		}
		if _, err := tx.Exec(`
			INSERT INTO workspace_cache (workspace_id, profile_name, name, state, backend, repo_path, cached_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, w.WorkspaceID, profileName, w.Name, w.State, w.Backend, w.RepoPath, cachedAt); err != nil {
			return fmt.Errorf("clientstate: cache workspace %q: %w", w.WorkspaceID, err)
		}
	}
	return tx.Commit()
}

// GetCachedWorkspaces returns all cached workspace rows for a profile.
func (d *DB) GetCachedWorkspaces(profileName string) ([]CachedWorkspace, error) {
	rows, err := d.db.Query(`
		SELECT workspace_id, profile_name, name, state, backend, repo_path, cached_at
		FROM workspace_cache WHERE profile_name=? ORDER BY rowid
	`, profileName)
	if err != nil {
		return nil, fmt.Errorf("clientstate: get cached workspaces: %w", err)
	}
	defer rows.Close()
	var workspaces []CachedWorkspace
	for rows.Next() {
		var w CachedWorkspace
		var cachedAt string
		if err := rows.Scan(&w.WorkspaceID, &w.ProfileName, &w.Name,
			&w.State, &w.Backend, &w.RepoPath, &cachedAt); err != nil {
			return nil, fmt.Errorf("clientstate: scan workspace cache: %w", err)
		}
		w.CachedAt, _ = time.Parse(time.RFC3339, cachedAt)
		workspaces = append(workspaces, w)
	}
	return workspaces, rows.Err()
}

// InvalidateCache removes all cached workspace rows for a profile.
func (d *DB) InvalidateCache(profileName string) error {
	_, err := d.db.Exec(`DELETE FROM workspace_cache WHERE profile_name=?`, profileName)
	if err != nil {
		return fmt.Errorf("clientstate: invalidate cache %q: %w", profileName, err)
	}
	return nil
}
