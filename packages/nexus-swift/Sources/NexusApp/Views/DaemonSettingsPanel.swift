import NexusCore
import SwiftUI
import Foundation

/// Popover shown when the user clicks the connection status pill.
struct DaemonSettingsPanel: View {
    @EnvironmentObject var appState: AppState
    @State private var isCheckRunning = false
    @State private var checkResult: DaemonCheckResult?

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            Divider().opacity(0.4)
            profilesSection
            Divider().opacity(0.4)
            provisionSection
            Divider().opacity(0.4)
            healthCheckSection
        }
        .frame(width: 320)
        .background(Theme.bgContent)
        .sheet(item: $checkResult) { result in
            DaemonCheckResultSheet(result: result)
        }
    }

    // MARK: - Header

    private var header: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 6) {
                Circle()
                    .fill(statusColor)
                    .frame(width: 8)
                Text(statusTitle)
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundColor(Theme.label)
                Spacer()
            }
            if let sub = statusSubtitle {
                Text(sub)
                    .font(.system(size: 11, design: .monospaced))
                    .foregroundColor(Theme.labelTertiary)
                    .lineLimit(3)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(12)
    }

    // MARK: - Profiles section

    @ViewBuilder
    private var profilesSection: some View {
        VStack(alignment: .leading, spacing: 0) {
            RemoteProfileSettingsView()
                .padding(.horizontal, 12)
                .padding(.vertical, 10)
        }
    }

    // MARK: - Provision section

    @ViewBuilder
    private var provisionSection: some View {
        VStack(alignment: .leading, spacing: 6) {
            // Live provision progress — shown while provisioning is in flight.
            if case .provisioning(let step) = appState.connectionState {
                HStack(spacing: 6) {
                    ProgressView().scaleEffect(0.65).frame(width: 14, height: 14)
                    Text(step)
                        .font(.system(size: 11, design: .monospaced))
                        .foregroundColor(Theme.labelSecondary)
                        .lineLimit(2)
                        .fixedSize(horizontal: false, vertical: true)
                }
                .padding(.top, 2)
            }
            HStack {
                Button {
                    appState.installDaemon()
                } label: {
                    Label(provisionButtonLabel, systemImage: provisionButtonIcon)
                }
                .buttonStyle(.borderless)
                .font(.system(size: 12))
                .foregroundColor(canProvision ? Theme.label : Theme.labelTertiary)
                .disabled(!canProvision)
                Spacer()
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
        .help(provisionButtonHelp)
    }

    private var canProvision: Bool {
        guard case .provisioning = appState.connectionState else {
            // Allow provisioning when offline or connected (re-provision).
            return appState.activeDaemonProfile?.sshTarget != nil
        }
        return false // already in progress
    }

    private var provisionButtonLabel: String {
        switch appState.connectionState {
        case .provisioning: return "Provisioning…"
        case .connected:    return "Re-provision Daemon"
        default:            return "Provision Daemon"
        }
    }

    private var provisionButtonIcon: String {
        switch appState.connectionState {
        case .provisioning: return "arrow.triangle.2.circlepath"
        case .connected:    return "arrow.triangle.2.circlepath"
        default:            return "bolt.fill"
        }
    }

    private var provisionButtonHelp: String {
        switch appState.connectionState {
        case .provisioning:
            return "Provisioning in progress — uploading binary, downloading rootfs, starting daemon."
        case .connected:
            return "Re-upload the daemon binary and restart it on the remote host. Use after updating the app."
        case .disconnected:
            return "Upload the daemon binary via SSH and start it on the remote host."
        default:
            return "Connect to a host profile first, then provision."
        }
    }

    // MARK: - Health check section

    @ViewBuilder
    private var healthCheckSection: some View {
        HStack {
            Button {
                Task { await runCheck() }
            } label: {
                if isCheckRunning {
                    HStack(spacing: 6) {
                        ProgressView().scaleEffect(0.7).frame(width: 14, height: 14)
                        Text("Running checks…")
                    }
                } else {
                    Label("Run Health Check", systemImage: "stethoscope")
                }
            }
            .buttonStyle(.borderless)
            .font(.system(size: 12))
            .foregroundColor(isDaemonConnected ? Theme.label : Theme.labelTertiary)
            .disabled(isCheckRunning || !isDaemonConnected)
            Spacer()
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
        .help(isDaemonConnected
              ? "Run nexus daemon check on the engine host to verify the full environment setup (KVM, kernel, rootfs, SSH keys, auth tokens, …)"
              : "Connect to a daemon first to run health checks.")
    }

    // MARK: - Actions

    private var isDaemonConnected: Bool {
        if case .running = appState.daemonStatus { return true }
        return false
    }

    private func runCheck() async {
        guard !isCheckRunning else { return }
        isCheckRunning = true
        defer { isCheckRunning = false }
        let (ok, output) = await appState.runDaemonCheckViaCLI()
        checkResult = DaemonCheckResult(id: UUID(), passed: ok, output: output)
    }

    // MARK: - Computed helpers

    private var statusColor: Color {
        switch appState.daemonStatus {
        case .running:              return Theme.green
        case .outdated:             return Theme.orange
        case .offline, .unknown:    return Color.gray
        }
    }

    private var statusTitle: String {
        switch appState.daemonStatus {
        case .running:  return "Daemon Running"
        case .outdated: return "Daemon Outdated"
        case .offline:  return "Daemon Offline"
        case .unknown:  return "Connecting…"
        }
    }

    private var statusSubtitle: String? {
        switch appState.daemonStatus {
        case .running(let info):
            let devNote = info.version == "0.0.0-dev" ? "  (dev build)" : ""
            return "v\(info.version)  ·  protocol \(info.protocolVersion)\(devNote)"
        case .outdated(let info):
            return "Running protocol v\(info.protocolVersion), requires v\(DaemonInfo.requiredProtocol)."
        case .offline:
            return "No remote daemon connected."
        case .unknown:
            return nil
        }
    }
}

// MARK: - Supporting types

struct DaemonCheckResult: Identifiable {
    let id: UUID
    let passed: Bool
    let output: String
}

struct DaemonCheckResultSheet: View {
    let result: DaemonCheckResult
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Image(systemName: result.passed ? "checkmark.circle.fill" : "exclamationmark.triangle.fill")
                    .foregroundColor(result.passed ? Theme.green : Theme.orange)
                Text(result.passed ? "All checks passed" : "Some checks failed")
                    .font(.headline)
                Spacer()
                Button("Done") { dismiss() }
                    .keyboardShortcut(.defaultAction)
            }

            Divider()

            ScrollView {
                Text(result.output.isEmpty ? "(no output)" : result.output)
                    .font(.system(size: 11, design: .monospaced))
                    .foregroundColor(Theme.label)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .textSelection(.enabled)
            }
            .frame(maxHeight: 400)
        }
        .padding(16)
        .frame(width: 520)
        .background(Theme.bgContent)
    }
}
