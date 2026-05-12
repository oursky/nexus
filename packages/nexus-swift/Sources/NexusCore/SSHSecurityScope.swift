import Foundation

public struct SSHSecurityScopedPaths {
    public let configPath: String?
    public let identityPath: String?
    fileprivate let scopedURLs: [URL]

    public static let empty = SSHSecurityScopedPaths(configPath: nil, identityPath: nil, scopedURLs: [])
}

public enum SSHSecurityScope {
    /// Resolve the `.ssh` directory bookmark, derive config/identity paths,
    /// and start accessing security-scoped resources.
    public static func resolve(profile: DaemonProfile, category: String) -> SSHSecurityScopedPaths {
        var scopedURLs: [URL] = []

        let sshDirURL = resolveDir(
            explicitPath: profile.sshDir,
            bookmark: profile.sshDirBookmark,
            category: category,
            scopedURLs: &scopedURLs
        )

        let configPath: String? = sshDirURL?.appendingPathComponent("config").path
        let identityPath: String? = detectIdentity(in: sshDirURL, fallback: nil)

        return SSHSecurityScopedPaths(configPath: configPath, identityPath: identityPath, scopedURLs: scopedURLs)
    }

    public static func stop(_ paths: SSHSecurityScopedPaths) {
        paths.scopedURLs.forEach { $0.stopAccessingSecurityScopedResource() }
    }

    private static func resolveDir(
        explicitPath: String?,
        bookmark: Data?,
        category: String,
        scopedURLs: inout [URL]
    ) -> URL? {
        guard let bookmark else { return nil }
        do {
            var stale = false
            let url = try URL(
                resolvingBookmarkData: bookmark,
                options: [.withSecurityScope],
                relativeTo: nil,
                bookmarkDataIsStale: &stale
            )
            let started = url.startAccessingSecurityScopedResource()
            AppLifecycleLog.info(
                category,
                "bookmark resolve label=ssh-dir path=\(url.path) started=\(started) stale=\(stale)"
            )
            if started {
                scopedURLs.append(url)
            }
            return url
        } catch {
            AppLifecycleLog.warn(
                category,
                "bookmark resolve failed label=ssh-dir: \(error.localizedDescription)"
            )
            return nil
        }
    }

    /// Auto-detect a private key in the `.ssh` directory. Prefers ed25519, then rsa.
    private static func detectIdentity(in sshDir: URL?, fallback: String?) -> String? {
        guard let dir = sshDir else { return fallback }
        // Preferred order: ed25519 → rsa
        let candidates = ["id_ed25519", "id_rsa"]
        let fm = FileManager.default
        for name in candidates {
            let path = dir.appendingPathComponent(name).path
            if fm.fileExists(atPath: path) { return path }
        }
        return fallback
    }
}
