package workspace

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	domainworkspace "github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
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
			// Accept an optional positional path: `workspace create .` or
			// `workspace create /path/to/repo` as a shorthand for --repo.
			if len(args) == 1 && repo == "" {
				repo = args[0]
			}

			resolvedRepo := normalizeRepoForCreate(repo)

			// Infer name from the repo directory when not explicitly provided.
			if resolvedRepo != "" && name == "" && !looksLikeRemoteRepo(resolvedRepo) {
				name = filepath.Base(filepath.Clean(resolvedRepo))
			}
			if resolvedRepo == "" || name == "" {
				return fmt.Errorf("--repo and --name are required")
			}

			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus workspace create: %w", err)
			}
			defer conn.Close()

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
			return nil
		},
	}

	cmd.Flags().StringVar(&repo, "repo", "", "repository path or URL on the engine host (required)")
	cmd.Flags().StringVar(&ref, "ref", "", "branch / ref (default: repo default branch)")
	cmd.Flags().StringVar(&name, "name", "", "workspace name (required)")
	cmd.Flags().StringVar(&profile, "profile", "default", "agent profile")
	cmd.Flags().StringVar(&backend, "backend", "", "runtime backend override (libkrun only; omit for default)")
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

	repo = expandHomeDirPrefix(repo)

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

// expandHomeDirPrefix turns ~/path into an absolute path using the current user's
// home directory. libkrun virtiofs host paths must exist on disk — a literal "~"
// breaks VM startup (stat ~/… no such file).
func expandHomeDirPrefix(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return path
	}
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func looksLikeRemoteRepo(repo string) bool {
	if strings.HasPrefix(repo, "git@") || strings.HasPrefix(repo, "ssh://") {
		return true
	}
	u, err := url.Parse(repo)
	return err == nil && u.Scheme != "" && u.Host != ""
}
