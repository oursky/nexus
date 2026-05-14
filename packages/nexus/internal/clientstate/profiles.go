package clientstate

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Profile stores non-secret connection details for a remote daemon.
// The token is intentionally omitted — it lives in the OS keychain or a
// separate file store via the tokenstore package.
type Profile struct {
	Name            string
	Host            string
	Port            int
	SSHPort         int
	SSHIdentityFile string
	LocalPort       int
	IsDefault       bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// UpsertProfile inserts or updates a profile by name.
// created_at is preserved on update; updated_at is always refreshed.
func (d *DB) UpsertProfile(p Profile) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := d.db.Exec(`
		INSERT INTO profiles (name, host, port, ssh_port, ssh_identity, local_port, is_default, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			host=excluded.host,
			port=excluded.port,
			ssh_port=excluded.ssh_port,
			ssh_identity=excluded.ssh_identity,
			local_port=excluded.local_port,
			is_default=excluded.is_default,
			updated_at=excluded.updated_at
	`, p.Name, p.Host, p.Port, p.SSHPort, p.SSHIdentityFile, p.LocalPort,
		boolToInt(p.IsDefault), now, now)
	if err != nil {
		return fmt.Errorf("clientstate: upsert profile %q: %w", p.Name, err)
	}
	return nil
}

// GetProfile retrieves a profile by name. Returns sql.ErrNoRows if not found.
func (d *DB) GetProfile(name string) (*Profile, error) {
	row := d.db.QueryRow(`
		SELECT name, host, port, ssh_port, ssh_identity, local_port, is_default, created_at, updated_at
		FROM profiles WHERE name=?
	`, name)
	return scanProfileRow(row)
}

// GetDefaultProfile returns the first profile with is_default=1, or the first
// row if none is marked as default, or nil if the table is empty.
func (d *DB) GetDefaultProfile() (*Profile, error) {
	// Try the explicitly marked default first.
	row := d.db.QueryRow(`
		SELECT name, host, port, ssh_port, ssh_identity, local_port, is_default, created_at, updated_at
		FROM profiles WHERE is_default=1 ORDER BY rowid LIMIT 1
	`)
	p, err := scanProfileRow(row)
	if err == nil {
		return p, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	// Fall back to the first row.
	row = d.db.QueryRow(`
		SELECT name, host, port, ssh_port, ssh_identity, local_port, is_default, created_at, updated_at
		FROM profiles ORDER BY rowid LIMIT 1
	`)
	p, err = scanProfileRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil // no profiles at all
	}
	return p, err
}

// ListProfiles returns all profiles ordered by insertion order.
func (d *DB) ListProfiles() ([]Profile, error) {
	rows, err := d.db.Query(`
		SELECT name, host, port, ssh_port, ssh_identity, local_port, is_default, created_at, updated_at
		FROM profiles ORDER BY rowid
	`)
	if err != nil {
		return nil, fmt.Errorf("clientstate: list profiles: %w", err)
	}
	defer rows.Close()
	var profiles []Profile
	for rows.Next() {
		var p Profile
		var createdAt, updatedAt string
		var isDefault int
		if err := rows.Scan(&p.Name, &p.Host, &p.Port, &p.SSHPort,
			&p.SSHIdentityFile, &p.LocalPort, &isDefault, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("clientstate: scan profile: %w", err)
		}
		p.IsDefault = isDefault != 0
		p.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		p.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		profiles = append(profiles, p)
	}
	return profiles, rows.Err()
}

// DeleteProfile removes a profile by name. No-op if the profile does not exist.
func (d *DB) DeleteProfile(name string) error {
	_, err := d.db.Exec(`DELETE FROM profiles WHERE name=?`, name)
	if err != nil {
		return fmt.Errorf("clientstate: delete profile %q: %w", name, err)
	}
	return nil
}

func scanProfileRow(row *sql.Row) (*Profile, error) {
	var p Profile
	var createdAt, updatedAt string
	var isDefault int
	err := row.Scan(&p.Name, &p.Host, &p.Port, &p.SSHPort,
		&p.SSHIdentityFile, &p.LocalPort, &isDefault, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	p.IsDefault = isDefault != 0
	p.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	p.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &p, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
