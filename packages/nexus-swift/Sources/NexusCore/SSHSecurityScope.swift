import Foundation

/// Resolved SSH paths (no security-scoped bookmarks needed — app has read-write
/// entitlement for ~/.ssh/).
public struct SSHSecurityScopedPaths {
    public let configPath: String
    public let identityPath: String?
    /// Always empty — no bookmarks to manage.
    fileprivate let scopedURLs: [URL] = []

    public static var empty: SSHSecurityScopedPaths {
        SSHSecurityScopedPaths(configPath: "\(NSHomeDirectory())/.ssh/config", identityPath: nil)
    }
}

public enum SSHSecurityScope {
    /// Resolve SSH paths using entitlement-based access (no bookmarks).
    public static func resolve(profile: DaemonProfile, category: String) -> SSHSecurityScopedPaths {
        let cfgPath = profile.sshConfigPath
        let idPath = profile.resolvedIdentity()

        AppLifecycleLog.info(category, "ssh resolve config=\(cfgPath) identity=\(idPath ?? "auto-detect-none")")
        return SSHSecurityScopedPaths(configPath: cfgPath, identityPath: idPath)
    }

    /// No-op — no security-scoped bookmarks to release.
    public static func stop(_ paths: SSHSecurityScopedPaths) {}
}
