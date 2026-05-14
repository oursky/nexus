package clientstate

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// DB holds the client-side SQLite state database.
type DB struct {
	db *sql.DB
}

// schema is applied with CREATE TABLE IF NOT EXISTS so it is safe to run on
// every open — adding new tables in future versions is a no-op for existing
// databases.
const schema = `
CREATE TABLE IF NOT EXISTS profiles (
    name            TEXT PRIMARY KEY,
    host            TEXT NOT NULL DEFAULT '',
    port            INTEGER NOT NULL DEFAULT 7777,
    ssh_port        INTEGER NOT NULL DEFAULT 0,
    ssh_identity    TEXT NOT NULL DEFAULT '',
    local_port      INTEGER NOT NULL DEFAULT 0,
    is_default      INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS tui_sessions (
    workspace_id    TEXT PRIMARY KEY,
    workspace_name  TEXT NOT NULL DEFAULT '',
    last_attached   TEXT,
    tab_order       INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS tui_prefs (
    key     TEXT PRIMARY KEY,
    value   TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS workspace_cache (
    workspace_id    TEXT PRIMARY KEY,
    profile_name    TEXT NOT NULL DEFAULT '',
    name            TEXT NOT NULL DEFAULT '',
    state           TEXT NOT NULL DEFAULT '',
    backend         TEXT NOT NULL DEFAULT '',
    repo_path       TEXT NOT NULL DEFAULT '',
    cached_at       TEXT NOT NULL DEFAULT (datetime('now'))
);
`

// DefaultDBPath returns the path to the client state database.
// Respects $XDG_STATE_HOME; defaults to ~/.local/state/nexus/client.db.
func DefaultDBPath() string {
	if stateHome := os.Getenv("XDG_STATE_HOME"); stateHome != "" {
		return filepath.Join(expandTilde(stateHome), "nexus", "client.db")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".local", "state", "nexus", "client.db")
	}
	return filepath.Join(home, ".local", "state", "nexus", "client.db")
}

func expandTilde(p string) string {
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// Open opens (or creates) the client state database at the default path.
func Open() (*DB, error) {
	return OpenAt(DefaultDBPath())
}

// OpenAt opens (or creates) the client state database at the given path.
// Intended for tests and isolated environments (e.g. XDG_STATE_HOME overrides).
func OpenAt(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("clientstate: create dir: %w", err)
	}

	dsn := path + "?_journal_mode=WAL"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("clientstate: open: %w", err)
	}

	// modernc.org/sqlite does not reliably honour _busy_timeout in the DSN;
	// set it explicitly via PRAGMA.
	if _, err := sqlDB.Exec("PRAGMA busy_timeout = 30000"); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("clientstate: set busy_timeout: %w", err)
	}

	// Single connection — avoids "database is locked" on concurrent CLI calls.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	d := &DB{db: sqlDB}
	if err := d.initSchema(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return d, nil
}

// Close closes the underlying database connection.
func (d *DB) Close() error {
	if d == nil || d.db == nil {
		return nil
	}
	return d.db.Close()
}

func (d *DB) initSchema() error {
	if _, err := d.db.Exec(schema); err != nil {
		return fmt.Errorf("clientstate: init schema: %w", err)
	}
	return nil
}
