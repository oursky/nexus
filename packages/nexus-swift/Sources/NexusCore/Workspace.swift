import Foundation
import Combine

// MARK: - Sheet intent

public enum CreateIntent: Identifiable {
    case newProject
    case newSandbox(projectID: String?)

    public var id: String {
        switch self {
        case .newProject: return "newProject"
        case .newSandbox(let pid): return "newSandbox:\(pid ?? "")"
        }
    }
}

// MARK: - Status

/// Maps workspacemgr.WorkspaceState values from the daemon.
public enum WorkspaceStatus: String, Codable, Equatable, Sendable {
    case starting  = "starting"
    case running   = "running"
    case paused    = "paused"
    case stopped   = "stopped"
    case created   = "created"
    case restored  = "restored"

    public var isActive: Bool { self == .running || self == .restored }

    public var displayName: String {
        switch self {
        case .starting: "Starting…"
        case .running:  "Running"
        case .paused:   "Paused"
        case .stopped:  "Stopped"
        case .created:  "Ready"
        case .restored: "Running"
        }
    }
}

// MARK: - Actions

public enum WorkspaceAction: String, Sendable {
    case start, stop, remove, fork, create
}

// MARK: - In-flight operation state (transient UI state, set by AppState)

public enum WorkspaceOpState: Sendable, Equatable {
    case starting(detail: String?)
    case stopping
    case removing

    public var label: String {
        switch self {
        case .starting(let detail):
            let text = detail?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            return text.isEmpty ? "Starting…" : text
        case .stopping: return "Stopping…"
        case .removing: return "Removing…"
        }
    }
}

// MARK: - Workspace create profiling

public struct WorkspaceCreatePhaseTiming: Sendable, Equatable, Identifiable {
    public let id: String
    public let label: String
    public let durationSeconds: Double

    public init(id: String, label: String, durationSeconds: Double) {
        self.id = id
        self.label = label
        self.durationSeconds = durationSeconds
    }

    public var durationLabel: String {
        if durationSeconds >= 60 {
            return String(format: "%.1fm", durationSeconds / 60.0)
        }
        return String(format: "%.1fs", durationSeconds)
    }
}

public struct WorkspaceCreateProgress: Sendable, Equatable {
    public let workspaceID: String
    public let workspaceName: String
    public let elapsedSeconds: Double
    public let currentPhaseLabel: String
    public let phaseTimings: [WorkspaceCreatePhaseTiming]
    public let notes: [String]
    public let isComplete: Bool

    public init(
        workspaceID: String,
        workspaceName: String,
        elapsedSeconds: Double,
        currentPhaseLabel: String,
        phaseTimings: [WorkspaceCreatePhaseTiming],
        notes: [String],
        isComplete: Bool
    ) {
        self.workspaceID = workspaceID
        self.workspaceName = workspaceName
        self.elapsedSeconds = elapsedSeconds
        self.currentPhaseLabel = currentPhaseLabel
        self.phaseTimings = phaseTimings
        self.notes = notes
        self.isComplete = isComplete
    }

    public var elapsedLabel: String {
        if elapsedSeconds >= 60 {
            return String(format: "%.1fm", elapsedSeconds / 60.0)
        }
        return String(format: "%.1fs", elapsedSeconds)
    }

    public var sidebarLabel: String {
        "\(currentPhaseLabel) (\(elapsedLabel))"
    }
}

// MARK: - Workspace
// Field names match workspacemgr.Workspace JSON keys exactly.

public struct Workspace: Identifiable, Codable, Equatable, Sendable {
    public let id: String
    public let workspaceName: String
    public let repo: String
    public var ref: String
    public let targetBranch: String?
    public let currentRef: String?
    public let currentCommit: String?
    public let parentWorkspaceId: String?
    /// Lineage root workspace id (fork chains); mirrors daemon `lineageRootId`.
    public let lineageRootId: String?
    public var state: WorkspaceStatus
    public let rootPath: String
    public let agentProfile: String
    public var repoId: String?
    public var projectId: String?
    /// Daemon runtime backend id, e.g. libkrun, process.
    public let backend: String?
    /// Human-readable summary from daemon (`runtimeLabel` JSON), e.g. backend + isolation.
    public let runtimeLabel: String?
    /// Guest IPv4 on the engine bridge (`guestIp` in daemon JSON) when the libkrun VM is running.
    public var guestIp: String?
    public var ports: [ForwardedPort]
    public var hasActiveTunnels: Bool

    public var name: String   { workspaceName }
    public var branch: String {
        let candidate = (currentRef?.isEmpty == false ? currentRef : nil)
            ?? (targetBranch?.isEmpty == false ? targetBranch : nil)
            ?? (ref.isEmpty ? nil : ref)
        return candidate ?? "main"
    }
    public var status: WorkspaceStatus { state }
    public var snapshotCount: Int { 0 }

    private var normalizedBackendID: String {
        (backend ?? "").trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
    }

    /// True when the workspace uses a libkrun micro-VM backend.
    public var usesGuestVMRuntime: Bool {
        normalizedBackendID == "libkrun"
    }

    /// Short tag for lists (sidebar); full `runtimeLabel` is shown in the workspace detail strip when present.
    public var shortRuntimeBadge: String {
        let b = normalizedBackendID
        switch b {
        case "libkrun": return "VM"
        case "process": return "proc"
        case "":
            let label = (runtimeLabel ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            if label.isEmpty { return "" }
            return String(label.prefix(8))
        default: return String(b.prefix(6))
        }
    }

    /// Line for detail UI: prefer daemon-composed label, else backend name.
    public var detailRuntimeLine: String {
        let label = (runtimeLabel ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        if !label.isEmpty { return label }
        let b = (backend ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        return b
    }

    /// Absolute path on the engine host for Remote SSH editor links (not the VM-only `/workspace` path).
    public var remoteSSHRepoPath: String? {
        for candidate in [repo, rootPath].map({ $0.trimmingCharacters(in: .whitespacesAndNewlines) }) {
            guard candidate.hasPrefix("/"), candidate != "/workspace" else { continue }
            return candidate
        }
        return nil
    }

    /// Returns true for libkrun VM backends that currently have an SSH host available.
    private var isGuestVMBackend: Bool {
        let b = normalizedBackendID
        guard b == "libkrun" else { return false }
        guard state.isActive else { return false }
        let g = (guestIp ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        return !g.isEmpty
    }

    /// Stable `Host` alias for `~/.ssh/config.d` when opening a VM workspace in an external editor.
    public static func nexusSSHHostAlias(for workspaceID: String) -> String {
        let safe = workspaceID.replacingOccurrences(
            of: "[^a-zA-Z0-9-]+",
            with: "-",
            options: .regularExpression
        )
        return "nexus-vm-\(safe)"
    }

    /// Returns true if this workspace uses a libkrun VM backend,
    /// regardless of its current run state.
    private var isVMBackend: Bool { usesGuestVMRuntime }

    /// Resolves Remote-SSH parameters: VM guests (libkrun) use
    /// `/workspace` via a ProxyJump to the guest IP; process sandboxes use the
    /// engine repo path directly.
    ///
    /// Returns `nil` when the workspace cannot be opened (no valid path, or a VM
    /// backend that is not currently running — opening it would resolve to the
    /// daemon host folder, which is always wrong for VM workspaces).
    public func remoteSSHFolderOpen(jumpHost: String, identityFile: String?) -> RemoteSSHFolderOpenSpec? {
        let jump = jumpHost.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !jump.isEmpty else { return nil }
        let idf = identityFile?.trimmingCharacters(in: .whitespacesAndNewlines)
        let idfOrNil = (idf?.isEmpty ?? true) ? nil : idf

        if isVMBackend {
            // VM workspaces must be actively running to be opened in an editor.
            // Return nil when stopped so callers can show a "start the VM first"
            // prompt instead of silently opening the daemon-host folder.
            guard isGuestVMBackend else { return nil }
            let ip = (guestIp ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            let alias = Self.nexusSSHHostAlias(for: id)
            return RemoteSSHFolderOpenSpec(
                sshHostForURI: alias,
                remotePath: "/workspace",
                vmGuestIP: ip,
                proxyJump: jump,
                identityFile: idfOrNil
            )
        }

        guard let path = remoteSSHRepoPath else { return nil }
        return RemoteSSHFolderOpenSpec(
            sshHostForURI: jump,
            remotePath: path,
            vmGuestIP: nil,
            proxyJump: jump,
            identityFile: idfOrNil
        )
    }

    enum CodingKeys: String, CodingKey {
        case id, workspaceName, repo, ref, state, rootPath, agentProfile
        case targetBranch, currentRef, currentCommit
        case parentWorkspaceId = "parentWorkspaceId"
        case lineageRootId = "lineageRootId"
        case repoId = "repoId"
        case projectId = "projectId"
        case backend, runtimeLabel
        case guestIp
    }

    public init(from decoder: Decoder) throws {
        let c    = try decoder.container(keyedBy: CodingKeys.self)
        let raw  = try decoder.container(keyedBy: AnyCodingKey.self)
        id           = try c.decode(String.self, forKey: .id)
        workspaceName = try c.decodeIfPresent(String.self, forKey: .workspaceName) ?? ""
        repo         = try c.decodeIfPresent(String.self, forKey: .repo) ?? ""
        ref          = try c.decodeIfPresent(String.self, forKey: .ref) ?? "main"
        let decodedTargetBranch = try c.decodeIfPresent(String.self, forKey: .targetBranch)
        targetBranch = Workspace.decodeString(raw, keys: ["targetBranch", "target_branch"]) ?? decodedTargetBranch
        let decodedCurrentRef = try c.decodeIfPresent(String.self, forKey: .currentRef)
        currentRef = Workspace.decodeString(raw, keys: ["currentRef", "current_ref"]) ?? decodedCurrentRef
        let decodedCurrentCommit = try c.decodeIfPresent(String.self, forKey: .currentCommit)
        currentCommit = Workspace.decodeString(raw, keys: ["currentCommit", "current_commit"]) ?? decodedCurrentCommit
        parentWorkspaceId = try c.decodeIfPresent(String.self, forKey: .parentWorkspaceId)
        lineageRootId = try c.decodeIfPresent(String.self, forKey: .lineageRootId)
        state        = try c.decodeIfPresent(WorkspaceStatus.self, forKey: .state) ?? .stopped
        rootPath     = try c.decodeIfPresent(String.self, forKey: .rootPath) ?? ""
        agentProfile = try c.decodeIfPresent(String.self, forKey: .agentProfile) ?? ""
        repoId       = try c.decodeIfPresent(String.self, forKey: .repoId)
        projectId    = try c.decodeIfPresent(String.self, forKey: .projectId)
        backend      = try c.decodeIfPresent(String.self, forKey: .backend)
        runtimeLabel = try c.decodeIfPresent(String.self, forKey: .runtimeLabel)
        guestIp      = try c.decodeIfPresent(String.self, forKey: .guestIp)
        ports        = []
        hasActiveTunnels = false
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(id, forKey: .id)
        try c.encode(workspaceName, forKey: .workspaceName)
        try c.encode(repo, forKey: .repo)
        try c.encode(ref, forKey: .ref)
        try c.encodeIfPresent(targetBranch, forKey: .targetBranch)
        try c.encodeIfPresent(currentRef, forKey: .currentRef)
        try c.encodeIfPresent(currentCommit, forKey: .currentCommit)
        try c.encodeIfPresent(parentWorkspaceId, forKey: .parentWorkspaceId)
        try c.encodeIfPresent(lineageRootId, forKey: .lineageRootId)
        try c.encode(state, forKey: .state)
        try c.encode(rootPath, forKey: .rootPath)
        try c.encode(agentProfile, forKey: .agentProfile)
        try c.encodeIfPresent(repoId, forKey: .repoId)
        try c.encodeIfPresent(projectId, forKey: .projectId)
        try c.encodeIfPresent(backend, forKey: .backend)
        try c.encodeIfPresent(runtimeLabel, forKey: .runtimeLabel)
        try c.encodeIfPresent(guestIp, forKey: .guestIp)
    }

    public init(id: String, workspaceName: String, repo: String = "",
                ref: String = "main", state: WorkspaceStatus = .stopped,
                rootPath: String = "", agentProfile: String = "default",
                repoId: String? = nil, projectId: String? = nil,
                ports: [ForwardedPort] = [], hasActiveTunnels: Bool = false,
                backend: String? = nil, runtimeLabel: String? = nil,
                guestIp: String? = nil) {
        self.id           = id
        self.workspaceName = workspaceName
        self.repo         = repo
        self.ref          = ref
        self.targetBranch = nil
        self.currentRef   = nil
        self.currentCommit = nil
        self.parentWorkspaceId = nil
        self.lineageRootId = nil
        self.state        = state
        self.rootPath     = rootPath
        self.agentProfile = agentProfile
        self.repoId       = repoId
        self.projectId    = projectId
        self.backend      = backend
        self.runtimeLabel = runtimeLabel
        self.guestIp      = guestIp
        self.ports        = ports
        self.hasActiveTunnels = hasActiveTunnels
    }
}

private struct AnyCodingKey: CodingKey {
    var stringValue: String
    var intValue: Int? { nil }
    init?(stringValue: String) { self.stringValue = stringValue }
    init?(intValue: Int) { return nil }
}

private extension Workspace {
    static func decodeString(_ c: KeyedDecodingContainer<AnyCodingKey>, keys: [String]) -> String? {
        for key in keys {
            guard let k = AnyCodingKey(stringValue: key) else { continue }
            guard c.contains(k) else { continue }
            if let value = try? c.decodeIfPresent(String.self, forKey: k) {
                let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
                if !trimmed.isEmpty { return trimmed }
            }
        }
        return nil
    }
}

public struct Project: Identifiable, Codable, Equatable, Sendable {
    public let id: String
    public let name: String
    public let primaryRepo: String
    public let rootPath: String?

    enum CodingKeys: String, CodingKey {
        case id, name
        case primaryRepo = "repoUrl"
        case rootPath
    }
}

// MARK: - Workspace create spec

public struct WorkspaceCreateSpec: Encodable, Sendable {
    public let repo: String
    public let ref: String
    public let workspaceName: String
    public let agentProfile: String
    public let backend: String
    /// When true, `repo` is an absolute path on the daemon host (no Mutagen); must be set before resolving the path.
    public let engineLocalRepo: Bool

    public init(
        repo: String,
        ref: String,
        workspaceName: String,
        agentProfile: String = "default",
        backend: String = "",
        engineLocalRepo: Bool = false
    ) {
        self.repo = repo
        self.ref = ref
        self.workspaceName = workspaceName
        self.agentProfile = agentProfile
        self.backend = backend
        self.engineLocalRepo = engineLocalRepo
    }
}

/// JSON-RPC `workspace.create` `spec` object (daemon `domain/workspace.CreateSpec`).
public struct WorkspaceDaemonCreateSpec: Encodable, Sendable {
    public let repo: String
    public let ref: String
    public let workspaceName: String
    public let agentProfile: String
    public let backend: String
    public let projectId: String

    public init(
        repo: String,
        ref: String,
        workspaceName: String,
        agentProfile: String = "default",
        backend: String = "",
        projectId: String
    ) {
        self.repo = repo
        self.ref = ref
        self.workspaceName = workspaceName
        self.agentProfile = agentProfile
        self.backend = backend
        self.projectId = projectId
    }

    func asDictionary() throws -> [String: Any] {
        let data = try JSONEncoder().encode(self)
        let obj = try JSONSerialization.jsonObject(with: data) as? [String: Any]
        return obj ?? [:]
    }
}

// MARK: - Repo

public struct Repo: Identifiable, Sendable {
    public let id: String
    public let name: String
    public let remoteURL: String
    public var workspaces: [Workspace]

    public init(id: String, name: String, remoteURL: String, workspaces: [Workspace]) {
        self.id        = id
        self.name      = name
        self.remoteURL = remoteURL
        self.workspaces = workspaces
    }

    public static func fromRelations(_ groups: [RelationsGroup], workspaces: [Workspace]) -> [Repo] {
        guard !groups.isEmpty else {
            return workspaces.isEmpty ? [] : [Repo(id: "nexus", name: "nexus", remoteURL: "", workspaces: workspaces)]
        }
        return groups.map { group in
            let wsInGroup = workspaces.filter { ws in
                group.nodes.contains { $0.workspaceId == ws.id }
            }
            let displayName = group.displayName.isEmpty
                ? (group.repo.split(separator: "/").last.map(String.init) ?? group.repo)
                : group.displayName
            return Repo(id: group.repoId, name: displayName,
                        remoteURL: group.repo, workspaces: wsInGroup)
        }
    }

    public static func fromProjects(_ projects: [Project], workspaces: [Workspace]) -> [Repo] {
        guard !projects.isEmpty else { return [] }
        // Include all projects, even those with no workspaces. An empty project
        // remains visible in the sidebar so the user can add sandboxes to it
        // without losing access to the project entry after deleting the last one.
        return projects.map { project in
            let wsInProject = workspaces.filter { $0.projectId == project.id }
            return Repo(
                id: project.id,
                name: project.name,
                remoteURL: project.primaryRepo,
                workspaces: wsInProject
            )
        }
    }

    public static func grouping(_ workspaces: [Workspace]) -> [Repo] {
        var map: [String: [Workspace]] = [:]
        var order: [String] = []
        for ws in workspaces {
            // Group by repoId when present; fall back to the repo path so that
            // workspaces created via the CLI (no repoId) appear under their
            // project folder rather than a generic bucket.
            let key: String
            if let rid = ws.repoId, !rid.isEmpty {
                key = rid
            } else if !ws.repo.isEmpty {
                key = ws.repo
            } else {
                key = "nexus"
            }
            if map[key] == nil { order.append(key) }
            map[key, default: []].append(ws)
        }
        return order.map { key in
            let ws = map[key] ?? []
            let name = ws.first.flatMap { w in
                w.repo.split(separator: "/").last.map(String.init)
            } ?? key
            return Repo(id: key, name: name, remoteURL: ws.first?.repo ?? "", workspaces: ws)
        }
    }
}

// MARK: - Relations (wire types for workspace.relations.list)

public struct RelationsGroup: Decodable, Sendable {
    public let repoId: String
    public let repo: String
    public let displayName: String
    public let nodes: [RelationNode]
}

public struct RelationNode: Decodable, Sendable {
    public let workspaceId: String
    public let workspaceName: String
    public let state: WorkspaceStatus
}

// MARK: - Forwarded port

public struct ForwardedPort: Identifiable, Codable, Equatable, Sendable {
    public let id: Int
    public let remotePort: Int
    public let preferred: Bool
    public let tunneled: Bool
    public let process: String?
    /// Daemon spotlight forward id (`spot-…`), for `workspace.ports.remove`.
    public let forwardId: String?
    public var port: Int { id }
    public var localURL: URL {
        var components = URLComponents()
        components.scheme = "http"
        components.host = "localhost"
        components.port = id
        return components.url ?? URL(fileURLWithPath: "/")
    }

    public init(id: Int, remotePort: Int? = nil, preferred: Bool = false, tunneled: Bool = false, process: String? = nil, forwardId: String? = nil) {
        self.id = id
        self.remotePort = remotePort ?? id
        self.preferred = preferred
        self.tunneled = tunneled
        self.process = process
        self.forwardId = forwardId
    }
}
