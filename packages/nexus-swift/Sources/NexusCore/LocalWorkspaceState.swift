import Foundation

/// Mirrors `~/.local/share/nexus/workspaces.json` written by the Nexus CLI so the app can
/// resolve Mac-side git paths for fork and git log — the same source of truth as `nexus workspace fork`.
public struct LocalWorkspaceRecord: Codable, Equatable, Sendable {
    public var workspaceID: String
    public var workspaceName: String
    public var localPath: String
    public var gitRoot: String
    public var isWorktree: Bool

    enum CodingKeys: String, CodingKey {
        case workspaceID
        case workspaceName
        case localPath
        case gitRoot
        case isWorktree
    }
}

public enum LocalWorkspaceState {
    private static func stateURL() -> URL {
        let base = ProcessInfo.processInfo.environment["XDG_DATA_HOME"].flatMap { URL(fileURLWithPath: $0) }
            ?? FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".local/share", isDirectory: true)
        return base.appendingPathComponent("nexus/workspaces.json", isDirectory: false)
    }

    public static func load() -> [String: LocalWorkspaceRecord] {
        let url = stateURL()
        guard let data = try? Data(contentsOf: url) else { return [:] }
        return (try? JSONDecoder().decode([String: LocalWorkspaceRecord].self, from: data)) ?? [:]
    }

    public static func saveRecord(_ record: LocalWorkspaceRecord) throws {
        var all = load()
        all[record.workspaceID] = record
        let url = stateURL()
        try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        let data = try JSONEncoder().encode(all)
        try data.write(to: url, options: .atomic)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: url.path)
    }

    public static func record(forWorkspaceID id: String) -> LocalWorkspaceRecord? {
        load()[id]
    }
}
