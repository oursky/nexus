import NexusCore
import SwiftUI

struct ContentView: View {
    @EnvironmentObject var appState: AppState

    private var columnVisibility: Binding<NavigationSplitViewVisibility> {
        Binding(
            get: { appState.sidebarVisible ? .all : .detailOnly },
            set: { appState.sidebarVisible = ($0 != .detailOnly) }
        )
    }

    var body: some View {
        Group {
            if appState.needsSetup {
                SetupRequiredView()
            } else if appState.connectionState == .starting && appState.repos.isEmpty {
                StartupView()
            } else {
                mainContent
            }
        }
        .frame(minWidth: 1080, minHeight: 560)
        .sheet(item: $appState.createIntent) { intent in
            NewWorkspaceSheet(intent: intent)
                .environmentObject(appState)
        }
    }

    private var mainContent: some View {
        NavigationSplitView(columnVisibility: columnVisibility) {
            SidebarView()
                .navigationSplitViewColumnWidth(min: 240, ideal: 280, max: 360)
        } detail: {
            Group {
                if let ws = appState.selectedWorkspace {
                    WorkspaceDetailView(workspace: ws)
                        .inspector(isPresented: $appState.showInspector) {
                            InspectorView(workspace: ws)
                                .inspectorColumnWidth(min: 260, ideal: 300, max: 360)
                        }
                } else {
                    EmptyStateView(error: appState.error)
                }
            }
            .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity)
        }
    }
}

// MARK: - Setup required (shown when SSH identity key is missing or no profile configured)

private struct SetupRequiredView: View {
    @EnvironmentObject var appState: AppState
    @State private var showSettings = false

    var body: some View {
        VStack(spacing: 20) {
            Image(systemName: "key.fill")
                .font(.system(size: 36, weight: .ultraLight))
                .foregroundColor(Theme.labelTertiary)

            Text("Setup Required")
                .font(.system(size: 16, weight: .semibold))
                .foregroundColor(Theme.label)

            VStack(spacing: 6) {
                if let msg = appState.error {
                    Text(msg)
                        .font(Theme.fontSm)
                        .foregroundColor(Theme.labelTertiary)
                        .multilineTextAlignment(.center)
                        .padding(.horizontal, 48)
                }
                Text("Open Settings to configure your SSH host and identity key.")
                    .font(Theme.fontSm)
                    .foregroundColor(Theme.labelTertiary)
                    .multilineTextAlignment(.center)
                    .padding(.horizontal, 48)
            }

            Button("Open Settings") { showSettings = true }
                .buttonStyle(.borderedProminent)
                .accessibilityIdentifier("setup_open_settings_button")
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Theme.bgApp)
        .accessibilityIdentifier("setup_required_view")
        .sheet(isPresented: $showSettings) {
            DaemonSettingsPanel()
                .environmentObject(appState)
                .frame(minWidth: 480, minHeight: 360)
        }
    }
}

// MARK: - Startup splash (shown while establishing remote connection)

private struct StartupView: View {
    var body: some View {
        VStack(spacing: 14) {
            ProgressView()
                .scaleEffect(0.9)
            Text("Starting Nexus…")
                .font(.system(size: 13, weight: .medium))
                .foregroundColor(Theme.label)
            Text("Connecting to remote daemon…")
                .font(.system(size: 11))
                .foregroundColor(Theme.labelTertiary)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Theme.bgApp)
        .accessibilityIdentifier("startup_view")
    }
}

// MARK: - Inspector (right sidebar)

struct InspectorView: View {
    let workspace: Workspace
    @EnvironmentObject var appState: AppState

    var body: some View {
        BottomPanelView(workspace: workspace)
            .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity, alignment: .topLeading)
    }
}

// MARK: - Empty state

private struct EmptyStateView: View {
    let error: String?

    var body: some View {
        VStack(spacing: 10) {
            // Show operational errors whenever set (e.g. Create Base / RPC failures).
            // Do not gate on connection state — that hid failures while connected and looked like "no effect".
            if let msg = error {
                Image(systemName: "exclamationmark.triangle")
                    .font(.system(size: 24, weight: .ultraLight))
                    .foregroundColor(Theme.labelTertiary)
                Text(msg)
                    .font(Theme.fontSm)
                    .foregroundColor(Theme.labelTertiary)
                    .multilineTextAlignment(.center)
                    .padding(.horizontal, 32)
                    .accessibilityIdentifier("error_message")
            } else {
                Image(systemName: "terminal")
                    .font(.system(size: 28, weight: .ultraLight))
                    .foregroundColor(Theme.labelTertiary)
                Text("Select a sandbox")
                    .font(Theme.fontBody)
                    .foregroundColor(Theme.labelTertiary)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Theme.bgApp)
        .accessibilityIdentifier("empty_state_view")
    }
}
