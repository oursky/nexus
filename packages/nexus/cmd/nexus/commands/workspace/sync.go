package workspace

import (
	"fmt"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/spf13/cobra"
)

func syncCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync workspace volumes to a local path",
	}
	cmd.AddCommand(
		syncStartCommand(),
		syncStopCommand(),
		syncStatusCommand(),
		syncListCommand(),
		syncPauseCommand(),
		syncResumeCommand(),
	)
	return cmd
}

func syncStartCommand() *cobra.Command {
	var workspaceID, localPath, direction string

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start syncing a workspace to a local path",
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus workspace sync start: %w", err)
			}
			defer conn.Close()

			if direction == "" {
				direction = "bidirectional"
			}

			var result struct {
				SessionID string `json:"sessionId"`
				Status    string `json:"status"`
			}

			if err := rpc.Do(conn, "workspace.sync-start", map[string]any{
				"workspaceId": workspaceID,
				"localPath":   localPath,
				"direction":   direction,
			}, &result); err != nil {
				return fmt.Errorf("nexus workspace sync start: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Sync session started: %s (%s)\n", result.SessionID, result.Status)
			return nil
		},
	}
	cmd.Flags().StringVar(&workspaceID, "workspace-id", "", "Workspace ID (required)")
	cmd.MarkFlagRequired("workspace-id")
	cmd.Flags().StringVar(&localPath, "local-path", "", "Local path to sync (required)")
	cmd.MarkFlagRequired("local-path")
	cmd.Flags().StringVar(&direction, "direction", "bidirectional", "Sync direction: bidirectional, up, or down")
	return cmd
}

func syncStopCommand() *cobra.Command {
	var sessionID, workspaceID string

	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop syncing a workspace",
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus workspace sync stop: %w", err)
			}
			defer conn.Close()

			if sessionID == "" && workspaceID == "" {
				return fmt.Errorf("either --session-id or --workspace-id is required")
			}

			params := map[string]any{}
			if sessionID != "" {
				params["sessionId"] = sessionID
			}
			if workspaceID != "" {
				params["workspaceId"] = workspaceID
			}

			var result struct {
				Success bool   `json:"success"`
				Message string `json:"message"`
			}

			if err := rpc.Do(conn, "workspace.sync-stop", params, &result); err != nil {
				return fmt.Errorf("nexus workspace sync stop: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Sync session stopped\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&sessionID, "session-id", "", "Stop a specific session by ID")
	cmd.Flags().StringVar(&workspaceID, "workspace-id", "", "Stop sessions for a workspace")
	return cmd
}

func syncStatusCommand() *cobra.Command {
	var sessionID, workspaceID string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show sync session status",
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus workspace sync status: %w", err)
			}
			defer conn.Close()

			if sessionID == "" && workspaceID == "" {
				return fmt.Errorf("either --session-id or --workspace-id is required")
			}

			params := map[string]any{}
			if sessionID != "" {
				params["sessionId"] = sessionID
			}
			if workspaceID != "" {
				params["workspaceId"] = workspaceID
			}

			var result struct {
				SessionID   string `json:"sessionId"`
				WorkspaceID string `json:"workspaceId"`
				LocalPath   string `json:"localPath"`
				Status      string `json:"status"`
				Direction   string `json:"direction"`
				StartedAt   string `json:"startedAt"`
				Stats       struct {
					TotalSyncs        int64 `json:"totalSyncs"`
					BytesSent         int64 `json:"bytesSent"`
					BytesReceived     int64 `json:"bytesReceived"`
					FilesSent         int64 `json:"filesSent"`
					FilesReceived     int64 `json:"filesReceived"`
					ConflictsResolved int64 `json:"conflictsResolved"`
				} `json:"stats"`
			}

			if err := rpc.Do(conn, "workspace.sync-status", params, &result); err != nil {
				return fmt.Errorf("nexus workspace sync status: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Session:       %s\n", result.SessionID)
			fmt.Fprintf(cmd.OutOrStdout(), "Workspace:     %s\n", result.WorkspaceID)
			fmt.Fprintf(cmd.OutOrStdout(), "Local path:    %s\n", result.LocalPath)
			fmt.Fprintf(cmd.OutOrStdout(), "Status:        %s\n", result.Status)
			fmt.Fprintf(cmd.OutOrStdout(), "Direction:     %s\n", result.Direction)
			fmt.Fprintf(cmd.OutOrStdout(), "Started at:    %s\n", result.StartedAt)

			fmt.Fprintf(cmd.OutOrStdout(), "\nStats:\n")
			fmt.Fprintf(cmd.OutOrStdout(), "  Total syncs:        %d\n", result.Stats.TotalSyncs)
			fmt.Fprintf(cmd.OutOrStdout(), "  Bytes sent:         %d\n", result.Stats.BytesSent)
			fmt.Fprintf(cmd.OutOrStdout(), "  Bytes received:     %d\n", result.Stats.BytesReceived)
			fmt.Fprintf(cmd.OutOrStdout(), "  Files sent:         %d\n", result.Stats.FilesSent)
			fmt.Fprintf(cmd.OutOrStdout(), "  Files received:     %d\n", result.Stats.FilesReceived)
			fmt.Fprintf(cmd.OutOrStdout(), "  Conflicts resolved: %d\n", result.Stats.ConflictsResolved)
			return nil
		},
	}
	cmd.Flags().StringVar(&sessionID, "session-id", "", "Show a specific session by ID")
	cmd.Flags().StringVar(&workspaceID, "workspace-id", "", "Show sessions for a workspace")
	return cmd
}

func syncPauseCommand() *cobra.Command {
	var sessionID string

	cmd := &cobra.Command{
		Use:   "pause",
		Short: "Pause a sync session",
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus workspace sync pause: %w", err)
			}
			defer conn.Close()

			if sessionID == "" {
				return fmt.Errorf("--session-id is required")
			}

			var result struct {
				Success bool `json:"success"`
			}

			if err := rpc.Do(conn, "workspace.sync-pause", map[string]any{
				"sessionId": sessionID,
			}, &result); err != nil {
				return fmt.Errorf("nexus workspace sync pause: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Sync session paused\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&sessionID, "session-id", "", "Session ID to pause (required)")
	cmd.MarkFlagRequired("session-id")
	return cmd
}

func syncResumeCommand() *cobra.Command {
	var sessionID string

	cmd := &cobra.Command{
		Use:   "resume",
		Short: "Resume a sync session",
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus workspace sync resume: %w", err)
			}
			defer conn.Close()

			if sessionID == "" {
				return fmt.Errorf("--session-id is required")
			}

			var result struct {
				Success bool `json:"success"`
			}

			if err := rpc.Do(conn, "workspace.sync-resume", map[string]any{
				"sessionId": sessionID,
			}, &result); err != nil {
				return fmt.Errorf("nexus workspace sync resume: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Sync session resumed\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&sessionID, "session-id", "", "Session ID to resume (required)")
	cmd.MarkFlagRequired("session-id")
	return cmd
}

func syncListCommand() *cobra.Command {
	var workspaceID string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List sync sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus workspace sync list: %w", err)
			}
			defer conn.Close()

			params := map[string]any{}
			if workspaceID != "" {
				params["workspaceId"] = workspaceID
			}

			var result struct {
				Sessions []struct {
					SessionID   string `json:"sessionId"`
					WorkspaceID string `json:"workspaceId"`
					LocalPath   string `json:"localPath"`
					Status      string `json:"status"`
					Direction   string `json:"direction"`
					StartedAt   string `json:"startedAt"`
				} `json:"sessions"`
			}

			if err := rpc.Do(conn, "workspace.sync-list", params, &result); err != nil {
				return fmt.Errorf("nexus workspace sync list: %w", err)
			}

			if len(result.Sessions) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No active sync sessions")
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "%-36s  %-36s  %-20s  %-12s  %-14s  %s\n",
				"SESSION-ID", "WORKSPACE-ID", "LOCAL-PATH", "STATUS", "DIRECTION", "STARTED")
			for _, s := range result.Sessions {
				fmt.Fprintf(cmd.OutOrStdout(), "%-36s  %-36s  %-20s  %-12s  %-14s  %s\n",
					s.SessionID, s.WorkspaceID, s.LocalPath, s.Status, s.Direction, s.StartedAt)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&workspaceID, "workspace-id", "", "Filter by workspace ID")
	return cmd
}
