import NexusCore
import SwiftUI
import Foundation

/// Popover shown when the user clicks the connection status pill.
struct DaemonSettingsPanel: View {
    @EnvironmentObject var appState: AppState

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            Divider().opacity(0.4)
            profilesSection
        }
        .frame(width: 320)
        .background(Theme.bgContent)
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
