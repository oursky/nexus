import NexusCore
import OSLog
import SwiftUI

// MARK: - Sheet root

struct NewWorkspaceSheet: View {
    private static let logger = Logger(subsystem: "com.nexus.NexusApp", category: "remote-folder-picker")

    @EnvironmentObject var appState: AppState
    @Environment(\.dismiss) private var dismiss
    let intent: CreateIntent

    @State private var workspaceName = "main"
    @State private var localPath     = ""
    @State private var selectedProjectID = ""
    @State private var createNewProject = false
    @State private var sourceMode: SourceMode = .projectRoot
    @State private var backend: RuntimeBackend = .libkrun
    @State private var selectedSourceWorkspaceID = ""
    @State private var freshSandbox = false
    @State private var isCreating    = false
    @State private var localError: String?
    /// Absolute repository path on the Linux engine (chosen via remote folder picker).
    @State private var engineRepoPath = ""
    /// Tracks current path while remote picker is open so re-renders don't reset navigation.
    @State private var enginePickerPath = ""
    @State private var showEngineFolderPicker = false

    init(intent: CreateIntent) {
        self.intent = intent
    }

    private enum SourceMode: String, CaseIterable, Identifiable {
        case projectRoot
        case specificSandbox
        case fresh
        var id: String { rawValue }
    }

    private enum RuntimeBackend: String, CaseIterable, Identifiable {
        case libkrun
        var id: String { rawValue }

        var label: String {
            switch self {
            case .libkrun: return "libkrun (VM)"
            }
        }

        static func from(_ raw: String) -> RuntimeBackend {
            let r = raw.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
            if r == "libkrun" { return .libkrun }
            return .libkrun
        }
    }

    private var selectedProject: Project? {
        appState.projects.first { $0.id == selectedProjectID }
    }



    private var projectWorkspaces: [Workspace] {
        guard let pid = selectedProject?.id else { return [] }
        return appState.repos.first(where: { $0.id == pid })?.workspaces ?? []
    }

    private var projectRootWorkspace: Workspace? {
        if let root = projectWorkspaces.first(where: { ($0.parentWorkspaceId ?? "").isEmpty }) {
            return root
        }
        return projectWorkspaces.first
    }

    private var isValid: Bool {
        if createNewProject {
            let t = engineRepoPath.trimmingCharacters(in: .whitespaces)
            return t.hasPrefix("/") && t.count > 1
        }
        let name = workspaceName.trimmingCharacters(in: .whitespaces)
        if name.isEmpty { return false }
        if sourceMode == .specificSandbox && selectedSourceWorkspaceID.trimmingCharacters(in: .whitespaces).isEmpty {
            return false
        }
        return !selectedProjectID.isEmpty
    }

    private var headerTitle: String {
        createNewProject ? "New Project" : "New Sandbox"
    }

    var body: some View {
        VStack(spacing: 0) {
            // ── Header ────────────────────────────────────────────
            HStack {
                Text(headerTitle)
                    .font(.system(size: 15, weight: .semibold))
                    .foregroundColor(Theme.label)
                Spacer()
                Button { dismiss() } label: {
                    Image(systemName: "xmark.circle.fill")
                        .font(.system(size: 16))
                        .foregroundColor(Theme.labelTertiary)
                }
                .buttonStyle(.plain)
            }
            .padding(.horizontal, 20)
            .padding(.top, 20)
            .padding(.bottom, 16)

            Divider().overlay(Theme.separator)

            // ── Form ─────────────────────────────────────────────
            VStack(alignment: .leading, spacing: 18) {

                if case .newSandbox(let pid) = intent, pid == nil {
                    FormField(label: "Project", isRequired: true) {
                        VStack(alignment: .leading, spacing: 8) {
                            Picker("Project", selection: $selectedProjectID) {
                                ForEach(appState.projects) { project in
                                    Text(project.name).tag(project.id)
                                }
                            }
                            .labelsHidden()
                            .onChange(of: selectedProjectID) { _, value in
                                if let root = appState.repos.first(where: { $0.id == value })?.workspaces.first(where: { ($0.parentWorkspaceId ?? "").isEmpty }) {
                                    selectedSourceWorkspaceID = root.id
                                } else {
                                    selectedSourceWorkspaceID = ""
                                }
                            }
                            .accessibilityIdentifier("sandbox_project_picker")
                        }
                    }
                }

                if !createNewProject {
                    FormField(label: "Sandbox name", isRequired: true) {
                        NexusTextField(
                            placeholder: "e.g. feature-auth",
                            text: $workspaceName,
                            accessibilityID: "sandbox_name_field"
                        )
                    }

                    FormField(label: "Fork source") {
                        Picker("Fork source", selection: $sourceMode) {
                            Text("Project root").tag(SourceMode.projectRoot)
                            Text("Specific sandbox").tag(SourceMode.specificSandbox)
                            Text("Fresh").tag(SourceMode.fresh)
                        }
                        .labelsHidden()
                        .accessibilityIdentifier("sandbox_fork_source_picker")
                    }

                    FormField(
                        label: "Runtime backend",
                        hint: sourceMode == .fresh ? "Used for fresh sandbox creation." : "Applies when Fork source is Fresh."
                    ) {
                        Picker("Runtime backend", selection: $backend) {
                            ForEach(RuntimeBackend.allCases) { option in
                                Text(option.label).tag(option)
                            }
                        }
                        .labelsHidden()
                        .disabled(sourceMode != .fresh)
                        .accessibilityIdentifier("sandbox_backend_picker")
                    }

                    if sourceMode == .specificSandbox {
                        FormField(label: "Source sandbox", isRequired: true) {
                            Picker("Source sandbox", selection: $selectedSourceWorkspaceID) {
                                ForEach(projectWorkspaces) { ws in
                                    Text(ws.name).tag(ws.id)
                                }
                            }
                            .labelsHidden()
                            .accessibilityIdentifier("sandbox_source_workspace_picker")
                        }
                    }
                    if sourceMode == .projectRoot && projectRootWorkspace == nil {
                        Text("Project root sandbox does not exist yet. It will be created automatically first.")
                            .font(.system(size: 11))
                            .foregroundColor(Theme.labelTertiary)
                    }
                }

                if createNewProject {
                    FormField(
                        label: "Repository on engine",
                        hint: "Same path the daemon bind-mounts into the sandbox (no Mutagen). Browse the remote machine over SSH, like VS Code Remote.",
                        isRequired: true
                    ) {
                        HStack(spacing: 8) {
                            NexusTextField(
                                placeholder: "/home/you/projects/my-app",
                                text: $engineRepoPath,
                                accessibilityID: "project_engine_path_field"
                            )
                            Button {
                                guard let prof = DaemonProfileStore().defaultProfile(),
                                      let st = prof.sshTarget?.trimmingCharacters(in: .whitespacesAndNewlines), !st.isEmpty else {
                                    localError = "Configure a remote profile with an SSH target first."
                                    Self.logger.error("sheet.browse.reject missing sshTarget")
                                    return
                                }
                                localError = nil
                                let typed = engineRepoPath.trimmingCharacters(in: .whitespacesAndNewlines)
                                Self.logger.notice("sheet.browse.tap typed=\(typed, privacy: .public) currentPickerPath=\(self.enginePickerPath, privacy: .public)")
                                if typed.hasPrefix("/") {
                                    enginePickerPath = typed
                                    showEngineFolderPicker = true
                                    Self.logger.notice("sheet.browse.open typedStart=\(typed, privacy: .public)")
                                    return
                                }
                                Task {
                                    let home = await Task.detached { [prof] in
                                        (try? EngineRemotePathBrowser.remoteHome(profile: prof)) ?? "/"
                                    }.value
                                    await MainActor.run {
                                        enginePickerPath = home
                                        showEngineFolderPicker = true
                                        Self.logger.notice("sheet.browse.open homeStart=\(home, privacy: .public)")
                                    }
                                }
                            } label: {
                                Text("Browse…")
                                    .font(.system(size: 12, weight: .medium))
                                    .foregroundColor(Theme.accent)
                                    .padding(.horizontal, 10)
                                    .padding(.vertical, 7)
                                    .background(
                                        RoundedRectangle(cornerRadius: 6)
                                            .stroke(Theme.accent.opacity(0.5), lineWidth: 1)
                                    )
                            }
                            .buttonStyle(.plain)
                        }
                    }
                }

                if isCreating, let progress = appState.workspaceCreateProgress {
                    VStack(alignment: .leading, spacing: 6) {
                        Text("Create progress")
                            .font(.system(size: 12, weight: .medium))
                            .foregroundColor(Theme.labelSecondary)
                        Text(progress.currentPhaseLabel + " · " + progress.elapsedLabel)
                            .font(.system(size: 11, weight: .medium))
                            .foregroundColor(Theme.label)
                            .lineLimit(2)
                        ForEach(progress.phaseTimings.prefix(3)) { phase in
                            Text("\(phase.label): \(phase.durationLabel)")
                                .font(.system(size: 11))
                                .foregroundColor(Theme.labelTertiary)
                                .lineLimit(1)
                        }
                        ForEach(progress.notes.prefix(2), id: \.self) { note in
                            Text(note)
                                .font(.system(size: 10))
                                .foregroundColor(Theme.labelTertiary)
                                .lineLimit(2)
                        }
                    }
                    .padding(10)
                    .background(
                        RoundedRectangle(cornerRadius: 7)
                            .fill(Theme.bgContent)
                            .overlay(
                                RoundedRectangle(cornerRadius: 7)
                                    .stroke(Theme.separator, lineWidth: 1)
                            )
                    )
                }

                if let err = localError ?? appState.error {
                    HStack(spacing: 6) {
                        Image(systemName: "exclamationmark.triangle.fill")
                            .font(.system(size: 11))
                            .foregroundColor(Theme.red)
                        Text(err)
                            .font(.system(size: 11))
                            .foregroundColor(Theme.red)
                    }
                }
            }
            .padding(20)

            Divider().overlay(Theme.separator)

            // ── Footer ────────────────────────────────────────────
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.escape, modifiers: [])
                    .buttonStyle(.plain)
                    .font(.system(size: 13))
                    .foregroundColor(Theme.labelSecondary)
                    .padding(.horizontal, 12)

                Button {
                    Task { await create() }
                } label: {
                    HStack(spacing: 6) {
                        if isCreating {
                            ProgressView().scaleEffect(0.7).frame(width: 12, height: 12)
                        }
                        Text(isCreating ? "Creating…" : (createNewProject ? "Create Project" : "Create Sandbox"))
                    }
                    .font(.system(size: 13, weight: .medium))
                    .foregroundColor(.white)
                    .padding(.horizontal, 14)
                    .padding(.vertical, 7)
                    .background(
                        RoundedRectangle(cornerRadius: 7)
                            .fill(isValid ? Theme.accent : Theme.labelTertiary)
                    )
                }
                .buttonStyle(.plain)
                .disabled(!isValid || isCreating)
                .accessibilityIdentifier(createNewProject ? "create_project_button" : "create_sandbox_button")
            }
            .padding(.horizontal, 20)
            .padding(.vertical, 14)
        }
        .frame(width: 520)
        .background(Theme.bgApp)
        .sheet(isPresented: $showEngineFolderPicker) {
            if let profile = DaemonProfileStore().defaultProfile() {
                RemoteEngineFolderPicker(
                    profile: profile,
                    startPath: enginePickerPath,
                    onPathChange: { path in
                        enginePickerPath = path
                        Self.logger.notice("sheet.picker.pathChange path=\(path, privacy: .public)")
                    },
                    onChoose: { path in
                        engineRepoPath = path
                        enginePickerPath = path
                        showEngineFolderPicker = false
                        Self.logger.notice("sheet.picker.choose path=\(path, privacy: .public)")
                    },
                    onCancel: {
                        Self.logger.notice("sheet.picker.cancel lastPath=\(self.enginePickerPath, privacy: .public)")
                        showEngineFolderPicker = false
                    }
                )
            }
        }
        .onChange(of: showEngineFolderPicker) { _, shown in
            Self.logger.notice("sheet.picker.presented shown=\(shown) pickerPath=\(self.enginePickerPath, privacy: .public)")
        }
        .onAppear {
            Self.logger.notice("sheet.onAppear intent=\(String(describing: self.intent), privacy: .public) pickerPath=\(self.enginePickerPath, privacy: .public)")
            switch intent {
            case .newProject:
                createNewProject = true
                selectedProjectID = "__new__"
            case .newSandbox(let projectID):
                // Invariant: sandbox intent must never enter project-create mode.
                createNewProject = false
                if let pid = projectID, !pid.isEmpty {
                    selectedProjectID = pid
                } else if let first = appState.projects.first {
                    selectedProjectID = first.id
                } else {
                    // No projects exist; leave selectedProjectID empty — the picker
                    // will be empty and the user cannot submit until a project exists.
                    selectedProjectID = ""
                }
            }
            if selectedSourceWorkspaceID.isEmpty, let root = projectRootWorkspace {
                selectedSourceWorkspaceID = root.id
                backend = RuntimeBackend.from(root.backend ?? "")
            }
        }
    }

    // MARK: - Create

    private func create() async {
        localError = nil
        isCreating = true
        defer { isCreating = false }

        var projectID = selectedProjectID
        if createNewProject {
            let repo = engineRepoPath.trimmingCharacters(in: .whitespaces)
            if let project = await appState.createProject(repo: repo) {
                projectID = project.id
                dismiss()
            } else {
                localError = appState.error
                appState.error = nil
            }
            return
        }
        if projectID.isEmpty {
            localError = "Select a project."
            return
        }

        let name = workspaceName.trimmingCharacters(in: .whitespaces)
        let ref = await resolveTargetRef(projectID: projectID)
        let explicitSourceID: String?
        switch sourceMode {
            case .projectRoot:
                if let root = projectRootWorkspace {
                    explicitSourceID = root.id
                } else if let root = await appState.ensureProjectRootSandbox(projectID: projectID) {
                    selectedSourceWorkspaceID = root.id
                    explicitSourceID = root.id
                } else {
                    localError = appState.error ?? "Unable to create project root sandbox."
                    appState.error = nil
                    return
                }
            case .specificSandbox:
                if selectedSourceWorkspaceID.isEmpty {
                    localError = "Select a source sandbox."
                    return
                }
                explicitSourceID = selectedSourceWorkspaceID
            case .fresh:
                explicitSourceID = nil
        }
        let useFresh = sourceMode == .fresh || freshSandbox

        let request = SandboxCreateRequest(
            projectId: projectID,
            targetBranch: ref,
            sourceBranch: nil,
            sourceWorkspaceId: explicitSourceID,
            fresh: useFresh,
            workspaceName: name,
            backend: backend.rawValue
        )
        await appState.createSandbox(request: request)

        if appState.error == nil {
            dismiss()
        } else {
            localError = appState.error
            appState.error = nil
        }
    }

    private func resolveTargetRef(projectID: String) async -> String {
        if sourceMode != .fresh, !selectedSourceWorkspaceID.isEmpty,
           let rec = LocalWorkspaceState.record(forWorkspaceID: selectedSourceWorkspaceID),
           let ref = try? GitLogReader.currentRef(repoDirectory: rec.localPath),
           !ref.isEmpty {
            return ref
        }

        if let project = appState.projects.first(where: { $0.id == projectID }) {
            let projectRepo = project.primaryRepo.trimmingCharacters(in: .whitespacesAndNewlines)
            if !projectRepo.isEmpty,
               let ref = try? GitLogReader.currentRef(repoDirectory: projectRepo),
               !ref.isEmpty {
                return ref
            }
        }

        return "main"
    }
}

// MARK: - Helpers

private struct FormField<Content: View>: View {
    let label: String
    var hint: String? = nil
    var isRequired = false
    @ViewBuilder let content: () -> Content

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 4) {
                Text(label)
                    .font(.system(size: 12, weight: .medium))
                    .foregroundColor(Theme.labelSecondary)
                if isRequired {
                    Text("*")
                        .font(.system(size: 12, weight: .medium))
                        .foregroundColor(Theme.accent)
                }
            }
            content()
            if let hint {
                Text(hint)
                    .font(.system(size: 11))
                    .foregroundColor(Theme.labelTertiary)
            }
        }
    }
}

private struct NexusTextField: View {
    let placeholder: String
    @Binding var text: String
    var accessibilityID: String? = nil
    @FocusState private var focused: Bool

    var body: some View {
        let field = TextField(placeholder, text: $text)
            .textFieldStyle(.plain)
            .font(.system(size: 13, design: .monospaced))
            .foregroundColor(Theme.label)
            .padding(.horizontal, 10)
            .padding(.vertical, 8)
            .background(
                RoundedRectangle(cornerRadius: 7)
                    .fill(Theme.bgContent)
                    .overlay(
                        RoundedRectangle(cornerRadius: 7)
                            .stroke(focused ? Theme.accent.opacity(0.5) : Theme.separator, lineWidth: 1)
                    )
            )
            .focused($focused)
        if let accessibilityID {
            field.accessibilityIdentifier(accessibilityID)
        } else {
            field
        }
    }
}
