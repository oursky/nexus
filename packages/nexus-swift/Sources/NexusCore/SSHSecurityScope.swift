import Foundation

/// Resolved SSH paths, including any security-scoped resources that are currently
/// being accessed. Call `SSHSecurityScope.stop(_:)` when done to release the access.
public struct SSHSecurityScopedPaths {
    public let configPath: String
    public let identityPath: String?
    /// URLs for which `startAccessingSecurityScopedResource()` was called.
    fileprivate let scopedURLs: [URL]

    public static var empty: SSHSecurityScopedPaths {
        SSHSecurityScopedPaths(
            configPath: "\(NSHomeDirectory())/.ssh/config",
            identityPath: nil,
            scopedURLs: []
        )
    }
}

public enum SSHSecurityScope {
    /// Resolve SSH paths for `profile`.
    ///
    /// If the profile carries a security-scoped bookmark for the identity file, the
    /// bookmark is resolved and `startAccessingSecurityScopedResource()` is called so
    /// the app can read the key file from within App Sandbox. Call `stop(_:)` when the
    /// resolved paths are no longer needed (e.g. after the key has been loaded into the
    /// in-process ssh-agent).
    ///
    /// Falls back to `profile.resolvedIdentity()` (path-only) when no bookmark is stored.
    public static func resolve(profile: DaemonProfile, category: String) -> SSHSecurityScopedPaths {
        let cfgPath = profile.sshConfigPath
        var scopedURLs: [URL] = []

        // Try to resolve the security-scoped bookmark for the identity file.
        if let bookmarkData = profile.sshIdentityBookmark {
            var isStale = false
            do {
                let url = try URL(
                    resolvingBookmarkData: bookmarkData,
                    options: .withSecurityScope,
                    relativeTo: nil,
                    bookmarkDataIsStale: &isStale
                )
                if isStale {
                    AppLifecycleLog.warn(category,
                        "ssh bookmark is stale — user should re-select the identity key in Settings")
                }
                if url.startAccessingSecurityScopedResource() {
                    scopedURLs.append(url)
                    let identityPath = url.path
                    AppLifecycleLog.info(category,
                        "ssh resolve identity=\(identityPath) (security-scoped bookmark stale=\(isStale))")
                    return SSHSecurityScopedPaths(
                        configPath: cfgPath,
                        identityPath: identityPath,
                        scopedURLs: scopedURLs
                    )
                } else {
                    AppLifecycleLog.warn(category,
                        "ssh bookmark resolved but startAccessingSecurityScopedResource() denied")
                }
            } catch {
                AppLifecycleLog.warn(category,
                    "ssh bookmark resolve failed: \(error.localizedDescription)")
            }
        }

        // Fall back to direct path resolution (works with the .ssh/ temporary exception
        // entitlement or on ad-hoc signed debug builds).
        let idPath = profile.resolvedIdentity()
        AppLifecycleLog.info(category,
            "ssh resolve config=\(cfgPath) identity=\(idPath ?? "auto-detect-none") (path-based)")
        return SSHSecurityScopedPaths(configPath: cfgPath, identityPath: idPath, scopedURLs: [])
    }

    /// Stop security-scoped resource access for all URLs in `paths`.
    public static func stop(_ paths: SSHSecurityScopedPaths) {
        for url in paths.scopedURLs {
            url.stopAccessingSecurityScopedResource()
        }
    }
}
