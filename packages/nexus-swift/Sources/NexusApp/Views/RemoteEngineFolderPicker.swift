import NexusCore
import OSLog
import SwiftUI

/// VS Code Remote–style folder browser: lists directories on the daemon host over SSH.
struct RemoteEngineFolderPicker: View {
    private static let logger = Logger(subsystem: "com.nexus.NexusApp", category: "remote-folder-picker")

    let profile: DaemonProfile
    /// Starting directory when the sheet opens; ignored if empty (defaults to $HOME).
    let startPath: String
    let onPathChange: (String) -> Void
    let onChoose: (String) -> Void
    let onCancel: () -> Void

    @State private var currentPath: String = ""
    @State private var pathField: String = ""
    @State private var entries: [RemoteListingEntry] = []
    @State private var isLoading = false
    @State private var errorText: String?
    /// Bumped on every navigation start; lets the last-started call win and
    /// prevents bootstrap results overwriting a user-initiated navigation.
    @State private var navGen = 0
    /// Prevents the initial home navigation from re-firing if SwiftUI re-evaluates .onAppear.
    @State private var bootstrapped = false

    var body: some View {
        VStack(spacing: 0) {
            // ── Header ─────────────────────────────────────────────────────
            HStack {
                Text("Choose folder on engine")
                    .font(.system(size: 15, weight: .semibold))
                Spacer()
                Button { onCancel() } label: {
                    Image(systemName: "xmark.circle.fill")
                        .foregroundColor(Theme.labelTertiary)
                }
                .buttonStyle(.plain)
            }
            .padding(.horizontal, 16)
            .padding(.top, 14)
            .padding(.bottom, 10)

            // ── Toolbar ────────────────────────────────────────────────────
            HStack(spacing: 8) {
                Button("Home") { Task { await goHome() } }
                    .buttonStyle(.plain)
                    .font(.system(size: 12, weight: .medium))
                    .disabled(isLoading)
                Button("Up") { Task { await goUp() } }
                    .buttonStyle(.plain)
                    .font(.system(size: 12, weight: .medium))
                    .disabled(isLoading || currentPath == "/" || currentPath.isEmpty)
                Button { Task { await navigateTo(currentPath) } } label: {
                    Image(systemName: "arrow.clockwise")
                        .font(.system(size: 12))
                }
                .buttonStyle(.plain)
                .disabled(isLoading || currentPath.isEmpty)
                Spacer()
            }
            .padding(.horizontal, 16)
            .padding(.bottom, 8)

            // ── Path bar ───────────────────────────────────────────────────
            HStack(spacing: 8) {
                Text("Path")
                    .font(.system(size: 11, weight: .medium))
                    .foregroundColor(Theme.labelTertiary)
                    .frame(width: 36, alignment: .leading)
                TextField("/home/…", text: $pathField)
                    .textFieldStyle(.plain)
                    .font(.system(size: 12, design: .monospaced))
                    .padding(.horizontal, 8)
                    .padding(.vertical, 6)
                    .background(RoundedRectangle(cornerRadius: 6).fill(Theme.bgContent))
                    .onSubmit { Task { await applyPathField() } }
                Button("Go") { Task { await applyPathField() } }
                    .buttonStyle(.plain)
                    .font(.system(size: 12, weight: .medium))
                    .disabled(isLoading)
            }
            .padding(.horizontal, 16)
            .padding(.bottom, 10)

            Divider().overlay(Theme.separator)

            // ── Error banner ───────────────────────────────────────────────
            if let errorText {
                Text(errorText)
                    .font(.system(size: 11))
                    .foregroundColor(Theme.red)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 16)
                    .padding(.vertical, 8)
            }

            // ── File list ──────────────────────────────────────────────────
            Group {
                if isLoading {
                    ProgressView()
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                } else {
                    ScrollView {
                        LazyVStack(alignment: .leading, spacing: 0) {
                            ForEach(entries) { entry in
                                if entry.isDirectory {
                                    Button { Task { await openDirectory(entry.name) } } label: {
                                        HStack(spacing: 8) {
                                            Image(systemName: "folder.fill")
                                                .font(.system(size: 12))
                                                .foregroundColor(Theme.accent.opacity(0.85))
                                                .frame(width: 18)
                                            Text(entry.name)
                                                .font(.system(size: 13))
                                                .foregroundColor(Theme.label)
                                            Spacer()
                                            Image(systemName: "chevron.right")
                                                .font(.system(size: 10))
                                                .foregroundColor(Theme.labelTertiary)
                                        }
                                        .padding(.horizontal, 12)
                                        .padding(.vertical, 7)
                                    }
                                    .buttonStyle(.plain)
                                } else {
                                    HStack(spacing: 8) {
                                        Image(systemName: "doc")
                                            .font(.system(size: 12))
                                            .foregroundColor(Theme.labelTertiary)
                                            .frame(width: 18)
                                        Text(entry.name)
                                            .font(.system(size: 13))
                                            .foregroundColor(Theme.labelTertiary)
                                        Spacer()
                                    }
                                    .padding(.horizontal, 12)
                                    .padding(.vertical, 6)
                                }
                                Divider().overlay(Theme.separator.opacity(0.5)).padding(.leading, 38)
                            }
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                    }
                }
            }
            .frame(minHeight: 260)

            Divider().overlay(Theme.separator)

            // ── Footer ─────────────────────────────────────────────────────
            HStack {
                Spacer()
                Button("Cancel", action: onCancel)
                    .buttonStyle(.plain)
                    .padding(.trailing, 8)
                Button("Choose folder") { onChoose(currentPath) }
                    .buttonStyle(.plain)
                    .font(.system(size: 13, weight: .semibold))
                    .disabled(isLoading || currentPath.isEmpty)
            }
            .padding(12)
        }
        .frame(width: 520, height: 460)
        .background(Theme.bgApp)
        .onAppear {
            Self.logger.notice("picker.onAppear bootstrapped=\(self.bootstrapped) startPath=\(self.startPath, privacy: .public)")
            // Run initial navigation only once for this presentation.
            guard !bootstrapped else { return }
            bootstrapped = true
            Task { await bootstrapNavigate() }
        }
    }

    // MARK: - Navigation helpers

    /// Each call grabs the current generation; only applies results when no newer nav has started.
    @MainActor
    private func navigateTo(_ path: String) async {
        let requested = path.trimmingCharacters(in: .whitespacesAndNewlines)
        Self.logger.notice("picker.navigate.start gen=\(self.navGen + 1) requested=\(requested, privacy: .public) current=\(self.currentPath, privacy: .public)")
        if requested.hasPrefix("/") {
            // Persist intent immediately so parent-driven view refreshes resume this path,
            // even before canonicalization/listing completes.
            onPathChange(requested)
        }
        navGen &+= 1
        let gen = navGen
        isLoading = true
        errorText = nil

        let outcome: Result<(String, [RemoteListingEntry]), Error> = await Task.detached { [profile, requested] in
            do {
                // Validate requested path by listing it directly; keep requested path as source
                // of truth to avoid accidental jump caused by remote canonicalization quirks.
                let list = try EngineRemotePathBrowser.listDirectory(path: requested, profile: profile)
                let resolved = Self.normalizeAbsolutePath(requested)
                return .success((resolved, list))
            } catch {
                return .failure(error)
            }
        }.value

        // Another navigation started while we were waiting — discard our stale result.
        guard navGen == gen else {
            Self.logger.notice("picker.navigate.stale gen=\(gen) latest=\(self.navGen) requested=\(requested, privacy: .public)")
            return
        }

        switch outcome {
        case .success(let (canon, list)):
            currentPath = canon
            pathField = canon
            entries = list
            errorText = nil
            onPathChange(canon)
            Self.logger.notice("picker.navigate.ok gen=\(gen) canon=\(canon, privacy: .public) entries=\(list.count)")
        case .failure(let err):
            errorText = err.localizedDescription
            entries = []
            Self.logger.error("picker.navigate.fail gen=\(gen) requested=\(requested, privacy: .public) error=\(err.localizedDescription, privacy: .public)")
        }
        isLoading = false
    }

    @MainActor
    private func bootstrapNavigate() async {
        let initial = startPath.trimmingCharacters(in: .whitespacesAndNewlines)
        Self.logger.notice("picker.bootstrap initial=\(initial, privacy: .public)")
        if initial.hasPrefix("/") {
            await navigateTo(initial)
        } else {
            await goHome()
        }
    }

    @MainActor
    private func goHome() async {
        let fallback = startPath.trimmingCharacters(in: .whitespacesAndNewlines)
        let home: String = await Task.detached { [profile] in
            (try? EngineRemotePathBrowser.remoteHome(profile: profile)) ?? "/"
        }.value
        let target = home.hasPrefix("/") ? home : (fallback.hasPrefix("/") ? fallback : "/")
        Self.logger.notice("picker.goHome resolvedHome=\(home, privacy: .public) fallback=\(fallback, privacy: .public) target=\(target, privacy: .public)")
        await navigateTo(target)
    }

    @MainActor
    private func goUp() async {
        guard !currentPath.isEmpty, currentPath != "/" else { return }
        let parent = URL(fileURLWithPath: currentPath).deletingLastPathComponent().path
        Self.logger.notice("picker.goUp current=\(self.currentPath, privacy: .public) parent=\(parent, privacy: .public)")
        await navigateTo(parent.isEmpty ? "/" : parent)
    }

    @MainActor
    private func applyPathField() async {
        let t = pathField.trimmingCharacters(in: .whitespacesAndNewlines)
        Self.logger.notice("picker.pathField.submit value=\(t, privacy: .public)")
        guard t.hasPrefix("/") else {
            errorText = "Path must be absolute (start with /)."
            Self.logger.error("picker.pathField.reject value=\(t, privacy: .public)")
            return
        }
        await navigateTo(t)
    }

    @MainActor
    private func openDirectory(_ name: String) async {
        let joined = currentPath == "/" ? "/\(name)" : (currentPath as NSString).appendingPathComponent(name)
        Self.logger.notice("picker.openDir name=\(name, privacy: .public) current=\(self.currentPath, privacy: .public) next=\(joined, privacy: .public)")
        await navigateTo(joined)
    }

    private nonisolated static func normalizeAbsolutePath(_ path: String) -> String {
        var p = path.trimmingCharacters(in: .whitespacesAndNewlines)
        guard p.hasPrefix("/") else { return p }
        while p.count > 1, p.hasSuffix("/") {
            p.removeLast()
        }
        return p
    }
}
