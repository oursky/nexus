import Foundation

/// Tracks projects whose `repoUrl` is an **absolute path on the Linux daemon host** (direct bind mount),
/// so the app skips Mutagen mirroring for workspace creation.
public enum ProjectRepoBindingStore {
    private static let key = "nexus.projectIdsEngineLocalRepo"

    public static func setUsesEnginePath(_ projectId: String, _ value: Bool) {
        var ids = Set(UserDefaults.standard.stringArray(forKey: key) ?? [])
        if value {
            ids.insert(projectId)
        } else {
            ids.remove(projectId)
        }
        UserDefaults.standard.set(Array(ids), forKey: key)
    }

    public static func usesEnginePath(projectId: String) -> Bool {
        Set(UserDefaults.standard.stringArray(forKey: key) ?? []).contains(projectId)
    }
}
