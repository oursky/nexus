package workspace

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/inizio/nexus/packages/nexus/cmd/nexus/commands/rpc"
	domainworkspace "github.com/inizio/nexus/packages/nexus/internal/domain/workspace"
	"github.com/inizio/nexus/packages/nexus/internal/infra/cli/localws"
	"github.com/inizio/nexus/packages/nexus/internal/infra/cli/mirror"
	cliprofile "github.com/inizio/nexus/packages/nexus/internal/infra/cli/profile"
	"github.com/spf13/cobra"
)

type preflightErrorEnvelope struct {
	Status         string `json:"status"`
	SetupAttempted bool   `json:"setupAttempted"`
	SetupOutcome   string `json:"setupOutcome"`
	Checks         []struct {
		Name        string `json:"name"`
		OK          bool   `json:"ok"`
		Message     string `json:"message"`
		Remediation string `json:"remediation"`
		Installable bool   `json:"installable,omitempty"`
	} `json:"checks"`
}

func createCommand() *cobra.Command {
	var (
		repo    string
		ref     string
		name    string
		profile string
		backend string
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new workspace",
		RunE: func(cmd *cobra.Command, args []string) error {
			if repo == "" || name == "" {
				return fmt.Errorf("--repo and --name are required")
			}

			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus workspace create: %w", err)
			}
			defer conn.Close()

			resolvedRepo := normalizeRepoForCreate(repo)
			// originalRepo holds the Mac-local path before any mirroring so we
			// can record it in the client-side state regardless of what remote
			// path the daemon ends up using.
			originalRepo := resolvedRepo

			// When connected to a remote SSH daemon and --repo is a local path,
			// mirror it to the Linux host via mutagen so the daemon can access it.
			if !looksLikeRemoteRepo(resolvedRepo) {
				if p, err := cliprofile.LoadDefault(); err == nil && p.Host != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "mirroring %s to %s…\n", resolvedRepo, p.Host)
					slug := filepath.Base(resolvedRepo)
					result, mirrorErr := mirror.Ensure(mirror.Spec{
						LocalPath: resolvedRepo,
						ProjectID: slug,
						SSHTarget: p.Host,
						SSHPort:   p.SSHPort,
					})
					if mirrorErr != nil {
						return fmt.Errorf("nexus workspace create: mirror local repo: %w", mirrorErr)
					}
					resolvedRepo = result.RemotePath
				}
			}

			spec := domainworkspace.CreateSpec{
				Repo:          resolvedRepo,
				Ref:           ref,
				WorkspaceName: name,
				AgentProfile:  profile,
				Backend:       strings.TrimSpace(backend),
			}
			var result struct {
				Workspace domainworkspace.Workspace `json:"workspace"`
			}
			if err := rpc.Do(conn, "workspace.create", map[string]any{"spec": spec}, &result); err != nil {
				if renderPreflightCreateError(cmd, err) {
					os.Exit(1)
				}
				return fmt.Errorf("nexus workspace create: %w", err)
			}

			ws := result.Workspace
			fmt.Fprintf(cmd.OutOrStdout(), "created workspace %s  (id: %s)\n", ws.WorkspaceName, ws.ID)

			// The base workspace uses the original Mac directory as-is (the git
			// root). Record it so "fork" can locate it later without querying
			// the daemon (which only stores the mirrored Linux-side path).
			if !looksLikeRemoteRepo(resolvedRepo) {
				rec := localws.WorkspaceRecord{
					WorkspaceID:   ws.ID,
					WorkspaceName: ws.WorkspaceName,
					LocalPath:     originalRepo,
					GitRoot:       originalRepo,
					IsWorktree:    false,
				}
				if err := localws.SaveRecord(rec); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not save local workspace state: %v\n", err)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "local path:  %s\n", originalRepo)
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&repo, "repo", "", "repository URL (required)")
	cmd.Flags().StringVar(&ref, "ref", "", "branch / ref (default: repo default branch)")
	cmd.Flags().StringVar(&name, "name", "", "workspace name (required)")
	cmd.Flags().StringVar(&profile, "profile", "default", "agent profile")
	cmd.Flags().StringVar(&backend, "backend", "", "runtime backend override (firecracker)")
	return cmd
}

func renderPreflightCreateError(cmd *cobra.Command, err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	idx := strings.Index(msg, "runtime preflight failed:")
	if idx < 0 {
		return false
	}
	jsonStart := strings.Index(msg[idx:], "{")
	if jsonStart < 0 {
		return false
	}
	jsonPayload := strings.TrimSpace(msg[idx+jsonStart:])

	var payload preflightErrorEnvelope
	if unmarshalErr := rpc.UnmarshalJSON([]byte(jsonPayload), &payload); unmarshalErr != nil {
		return false
	}

	fmt.Fprintln(cmd.ErrOrStderr(), "nexus workspace create: runtime preflight failed")
	fmt.Fprintf(cmd.ErrOrStderr(), "status: %s\n", payload.Status)
	if payload.SetupAttempted {
		if strings.TrimSpace(payload.SetupOutcome) != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "setup: attempted (%s)\n", payload.SetupOutcome)
		} else {
			fmt.Fprintln(cmd.ErrOrStderr(), "setup: attempted")
		}
	}
	for _, check := range payload.Checks {
		if check.OK {
			continue
		}
		suffix := ""
		if check.Installable {
			suffix = " (installable)"
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "- %s%s", check.Name, suffix)
		if strings.TrimSpace(check.Message) != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), ": %s", check.Message)
		}
		fmt.Fprintln(cmd.ErrOrStderr())
		if strings.TrimSpace(check.Remediation) != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "  remediation: %s\n", check.Remediation)
		}
	}
	return true
}

func normalizeRepoForCreate(repo string) string {
	repo = strings.TrimSpace(repo)
	if repo == "" || looksLikeRemoteRepo(repo) {
		return repo
	}

	if filepath.IsAbs(repo) {
		return filepath.Clean(repo)
	}

	if strings.HasPrefix(repo, "./") || strings.HasPrefix(repo, "../") {
		if abs, err := filepath.Abs(repo); err == nil {
			return abs
		}
		return repo
	}

	if info, err := os.Stat(repo); err == nil && info.IsDir() {
		if abs, absErr := filepath.Abs(repo); absErr == nil {
			return abs
		}
	}

	return repo
}

func looksLikeRemoteRepo(repo string) bool {
	if strings.HasPrefix(repo, "git@") || strings.HasPrefix(repo, "ssh://") {
		return true
	}
	u, err := url.Parse(repo)
	return err == nil && u.Scheme != "" && u.Host != ""
}
