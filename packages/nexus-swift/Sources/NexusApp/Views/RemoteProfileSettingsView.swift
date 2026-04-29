import NexusCore
import SwiftUI
import AppKit

// MARK: - Status dot helper

private func statusColor(for status: ProfileStatus) -> Color {
    switch status {
    case .connected: return .green
    case .unreachable: return .red
    case .authFailed, .tlsError: return .orange
    case .protocolMismatch: return .yellow
    case .unknown: return .gray
    }
}

// MARK: - Profile row

private struct ProfileRow: View {
    let profile: DaemonProfile
    let onSetDefault: () -> Void
    let onEdit: () -> Void
    let onDelete: () -> Void

    var body: some View {
        HStack(spacing: 8) {
            Circle()
                .fill(statusColor(for: profile.lastKnownStatus))
                .frame(width: 8, height: 8)
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 4) {
                    Text(profile.name)
                        .font(.system(size: 12, weight: .medium))
                        .foregroundColor(.primary)
                    if profile.isDefault {
                        Text("default")
                            .font(.system(size: 10))
                            .padding(.horizontal, 4)
                            .padding(.vertical, 1)
                            .background(Color.green.opacity(0.15))
                            .cornerRadius(3)
                    }
                }
                if let sshTarget = profile.sshTarget, !sshTarget.isEmpty {
                    Text("\(profile.name) · \(sshTarget)")
                        .font(.system(size: 10, design: .monospaced))
                        .foregroundColor(.secondary)
                }
            }
            Spacer()
            if !profile.isDefault {
                Button("Set Default", action: onSetDefault)
                    .buttonStyle(.borderless)
                    .font(.system(size: 11))
            }
            Button(action: onEdit) {
                Image(systemName: "pencil")
                    .font(.system(size: 11))
            }
            .buttonStyle(.borderless)
            Button(action: onDelete) {
                Image(systemName: "trash")
                    .font(.system(size: 11))
                    .foregroundColor(.red)
            }
            .buttonStyle(.borderless)
        }
        .padding(.vertical, 4)
    }
}

// MARK: - Add / Edit sheet

private struct ProfileEditSheet: View {
    @Binding var profile: DaemonProfile
    let isNew: Bool
    let onCancel: () -> Void
    let onSave: (DaemonProfile) -> Void

    @State private var sshTargetText: String = ""
    @State private var sshPortText: String = ""
    @State private var sshIdentityText: String = ""
    @State private var sshIdentityBookmark: Data?

    private enum TestState: Equatable {
        case idle, running, ok, failed(String)
        static func == (lhs: TestState, rhs: TestState) -> Bool {
            switch (lhs, rhs) {
            case (.idle, .idle), (.running, .running), (.ok, .ok): return true
            case (.failed(let a), .failed(let b)): return a == b
            default: return false
            }
        }
    }
    @State private var testState: TestState = .idle
    @State private var validationMessage: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(isNew ? "Add Remote Profile" : "Edit Profile")
                .font(.headline)

            LabeledField("Name") {
                TextField("My Remote Daemon", text: $profile.name)
                    .textFieldStyle(.roundedBorder)
            }

            LabeledField("SSH Host") {
                TextField("user@linuxbox", text: $sshTargetText)
                    .textFieldStyle(.roundedBorder)
            }

            LabeledField("SSH Port") {
                TextField("22", text: $sshPortText)
                    .textFieldStyle(.roundedBorder)
                    .frame(width: 80)
            }

            LabeledField("Remote Port") {
                HStack {
                    TextField("7777", value: $profile.port, formatter: NumberFormatter())
                        .textFieldStyle(.roundedBorder)
                        .frame(width: 80)
                    Stepper("", value: $profile.port, in: 1...65535)
                        .labelsHidden()
                }
            }

            LabeledField("Identity") {
                HStack(spacing: 6) {
                    TextField("~/.ssh/id_ed25519", text: $sshIdentityText)
                        .textFieldStyle(.roundedBorder)
                    Button("Browse…") { chooseSSHIdentityFile() }
                    if !sshIdentityText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                        Button("Clear") {
                            sshIdentityText = ""
                            sshIdentityBookmark = nil
                        }
                    }
                }
            }

            // Test Connection
            LabeledField("") {
                HStack(spacing: 8) {
                    Button {
                        testConnection()
                    } label: {
                        HStack(spacing: 4) {
                            if testState == .running {
                                ProgressView().scaleEffect(0.6).frame(width: 12, height: 12)
                            }
                            Text(testState == .running ? "Testing…" : "Test Connection")
                        }
                    }
                    .disabled(testState == .running || sshTargetText.isEmpty || sshIdentityText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)

                    switch testState {
                    case .ok:
                        Label("OK", systemImage: "checkmark.circle.fill")
                            .foregroundColor(.green)
                            .font(.system(size: 11))
                    case .failed(let msg):
                        Label(msg, systemImage: "xmark.circle.fill")
                            .foregroundColor(.red)
                            .font(.system(size: 11))
                            .lineLimit(2)
                    default:
                        EmptyView()
                    }
                }
            }

            Text("Token is fetched automatically from the remote host via SSH tunnel.")
                .font(.caption)
                .foregroundColor(.secondary)

            if let validationMessage {
                Text(validationMessage)
                    .font(.caption)
                    .foregroundColor(.red)
            }

            Toggle("Set as Default", isOn: $profile.isDefault)
                .font(.system(size: 12))

            HStack {
                Spacer()
                Button("Cancel", action: onCancel)
                    .keyboardShortcut(.escape)
                Button("Save") {
                    let validation = validateInputs()
                    guard validation == nil else {
                        validationMessage = validation
                        return
                    }
                    var p = profile
                    let trimmedIdentity = sshIdentityText.trimmingCharacters(in: .whitespacesAndNewlines)
                    p.sshTarget = sshTargetText.trimmingCharacters(in: .whitespacesAndNewlines)
                    p.sshPort = Int(sshPortText)
                    p.sshIdentity = trimmedIdentity.isEmpty ? nil : trimmedIdentity
                    let identityChanged = trimmedIdentity != (profile.sshIdentity ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
                    if identityChanged, sshIdentityBookmark == profile.sshIdentityBookmark {
                        p.sshIdentityBookmark = nil
                    } else {
                        p.sshIdentityBookmark = sshIdentityBookmark
                    }
                    validationMessage = nil
                    onSave(p)
                }
                .keyboardShortcut(.return)
                .buttonStyle(.borderedProminent)
                .disabled(profile.name.isEmpty || sshTargetText.isEmpty)
            }
        }
        .padding(20)
        .frame(minWidth: 380)
        .onAppear {
            sshTargetText = profile.sshTarget ?? ""
            sshPortText = profile.sshPort.map { String($0) } ?? ""
            sshIdentityText = profile.sshIdentity ?? ""
            sshIdentityBookmark = profile.sshIdentityBookmark
        }
        .onChange(of: sshTargetText) { _ in testState = .idle }
        .onChange(of: sshPortText) { _ in testState = .idle }
        .onChange(of: sshIdentityText) { _ in testState = .idle }
        .onChange(of: sshTargetText) { _ in validationMessage = nil }
        .onChange(of: sshPortText) { _ in validationMessage = nil }
        .onChange(of: sshIdentityText) { _ in validationMessage = nil }
    }

    private func validateInputs() -> String? {
        let trimmedName = profile.name.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmedName.isEmpty { return "Profile name is required." }

        let target = sshTargetText.trimmingCharacters(in: .whitespacesAndNewlines)
        if target.isEmpty { return "SSH host is required." }
        if !target.contains("@") || target.hasPrefix("@") || target.hasSuffix("@") {
            return "SSH host must be in the form user@host."
        }

        let sshPort = sshPortText.trimmingCharacters(in: .whitespacesAndNewlines)
        if !sshPort.isEmpty {
            guard let port = Int(sshPort), (1...65535).contains(port) else {
                return "SSH port must be between 1 and 65535."
            }
        }
        let identity = sshIdentityText.trimmingCharacters(in: .whitespacesAndNewlines)
        if identity.isEmpty {
            return "SSH identity key is required. Click Browse… to select your private key."
        }
        return nil
    }

    private func testConnection() {
        testState = .running
        let testProfile = DaemonProfile(
            name: profile.name,
            port: profile.port,
            sshTarget: sshTargetText.isEmpty ? nil : sshTargetText,
            sshPort: Int(sshPortText),
            sshIdentity: sshIdentityText.isEmpty ? nil : sshIdentityText,
            sshIdentityBookmark: sshIdentityBookmark
        )
        Task {
            let mgr = SSHTunnelManager(profile: testProfile)
            do {
                let _ = try await mgr.start()
                let _ = try await mgr.fetchRemoteToken()
                await mgr.stop()
                await MainActor.run { testState = .ok }
            } catch {
                await mgr.stop()
                await MainActor.run { testState = .failed(error.localizedDescription) }
            }
        }
    }

    private func chooseSSHIdentityFile() {
        let panel = NSOpenPanel()
        panel.message = "Select SSH private key"
        panel.prompt = "Select"
        panel.canChooseDirectories = false
        panel.canChooseFiles = true
        panel.allowsMultipleSelection = false
        panel.canCreateDirectories = false
        panel.showsHiddenFiles = true
        panel.directoryURL = URL(fileURLWithPath: (NSHomeDirectory() as NSString).appendingPathComponent(".ssh"))

        if panel.runModal() == .OK, let url = panel.url {
            captureBookmark(from: url, targetPath: &sshIdentityText, targetBookmark: &sshIdentityBookmark)
            testState = .idle
        }
    }

    private func captureBookmark(from url: URL, targetPath: inout String, targetBookmark: inout Data?) {
        let started = url.startAccessingSecurityScopedResource()
        defer {
            if started { url.stopAccessingSecurityScopedResource() }
        }
        do {
            let bookmark = try url.bookmarkData(
                options: [.withSecurityScope, .securityScopeAllowOnlyReadAccess],
                includingResourceValuesForKeys: nil,
                relativeTo: nil
            )
            targetPath = url.path
            targetBookmark = bookmark
            validationMessage = nil
        } catch {
            validationMessage = "Failed to store file permission: \(error.localizedDescription)"
        }
    }
}

// MARK: - Labeled field helper

private struct LabeledField<Content: View>: View {
    let label: String
    @ViewBuilder let content: () -> Content
    init(_ label: String, @ViewBuilder content: @escaping () -> Content) {
        self.label = label
        self.content = content
    }
    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Text(label)
                .font(.system(size: 12))
                .foregroundColor(.secondary)
                .frame(width: 64, alignment: .trailing)
            content()
        }
    }
}

// MARK: - Main view

public struct RemoteProfileSettingsView: View {
    @EnvironmentObject private var appState: AppState

    @State private var profiles: [DaemonProfile] = []
    @State private var showSheet = false
    @State private var editingProfile: DaemonProfile = DaemonProfile(name: "")
    @State private var isNewProfile = true

    private let store = DaemonProfileStore()

    public init() {}

    public var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text("Daemon Profiles")
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundColor(.secondary)
                Spacer()
                Button {
                    editingProfile = DaemonProfile(name: "")
                    isNewProfile = true
                    showSheet = true
                } label: {
                    Image(systemName: "plus")
                        .font(.system(size: 11))
                }
                .buttonStyle(.borderless)
            }

            if profiles.isEmpty {
                Text("No profiles configured.")
                    .font(.caption)
                    .foregroundColor(.secondary)
            } else {
                ForEach(profiles) { profile in
                    ProfileRow(
                        profile: profile,
                        onSetDefault: { setDefault(profile) },
                        onEdit: {
                            editingProfile = profile
                            isNewProfile = false
                            showSheet = true
                        },
                        onDelete: { delete(profile) }
                    )
                    if profile.id != profiles.last?.id {
                        Divider().opacity(0.3)
                    }
                }
            }
        }
        .onAppear { profiles = store.load() }
        .sheet(isPresented: $showSheet) {
            ProfileEditSheet(
                profile: $editingProfile,
                isNew: isNewProfile,
                onCancel: { showSheet = false },
                onSave: { saved in
                    saveProfile(saved)
                    showSheet = false
                }
            )
        }
    }

    private func setDefault(_ target: DaemonProfile) {
        profiles = profiles.map { p in
            var copy = p
            copy.isDefault = (p.profileId == target.profileId)
            return copy
        }
        store.save(profiles)
        Task { await appState.reconnect() }
    }

    private func delete(_ target: DaemonProfile) {
        profiles.removeAll { $0.profileId == target.profileId }
        store.save(profiles)
        Task { await appState.reconnect() }
    }

    private func saveProfile(_ profile: DaemonProfile) {
        if isNewProfile {
            if profile.isDefault {
                profiles = profiles.map { p in var c = p; c.isDefault = false; return c }
            }
            profiles.append(profile)
        } else {
            if profile.isDefault {
                profiles = profiles.map { p in var c = p; c.isDefault = false; return c }
            }
            profiles = profiles.map { p in p.profileId == profile.profileId ? profile : p }
        }
        store.save(profiles)
        Task { await appState.reconnect() }
    }
}
