package profile

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/oursky/nexus/packages/nexus/internal/auth/tokenstore"
	"github.com/oursky/nexus/packages/nexus/internal/clientstate"
)

// Profile stores connection details for a remote daemon.
// Token is intentionally absent from the database — it is stored separately
// in the OS keychain (macOS Keychain / Linux SecretService / headless file
// store via the tokenstore package).
type Profile struct {
	Name            string `json:"name"`
	Host            string `json:"host"`
	Port            int    `json:"port"`
	Token           string `json:"-"` // never persisted; loaded from keychain
	SSHPort         int    `json:"sshPort,omitempty"`
	SSHIdentityFile string `json:"sshIdentityFile,omitempty"`
	LocalPort       int    `json:"localPort,omitempty"`
}

// SaveDefault writes the profile to the client state database and stores the
// token separately in the OS keychain.
func SaveDefault(p *Profile) error {
	db, err := clientstate.Open()
	if err != nil {
		return fmt.Errorf("save profile: open client state: %w", err)
	}
	defer db.Close()

	if err := db.UpsertProfile(clientstate.Profile{
		Name:            p.Name,
		Host:            p.Host,
		Port:            p.Port,
		SSHPort:         p.SSHPort,
		SSHIdentityFile: p.SSHIdentityFile,
		LocalPort:       p.LocalPort,
		IsDefault:       true,
	}); err != nil {
		return fmt.Errorf("save profile: %w", err)
	}
	if p.Token != "" {
		if err := tokenstore.SaveProfileToken(p.Token); err != nil {
			return fmt.Errorf("save profile token to keychain: %w", err)
		}
	}
	return nil
}

// LoadDefault reads the default profile from the client state database and
// populates Token from the OS keychain.
//
// If NEXUS_DAEMON_SSH_HOST is set, a synthetic profile is constructed from
// environment variables (NEXUS_DAEMON_SSH_HOST, NEXUS_DAEMON_SSH_PORT,
// NEXUS_DAEMON_SSH_IDENTITY, NEXUS_DAEMON_TOKEN) instead of reading from the
// database. This allows the Mac app to inject connection details when running
// the CLI binary from a sandboxed environment.
func LoadDefault() (*Profile, error) {
	if p := localDaemonProfileFromEnv(); p != nil {
		return p, nil
	}

	if host := strings.TrimSpace(os.Getenv("NEXUS_DAEMON_SSH_HOST")); host != "" {
		p := &Profile{
			Name:  "env",
			Host:  host,
			Port:  7777,
			Token: strings.TrimSpace(os.Getenv("NEXUS_DAEMON_TOKEN")),
		}
		if portStr := strings.TrimSpace(os.Getenv("NEXUS_DAEMON_SSH_PORT")); portStr != "" {
			if n, err := strconv.Atoi(portStr); err == nil {
				p.SSHPort = n
			}
		}
		if id := strings.TrimSpace(os.Getenv("NEXUS_DAEMON_SSH_IDENTITY")); id != "" {
			p.SSHIdentityFile = id
		}
		return p, nil
	}

	db, err := clientstate.Open()
	if err != nil {
		return nil, fmt.Errorf("load profile: open client state: %w", err)
	}
	defer db.Close()

	cp, err := db.GetDefaultProfile()
	if err != nil {
		return nil, fmt.Errorf("load profile: %w", err)
	}
	if cp == nil {
		return nil, fmt.Errorf("no daemon profile configured — run: nexus daemon connect <host>")
	}

	p := &Profile{
		Name:            cp.Name,
		Host:            cp.Host,
		Port:            cp.Port,
		SSHPort:         cp.SSHPort,
		SSHIdentityFile: cp.SSHIdentityFile,
		LocalPort:       cp.LocalPort,
	}
	tok, found, err := tokenstore.LoadProfileToken()
	if err != nil {
		return nil, fmt.Errorf("load profile token from keychain: %w", err)
	}
	if found {
		p.Token = tok
	}
	return p, nil
}

// localDaemonProfileFromEnv returns a localhost WebSocket profile when both
// NEXUS_DAEMON_TOKEN and NEXUS_DAEMON_PORT are set. This avoids reading the
// default profile / OS keychain (which often fails over SSH) while targeting a
// local daemon started with the same token.
func localDaemonProfileFromEnv() *Profile {
	tok := strings.TrimSpace(os.Getenv("NEXUS_DAEMON_TOKEN"))
	portStr := strings.TrimSpace(os.Getenv("NEXUS_DAEMON_PORT"))
	if tok == "" || portStr == "" {
		return nil
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 {
		return nil
	}
	return &Profile{Name: "env-local", Host: "127.0.0.1", Port: port, Token: tok}
}

// DeleteDefault removes the default profile from the database and its keychain
// token. Idempotent — safe to call when no profile exists.
func DeleteDefault() error {
	db, err := clientstate.Open()
	if err != nil {
		return fmt.Errorf("delete profile: open client state: %w", err)
	}
	defer db.Close()

	cp, err := db.GetDefaultProfile()
	if err != nil {
		return fmt.Errorf("delete profile: %w", err)
	}
	if cp != nil {
		if err := db.DeleteProfile(cp.Name); err != nil {
			return fmt.Errorf("delete profile: %w", err)
		}
	}
	_ = tokenstore.DeleteProfileToken()
	return nil
}
