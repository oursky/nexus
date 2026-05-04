import AppKit
import NexusCore
import SwiftUI

// MARK: - Detail root

struct WorkspaceDetailView: View {
    let workspace: Workspace
    @EnvironmentObject var appState: AppState

    var body: some View {
        VStack(spacing: 0) {
            TerminalCard(workspace: workspace)
                .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity)
                .background(Theme.bgApp)
        }
        .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity, alignment: .topLeading)
        .id("workspace-detail-\(workspace.id)")
        .background(Theme.bgApp)
        .accessibilityIdentifier("workspace_detail")
        .accessibilityLabel("Workspace \(workspace.name)")
        .toolbar {
            ToolbarItem(placement: .navigation) {
                WorkspaceBreadcrumb(workspace: workspace)
            }
            ToolbarItem(placement: .primaryAction) {
                WorkspaceStartStopButton(workspace: workspace)
            }
            ToolbarItem(placement: .primaryAction) {
                RemoteEditorOpenMenu(workspace: workspace)
            }
            ToolbarItem(placement: .primaryAction) {
                WorkspaceOverflowMenu(workspace: workspace)
            }
            // Flexible space pushes the inspector toggle to the far-right edge,
            // so it sits directly adjacent to the right sidebar panel.
            ToolbarItem(placement: .primaryAction) {
                Spacer()
            }
            ToolbarItem(placement: .primaryAction) {
                Button {
                    appState.showInspector.toggle()
                } label: {
                    Label(appState.showInspector ? "Hide Inspector" : "Show Inspector", systemImage: "sidebar.trailing")
                        .labelStyle(.iconOnly)
                }
                .help(appState.showInspector ? "Hide Inspector" : "Show Inspector")
            }
        }
    }
}

// MARK: - Open in Cursor / VS Code (Remote SSH)

private struct RemoteEditorOpenMenu: View {
    let workspace: Workspace
    @EnvironmentObject var appState: AppState

    /// SSH pre-flight state — nil = idle, true = checking in progress.
    @State private var isCheckingSSH = false
    /// Error/status result from the last SSH check; shown as a sheet.
    @State private var sshCheckResult: SSHCheckResult? = nil

    private var sshTarget: String? {
        appState.activeDaemonProfile?.sshTarget?.trimmingCharacters(in: .whitespacesAndNewlines).nilIfEmpty
    }

    /// Whether the workspace uses a libkrun VM backend.
    private var isVMBackend: Bool { workspace.usesGuestVMRuntime }

    private var isVMWorkspace: Bool {
        guard let ip = workspace.guestIp else { return false }
        return !ip.isEmpty
    }

    private var canOpen: Bool {
        guard case .connected = appState.connectionState else { return false }
        guard let sshTarget else { return false }
        return workspace.remoteSSHFolderOpen(
            jumpHost: sshTarget,
            identityFile: appState.activeDaemonProfile?.sshIdentity
        ) != nil
    }

    private var help: String {
        guard canOpen else {
            if isVMBackend && !workspace.state.isActive {
                return "Start the VM workspace first, then open it in an editor."
            }
            return "Requires a connected daemon, SSH host, and a running VM (or host repo path for process sandboxes)."
        }
        if isVMWorkspace {
            return "Open /workspace inside the VM. Runs an SSH connectivity check first."
        }
        if let port = appState.activeDaemonProfile?.sshPort, port != 22 {
            return "Open this repo on the SSH host in Cursor or VS Code. Non-default SSH ports need a matching Host in ~/.ssh/config."
        }
        return "Open this repo on the SSH host in Cursor or VS Code (Remote SSH)."
    }

    var body: some View {
        Menu {
            Button {
                Task { await open(.cursor) }
            } label: {
                Label("Open in Cursor", systemImage: "chevron.left.forwardslash.chevron.right")
            }
            Button {
                Task { await open(.vscode) }
            } label: {
                Label("Open in Visual Studio Code", systemImage: "chevron.left.forwardslash.chevron.right")
            }
            if isVMWorkspace {
                Divider()
                Button {
                    Task { await runSSHCheck() }
                } label: {
                    Label(isCheckingSSH ? "Checking SSH…" : "Check SSH Connection", systemImage: "network")
                }
                .disabled(isCheckingSSH)
            }
        } label: {
            if isCheckingSSH {
                ProgressView()
            } else {
                Label("Open in Editor", systemImage: "arrow.up.forward.square")
                    .labelStyle(.iconOnly)
            }
        }
        .disabled(!canOpen || isCheckingSSH)
        .help(help)
        .sheet(item: $sshCheckResult) { result in
            SSHCheckResultSheet(result: result)
        }
    }

    // MARK: - Open with pre-flight

    private func open(_ app: RemoteEditorApp) async {
        guard let sshTarget else { return }
        guard let spec = workspace.remoteSSHFolderOpen(jumpHost: sshTarget, identityFile: appState.activeDaemonProfile?.sshIdentity) else { return }

        guard let _ = spec.vmGuestIP.flatMap({ $0.isEmpty ? nil : $0 }) else {
            // Non-VM workspace: open directly without SSH check.
            guard let url = RemoteEditorURLBuilder.folderURL(app: app, sshTarget: spec.sshHostForURI, absoluteRemotePath: spec.remotePath) else { return }
            NSWorkspace.shared.open(url)
            return
        }

        // For VM workspaces: delegate everything (config write + SSH check + open) to the CLI.
        // This runs as a normal process with full filesystem/network permissions.
        isCheckingSSH = true
        defer { isCheckingSSH = false }

        let result = await runCLIOpenEditor(workspaceId: workspace.id, app: app)
        if let failure = result {
            sshCheckResult = failure
        }
        // On success the CLI itself opens the editor URL via `open cursor://...`.
    }

    // MARK: - Standalone "Check SSH" action

    private func runSSHCheck() async {
        isCheckingSSH = true
        defer { isCheckingSSH = false }

        let result = await runCLIOpenEditor(workspaceId: workspace.id, app: .cursor, checkOnly: true)
        if let failure = result {
            sshCheckResult = failure
        } else {
            sshCheckResult = SSHCheckResult(
                guestIP: workspace.guestIp ?? "",
                isSuccess: true,
                summary: "SSH check passed",
                detail: "Connection from Mac → engine host → VM is working."
            )
        }
    }

    // MARK: - CLI delegate

    /// Calls AppState.openEditorViaCLI and maps the result to SSHCheckResult.
    /// Returns nil on success, or an SSHCheckResult describing the failure.
    private func runCLIOpenEditor(workspaceId: String, app: RemoteEditorApp, checkOnly: Bool = false) async -> SSHCheckResult? {
        if let sshTarget,
           let spec = workspace.remoteSSHFolderOpen(jumpHost: sshTarget, identityFile: appState.activeDaemonProfile?.sshIdentity),
           let guestIP = spec.vmGuestIP,
           !guestIP.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            do {
                try NexusSSHConfigSnippet.installIncludeIfNeeded()
                try NexusSSHConfigSnippet.writeVMJumpHost(
                    hostAlias: spec.sshHostForURI,
                    guestIP: guestIP,
                    proxyJump: spec.proxyJump,
                    identityFile: spec.identityFile
                )
            } catch {
                // Sandboxed app builds may not be allowed to edit the real SSH
                // config directly. The CLI path still performs its own setup, so
                // continue instead of blocking the open.
            }
        }

        let (ok, detail) = await appState.openEditorViaCLI(
            workspaceID: workspaceId,
            app: app.rawValue,
            checkOnly: checkOnly
        )
        guard !ok else { return nil }
        return SSHCheckResult(
            guestIP: workspace.guestIp ?? "",
            isSuccess: false,
            summary: "SSH connection failed",
            detail: detail.isEmpty ? "nexus exited with a non-zero status" : detail
        )
    }
}
// MARK: - SSH check result model

private struct SSHCheckResult: Identifiable {
    let id = UUID()
    let guestIP: String
    let isSuccess: Bool
    let summary: String
    let detail: String
}

// MARK: - SSH check result sheet

private struct SSHCheckResultSheet: View {
    let result: SSHCheckResult
    @Environment(\.dismiss) private var dismiss

    private var iconName: String { result.isSuccess ? "checkmark.circle.fill" : "exclamationmark.triangle.fill" }
    private var iconColor: Color { result.isSuccess ? .green : .orange }
    private var title: String { result.isSuccess ? "SSH Check Passed" : "Cannot Connect to VM" }

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.spaceLg) {
            HStack(spacing: Theme.spaceMd) {
                Image(systemName: iconName)
                    .font(.system(size: 22))
                    .foregroundColor(iconColor)
                Text(title)
                    .font(.headline)
            }

            Text(result.summary)
                .font(.body)
                .foregroundColor(Theme.label)
                .textSelection(.enabled)

            if !result.guestIP.isEmpty {
                HStack {
                    Text("Guest IP:").foregroundColor(Theme.labelSecondary)
                    Text(result.guestIP)
                        .font(.system(.body, design: .monospaced))
                        .foregroundColor(Theme.label)
                        .textSelection(.enabled)
                }
            }

            if !result.detail.isEmpty {
                GroupBox("Details") {
                    ScrollView {
                        Text(result.detail)
                            .font(.system(.caption, design: .monospaced))
                            .foregroundColor(Theme.labelSecondary)
                            .textSelection(.enabled)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                    .frame(maxHeight: 160)
                }
            }

            if !result.isSuccess {
                GroupBox("Next steps") {
                    VStack(alignment: .leading, spacing: Theme.spaceSm) {
                        Text("1. Run 'nexus workspace ssh-vm <name> --diagnose' in a terminal to check sshd status inside the VM.")
                        Text("2. Ensure the workspace is running and the VM image has sshd installed.")
                        Text("3. If sshd is missing, rebuild the rootfs:")
                        Text("   nexus daemon implode && sudo nexus daemon start --setup")
                            .font(.system(.caption, design: .monospaced))
                            .foregroundColor(Theme.labelSecondary)
                    }
                    .font(.caption)
                    .foregroundColor(Theme.labelSecondary)
                }
            }

            HStack {
                Spacer()
                Button("Close") { dismiss() }
                    .keyboardShortcut(.defaultAction)
            }
        }
        .padding(Theme.spaceXl)
        .frame(minWidth: 480)
        .background(Theme.bgElevated)
    }
}
private extension String {
    var nilIfEmpty: String? {
        isEmpty ? nil : self
    }
}

// MARK: - Workspace action menu (toolbar)

private struct WorkspaceOverflowMenu: View {
    let workspace: Workspace
    @EnvironmentObject var appState: AppState

    private var currentOp: WorkspaceOpState? { appState.workspaceOps[workspace.id] }
    private var isBusy: Bool { currentOp != nil }

    var body: some View {
        Menu {
            Button {
                exportWorkspace()
            } label: {
                Label("Export Bundle…", systemImage: "square.and.arrow.up")
            }
            .disabled(isBusy)

            Button(role: .destructive) {
                Task { await appState.remove(workspace) }
            } label: {
                Label("Remove", systemImage: "trash")
            }
            .disabled(isBusy)
        } label: {
            Label("More", systemImage: "ellipsis")
                .labelStyle(.iconOnly)
        }
    }

    private func exportWorkspace() {
        let panel = NSSavePanel()
        panel.message = "Export workspace as bundle"
        panel.prompt = "Export"
        panel.nameFieldStringValue = "\(workspace.name).nxbundle"
        panel.canCreateDirectories = true
        panel.allowedContentTypes = [] // Allow any; bundle path validated by CLI

        guard panel.runModal() == .OK, let url = panel.url else { return }

        Task {
            let (ok, output) = await appState.exportWorkspaceViaCLI(
                workspaceID: workspace.id,
                outPath: url.path
            )
            await MainActor.run {
                let alert = NSAlert()
                alert.messageText = ok ? "Export Complete" : "Export Failed"
                alert.informativeText = output.isEmpty ? (ok ? "Bundle saved." : "Unknown error.") : output
                alert.alertStyle = ok ? .informational : .critical
                alert.addButton(withTitle: "OK")
                alert.runModal()
            }
        }
    }
}

private struct WorkspaceStartStopButton: View {
    let workspace: Workspace
    @EnvironmentObject var appState: AppState

    private var currentOp: WorkspaceOpState? { appState.workspaceOps[workspace.id] }
    private var isBusy: Bool { currentOp != nil }
    private var isRunning: Bool { workspace.state == .running || workspace.state == .restored }
    private var canStart: Bool { workspace.state == .stopped || workspace.state == .created || workspace.state == .paused }
    private var canStop: Bool { isRunning || workspace.state == .paused }

    var body: some View {
        Button {
            Task {
                if canStart { await appState.start(workspace) }
                else if canStop { await appState.stop(workspace) }
            }
        } label: {
            if isBusy {
                ProgressView()
                    .scaleEffect(0.6)
                    .frame(width: 16, height: 16)
            } else {
                Label(canStart ? "Start" : "Stop", systemImage: canStart ? "play.fill" : "stop.fill")
                    .labelStyle(.iconOnly)
            }
        }
        .disabled(isBusy || workspace.state == .starting)
        .help(canStart ? "Start Workspace" : "Stop Workspace")
    }
}

// MARK: - Breadcrumb (toolbar)

private struct WorkspaceBreadcrumb: View {
    let workspace: Workspace
    @EnvironmentObject var appState: AppState
    @State private var liveRef: String = ""
    var body: some View {
        HStack(spacing: 6) {
            Text(workspace.name)
                .font(.system(size: 13, weight: .semibold))
                .foregroundColor(Theme.label)
            let displayRef = liveRef.isEmpty ? workspace.branch : liveRef
            if !displayRef.isEmpty {
                Text("·")
                    .font(.system(size: 12))
                    .foregroundColor(Theme.labelTertiary)
                Text(displayRef)
                    .font(.system(size: 12, design: .monospaced))
                    .foregroundColor(Theme.labelSecondary)
                    .lineLimit(1)
            }
        }
        .padding(.horizontal, 8)
        .task(id: workspace.id) { await refreshRefLoop() }
    }

    private func refreshRefLoop() async {
        let wsID = workspace.id
        while !Task.isCancelled {
            let localRef = await Task.detached(priority: .utility) { () -> String in
                guard let rec = LocalWorkspaceState.record(forWorkspaceID: wsID) else { return "" }
                return (try? GitLogReader.currentRef(repoDirectory: rec.localPath)) ?? ""
            }.value
            if Task.isCancelled { return }
            // For VM workspaces (no local checkout) the daemon pushes workspace.ref
            // notifications over WebSocket; AppState patches repos in-place so we
            // just read the current value directly — no full reload needed.
            let daemonRef = appState.repos.flatMap(\.workspaces).first(where: { $0.id == wsID })?.branch ?? workspace.branch
            await MainActor.run { liveRef = localRef.isEmpty ? daemonRef : localRef }
            try? await Task.sleep(nanoseconds: 5_000_000_000)
        }
    }
}

// MARK: - Terminal inset card

/// The terminal lives inside a dark rounded card within the warm off-white background,
/// matching how Conductor embeds a terminal within a light window.
private struct TerminalCard: View {
    let workspace: Workspace
    @EnvironmentObject var appState: AppState

    var body: some View {
        // GeometryReader: NSViewRepresentable terminals report an intrinsic height (~500pt) unless
        // we pin them to the detail column’s bounds — otherwise the card leaves empty space below.
        GeometryReader { proxy in
            Group {
                if let client = appState.client as? WebSocketDaemonClient {
                    MultiTabTerminalView(workspace: workspace, client: client)
                } else {
                    TerminalView(workspace: workspace)
                }
            }
            .frame(width: proxy.size.width, height: proxy.size.height)
            .id("terminal-card-\(workspace.id)")
            .clipShape(RoundedRectangle(cornerRadius: 8))
            .overlay(
                RoundedRectangle(cornerRadius: 8)
                    .stroke(Color.white.opacity(0.06), lineWidth: 1)
            )
        }
        .padding(12)
        .background(Theme.bgApp)
    }
}

// MARK: - Status pill

struct StatusPill: View {
    let status: WorkspaceStatus

    var body: some View {
        HStack(spacing: 5) {
            Circle().fill(Theme.statusColor(status)).frame(width: 6)
            Text(status.displayName)
                .font(.system(size: 11.5, weight: .medium))
                .foregroundColor(Theme.labelSecondary)
        }
        .padding(.horizontal, 2)
        .padding(.vertical, 2)
        // Unified toolbars already render “glass” capsules; extra rounded fills read as oversized pills.
    }
}

// MARK: - Toolbar icon button

struct ToolbarBtn: View {
    let icon: String
    var active: Bool = false
    let action: () -> Void
    @State private var hover = false
    var body: some View {
        Button(action: action) {
            Image(systemName: icon)
                .font(.system(size: 13))
                .foregroundColor(
                    active
                        ? (hover ? Theme.accent : Theme.labelSecondary)
                        : (hover ? Theme.label : Theme.labelSecondary)
                )
                .frame(width: 28, height: 28)
        }
        .buttonStyle(.plain)
        .onHover { hover = $0 }
    }
}

typealias IconButton = ToolbarBtn
