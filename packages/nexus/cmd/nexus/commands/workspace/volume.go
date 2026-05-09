package workspace

import (
	"fmt"
	"strings"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/spf13/cobra"
)

// VolumeDTO mirrors the RPC volume DTO for CLI display.
type volumeDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	State       string `json:"state"`
	Size        int64  `json:"size"`
	Capacity    int64  `json:"capacity"`
	WorkspaceID string `json:"workspaceId,omitempty"`
	MountPath   string `json:"mountPath,omitempty"`
	CreatedAt   string `json:"createdAt"`
}

func volumeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "volume",
		Short:   "Manage volumes",
		Aliases: []string{"vol"},
	}
	cmd.AddCommand(
		volumeMountCommand(),
		volumeUnmountCommand(),
		volumeCreateCommand(),
		volumeDeleteCommand(),
		volumeListCommand(),
		volumeInfoCommand(),
		volumeRenameCommand(),
		volumeAttachCommand(),
		volumeDetachCommand(),
		volumeSnapshotCommand(),
		volumeSyncCommand(),
	)
	return cmd
}

func volumeCreateCommand() *cobra.Command {
	var volType string
	var capacity int64
	var workspaceID string

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new volume",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus volume create: %w", err)
			}
			defer conn.Close()

			name := args[0]
			var result struct {
				Volume *volumeDTO `json:"volume"`
			}
			if err := rpc.Do(conn, "volume.create", map[string]any{
				"name":        name,
				"type":        volType,
				"capacity":    capacity,
				"workspaceId": workspaceID,
			}, &result); err != nil {
				return fmt.Errorf("nexus volume create: %w", err)
			}

			if result.Volume == nil {
				return fmt.Errorf("nexus volume create: no volume in response")
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created volume %s (%s)\n", result.Volume.Name, result.Volume.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&volType, "type", "persistent", "Volume type: persistent, ephemeral, workspace")
	cmd.Flags().Int64Var(&capacity, "capacity", 0, "Capacity in bytes (0 for unlimited)")
	cmd.Flags().StringVar(&workspaceID, "workspace", "", "Workspace ID to bind to (for workspace type)")
	return cmd
}

func volumeDeleteCommand() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "delete <id|name>",
		Short: "Delete a volume",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus volume delete: %w", err)
			}
			defer conn.Close()

			volID := args[0]
			if err := rpc.Do(conn, "volume.delete", map[string]any{
				"volumeId": volID,
				"force":    force,
			}, nil); err != nil {
				return fmt.Errorf("nexus volume delete: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Deleted volume %s\n", volID)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Force detach before deleting")
	return cmd
}

func volumeListCommand() *cobra.Command {
	var workspaceID string
	var volType string
	var state string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List volumes",
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus volume list: %w", err)
			}
			defer conn.Close()

			var result struct {
				Volumes []*volumeDTO `json:"volumes"`
			}
			req := map[string]any{}
			if workspaceID != "" {
				req["workspaceId"] = workspaceID
			}
			if volType != "" {
				req["type"] = volType
			}
			if state != "" {
				req["state"] = state
			}
			if err := rpc.Do(conn, "volume.list", req, &result); err != nil {
				return fmt.Errorf("nexus volume list: %w", err)
			}

			if len(result.Volumes) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No volumes found")
				return nil
			}

			// Print table header
			fmt.Fprintf(cmd.OutOrStdout(), "%-36s %-20s %-12s %-10s %-10s %s\n",
				"ID", "NAME", "TYPE", "STATE", "SIZE", "WORKSPACE")
			fmt.Fprintln(cmd.OutOrStdout(), strings.Repeat("-", 100))

			for _, v := range result.Volumes {
				wsID := v.WorkspaceID
				if wsID == "" {
					wsID = "-"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%-36s %-20s %-12s %-10s %-10d %s\n",
					v.ID, v.Name, v.Type, v.State, v.Size, wsID)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&workspaceID, "workspace", "", "Filter by workspace ID")
	cmd.Flags().StringVar(&volType, "type", "", "Filter by type")
	cmd.Flags().StringVar(&state, "state", "", "Filter by state")
	return cmd
}

func volumeInfoCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "info <id|name>",
		Short: "Show volume details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus volume info: %w", err)
			}
			defer conn.Close()

			volID := args[0]
			var result struct {
				Volume *volumeDTO `json:"volume"`
			}
			if err := rpc.Do(conn, "volume.info", map[string]any{
				"volumeId": volID,
			}, &result); err != nil {
				return fmt.Errorf("nexus volume info: %w", err)
			}

			if result.Volume == nil {
				return fmt.Errorf("nexus volume info: no volume in response")
			}
			v := result.Volume
			fmt.Fprintf(cmd.OutOrStdout(), "ID:         %s\n", v.ID)
			fmt.Fprintf(cmd.OutOrStdout(), "Name:       %s\n", v.Name)
			fmt.Fprintf(cmd.OutOrStdout(), "Type:       %s\n", v.Type)
			fmt.Fprintf(cmd.OutOrStdout(), "State:      %s\n", v.State)
			fmt.Fprintf(cmd.OutOrStdout(), "Size:       %d bytes\n", v.Size)
			if v.Capacity > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "Capacity:   %d bytes\n", v.Capacity)
			}
			if v.WorkspaceID != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Workspace:  %s\n", v.WorkspaceID)
			}
			if v.MountPath != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Mount Path: %s\n", v.MountPath)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created:    %s\n", v.CreatedAt)
			return nil
		},
	}
}

func volumeRenameCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <id|name> <new-name>",
		Short: "Rename a volume",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus volume rename: %w", err)
			}
			defer conn.Close()

			volID := args[0]
			newName := args[1]
			if err := rpc.Do(conn, "volume.rename", map[string]any{
				"volumeId": volID,
				"newName":  newName,
			}, nil); err != nil {
				return fmt.Errorf("nexus volume rename: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Renamed volume to %s\n", newName)
			return nil
		},
	}
}

func volumeAttachCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "attach <volume-id> <workspace-id> <mount-path>",
		Short: "Attach a volume to a workspace",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus volume attach: %w", err)
			}
			defer conn.Close()

			volID := args[0]
			wsID := args[1]
			mountPath := args[2]
			if err := rpc.Do(conn, "volume.attach", map[string]any{
				"volumeId":    volID,
				"workspaceId": wsID,
				"mountPath":   mountPath,
			}, nil); err != nil {
				return fmt.Errorf("nexus volume attach: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Attached volume %s to workspace %s at %s\n",
				volID, wsID, mountPath)
			return nil
		},
	}
}

func volumeDetachCommand() *cobra.Command {
	var all bool

	cmd := &cobra.Command{
		Use:   "detach <volume-id> [workspace-id]",
		Short: "Detach a volume from a workspace",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus volume detach: %w", err)
			}
			defer conn.Close()

			volID := args[0]
			if all {
				if err := rpc.Do(conn, "volume.detachAll", map[string]any{
					"volumeId": volID,
				}, nil); err != nil {
					return fmt.Errorf("nexus volume detach: %w", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Detached volume %s from all workspaces\n", volID)
			} else {
				if len(args) < 2 {
					return fmt.Errorf("workspace-id required (use --all to detach from all)")
				}
				wsID := args[1]
				if err := rpc.Do(conn, "volume.detach", map[string]any{
					"volumeId":    volID,
					"workspaceId": wsID,
				}, nil); err != nil {
					return fmt.Errorf("nexus volume detach: %w", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Detached volume %s from workspace %s\n", volID, wsID)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Detach from all workspaces")
	return cmd
}

func volumeSnapshotCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Manage volume snapshots",
	}
	cmd.AddCommand(
		volumeSnapshotCreateCommand(),
		volumeSnapshotListCommand(),
		volumeSnapshotRestoreCommand(),
		volumeSnapshotDeleteCommand(),
	)
	return cmd
}

func volumeSnapshotCreateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "create <volume-id> <name>",
		Short: "Create a snapshot of a volume",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus volume snapshot create: %w", err)
			}
			defer conn.Close()

			volID := args[0]
			name := args[1]
			var result struct {
				Snapshot *snapshotDTO `json:"snapshot"`
			}
			if err := rpc.Do(conn, "volume.snapshot.create", map[string]any{
				"volumeId": volID,
				"name":     name,
			}, &result); err != nil {
				return fmt.Errorf("nexus volume snapshot create: %w", err)
			}

			if result.Snapshot == nil {
				return fmt.Errorf("nexus volume snapshot create: no snapshot in response")
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created snapshot %s (%s)\n",
				result.Snapshot.Name, result.Snapshot.ID)
			return nil
		},
	}
}

func volumeSnapshotListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list <volume-id>",
		Short: "List snapshots for a volume",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus volume snapshot list: %w", err)
			}
			defer conn.Close()

			volID := args[0]
			var result struct {
				Snapshots []*snapshotDTO `json:"snapshots"`
			}
			if err := rpc.Do(conn, "volume.snapshot.list", map[string]any{
				"volumeId": volID,
			}, &result); err != nil {
				return fmt.Errorf("nexus volume snapshot list: %w", err)
			}

			if len(result.Snapshots) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No snapshots found")
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "%-36s %-20s %s\n", "ID", "NAME", "CREATED")
			fmt.Fprintln(cmd.OutOrStdout(), strings.Repeat("-", 90))
			for _, s := range result.Snapshots {
				fmt.Fprintf(cmd.OutOrStdout(), "%-36s %-20s %s\n",
					s.ID, s.Name, s.CreatedAt)
			}
			return nil
		},
	}
}

func volumeSnapshotRestoreCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "restore <volume-id> <snapshot-id>",
		Short: "Restore a volume from a snapshot",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus volume snapshot restore: %w", err)
			}
			defer conn.Close()

			volID := args[0]
			snapID := args[1]
			if err := rpc.Do(conn, "volume.snapshot.restore", map[string]any{
				"volumeId":   volID,
				"snapshotId": snapID,
			}, nil); err != nil {
				return fmt.Errorf("nexus volume snapshot restore: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Restored volume %s from snapshot %s\n", volID, snapID)
			return nil
		},
	}
}

func volumeSnapshotDeleteCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <volume-id> <snapshot-id>",
		Short: "Delete a snapshot",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus volume snapshot delete: %w", err)
			}
			defer conn.Close()

			volID := args[0]
			snapID := args[1]
			if err := rpc.Do(conn, "volume.snapshot.delete", map[string]any{
				"volumeId":   volID,
				"snapshotId": snapID,
			}, nil); err != nil {
				return fmt.Errorf("nexus volume snapshot delete: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Deleted snapshot %s from volume %s\n", snapID, volID)
			return nil
		},
	}
}

// snapshotDTO mirrors the RPC snapshot DTO for CLI display.
type snapshotDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	VolumeID  string `json:"volumeId"`
	CreatedAt string `json:"createdAt"`
	Size      int64  `json:"size"`
}

func volumeSyncCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Manage volume sync",
	}
	cmd.AddCommand(
		volumeSyncEnableCommand(),
		volumeSyncDisableCommand(),
		volumeSyncInfoCommand(),
	)
	return cmd
}

func volumeSyncEnableCommand() *cobra.Command {
	var direction string

	cmd := &cobra.Command{
		Use:   "enable <volume-id> <local-path>",
		Short: "Enable sync for a volume with a local directory",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus volume sync enable: %w", err)
			}
			defer conn.Close()

			volID := args[0]
			localPath := args[1]
			var result struct {
				Sync *syncConfigDTO `json:"sync"`
			}
			if err := rpc.Do(conn, "volume.sync.enable", map[string]any{
				"volumeId":  volID,
				"localPath": localPath,
				"direction": direction,
			}, &result); err != nil {
				return fmt.Errorf("nexus volume sync enable: %w", err)
			}

			if result.Sync == nil {
				return fmt.Errorf("nexus volume sync enable: no sync config in response")
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Sync enabled for volume %s\n", volID)
			fmt.Fprintf(cmd.OutOrStdout(), "Local path: %s\n", result.Sync.LocalPath)
			fmt.Fprintf(cmd.OutOrStdout(), "Direction:  %s\n", result.Sync.Direction)
			return nil
		},
	}
	cmd.Flags().StringVar(&direction, "direction", "bidirectional", "Sync direction: up, down, bidirectional")
	return cmd
}

func volumeSyncDisableCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "disable <volume-id>",
		Short: "Disable sync for a volume",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus volume sync disable: %w", err)
			}
			defer conn.Close()

			volID := args[0]
			if err := rpc.Do(conn, "volume.sync.disable", map[string]any{
				"volumeId": volID,
			}, nil); err != nil {
				return fmt.Errorf("nexus volume sync disable: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Sync disabled for volume %s\n", volID)
			return nil
		},
	}
}

func volumeSyncInfoCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "info <volume-id>",
		Short: "Show sync info for a volume",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus volume sync info: %w", err)
			}
			defer conn.Close()

			volID := args[0]
			var result struct {
				Sync *syncConfigDTO `json:"sync"`
			}
			if err := rpc.Do(conn, "volume.sync.info", map[string]any{
				"volumeId": volID,
			}, &result); err != nil {
				return fmt.Errorf("nexus volume sync info: %w", err)
			}

			if result.Sync == nil {
				fmt.Fprintln(cmd.OutOrStdout(), "Sync not configured for this volume")
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Enabled:    %v\n", result.Sync.Enabled)
			fmt.Fprintf(cmd.OutOrStdout(), "Local Path: %s\n", result.Sync.LocalPath)
			fmt.Fprintf(cmd.OutOrStdout(), "Direction:  %s\n", result.Sync.Direction)
			fmt.Fprintf(cmd.OutOrStdout(), "Status:     %s\n", result.Sync.Status)
			if result.Sync.SessionID != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Session ID: %s\n", result.Sync.SessionID)
			}
			if result.Sync.LastSyncAt != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Last Sync:  %s\n", result.Sync.LastSyncAt)
			}
			return nil
		},
	}
}

// syncConfigDTO mirrors the RPC sync config DTO for CLI display.
type syncConfigDTO struct {
	Enabled    bool   `json:"enabled"`
	LocalPath  string `json:"localPath,omitempty"`
	SessionID  string `json:"sessionId,omitempty"`
	Direction  string `json:"direction,omitempty"`
	Status     string `json:"status,omitempty"`
	LastSyncAt string `json:"lastSyncAt,omitempty"`
}

func volumeMountCommand() *cobra.Command {
	var direction string
	var name string

	cmd := &cobra.Command{
		Use:   "mount <local-path> <workspace-id> [mount-path]",
		Short: "Mount a local directory to a workspace (creates volume + sync)",
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus volume mount: %w", err)
			}
			defer conn.Close()

			localPath := args[0]
			workspaceID := args[1]
			mountPath := ""
			if len(args) > 2 {
				mountPath = args[2]
			}

			var result struct {
				Volume *volumeDTO `json:"volume"`
			}
			if err := rpc.Do(conn, "volume.mount", map[string]any{
				"localPath":   localPath,
				"workspaceId": workspaceID,
				"mountPath":   mountPath,
				"direction":   direction,
				"name":        name,
			}, &result); err != nil {
				return fmt.Errorf("nexus volume mount: %w", err)
			}

			if result.Volume == nil {
				return fmt.Errorf("nexus volume mount: no volume in response")
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Mounted %s to workspace %s\n", localPath, workspaceID)
			fmt.Fprintf(cmd.OutOrStdout(), "Volume: %s (%s)\n", result.Volume.Name, result.Volume.ID)
			fmt.Fprintf(cmd.OutOrStdout(), "Mount path: %s\n", result.Volume.MountPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&direction, "direction", "bidirectional", "Sync direction: up, down, bidirectional")
	cmd.Flags().StringVar(&name, "name", "", "Custom volume name (default: basename of local-path)")
	return cmd
}

func volumeUnmountCommand() *cobra.Command {
	var delete bool

	cmd := &cobra.Command{
		Use:   "unmount <volume-id> <workspace-id>",
		Short: "Unmount a volume from a workspace",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus volume unmount: %w", err)
			}
			defer conn.Close()

			volID := args[0]
			workspaceID := args[1]
			if err := rpc.Do(conn, "volume.unmount", map[string]any{
				"volumeId":    volID,
				"workspaceId": workspaceID,
				"delete":      delete,
			}, nil); err != nil {
				return fmt.Errorf("nexus volume unmount: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Unmounted volume %s from workspace %s\n", volID, workspaceID)
			if delete {
				fmt.Fprintf(cmd.OutOrStdout(), "Volume deleted\n")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&delete, "delete", false, "Delete volume after unmount")
	return cmd
}
