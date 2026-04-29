import Foundation

public struct SSHSecurityScopedPaths {
    public let configPath: String?
    public let identityPath: String?
    fileprivate let scopedURLs: [URL]

    public static let empty = SSHSecurityScopedPaths(configPath: nil, identityPath: nil, scopedURLs: [])
}

public enum SSHSecurityScope {
    public static func resolve(profile: DaemonProfile, category: String) -> SSHSecurityScopedPaths {
        var scopedURLs: [URL] = []

        let config = resolvePath(
            explicitPath: profile.sshConfigPath,
            bookmark: profile.sshConfigBookmark,
            label: "ssh-config",
            category: category,
            scopedURLs: &scopedURLs
        )
        let identity = resolvePath(
            explicitPath: profile.sshIdentity,
            bookmark: profile.sshIdentityBookmark,
            label: "ssh-identity",
            category: category,
            scopedURLs: &scopedURLs
        )
        return SSHSecurityScopedPaths(configPath: config, identityPath: identity, scopedURLs: scopedURLs)
    }

    public static func stop(_ paths: SSHSecurityScopedPaths) {
        paths.scopedURLs.forEach { $0.stopAccessingSecurityScopedResource() }
    }

    private static func resolvePath(
        explicitPath: String?,
        bookmark: Data?,
        label: String,
        category: String,
        scopedURLs: inout [URL]
    ) -> String? {
        let fallback = explicitPath?.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let bookmark else {
            return fallback?.isEmpty == true ? nil : fallback
        }

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
                "bookmark resolve label=\(label) path=\(url.path) started=\(started) stale=\(stale)"
            )
            if started {
                scopedURLs.append(url)
            }
            return url.path
        } catch {
            AppLifecycleLog.warn(
                category,
                "bookmark resolve failed label=\(label): \(error.localizedDescription)"
            )
            return fallback?.isEmpty == true ? nil : fallback
        }
    }
}
