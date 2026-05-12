import Foundation
import Darwin

/// Desktop editors that register a `vscode-remote` folder URL handler on macOS.
public enum RemoteEditorApp: String, Sendable, CaseIterable {
    case cursor = "cursor"
    case vscode = "vscode"
}

/// Parameters for opening a folder via Remote SSH (daemon host path or libkrun guest via ProxyJump).
public struct RemoteSSHFolderOpenSpec: Sendable {
    public let sshHostForURI: String
    public let remotePath: String
    public let vmGuestIP: String?
    public let proxyJump: String
    public let identityFile: String?

    public init(sshHostForURI: String, remotePath: String, vmGuestIP: String?, proxyJump: String, identityFile: String?) {
        self.sshHostForURI = sshHostForURI
        self.remotePath = remotePath
        self.vmGuestIP = vmGuestIP
        self.proxyJump = proxyJump
        self.identityFile = identityFile
    }
}

/// Writes `~/.nexus/ssh` snippets so Remote-SSH can reach libkrun VM guests via `ProxyJump` through the engine host.
/// `~/.ssh/config` must already contain `Include ~/.nexus/ssh/*.ssh.config` (added by `installIncludeIfNeeded`).
public enum NexusSSHConfigSnippet {
    private static let marker = "# nexus VM remote-editor (managed by Nexus — must be first)"
    private static let includeLines = [
        "Include ~/.nexus/ssh/*.ssh.config",
        "Include ~/Library/Containers/com.oursky.nexus/Data/.nexus/ssh/*.ssh.config",
        "Include ~/Library/Containers/com.oursky.nexus.local/Data/.nexus/ssh/*.ssh.config",
    ]

    private static func realUserHome() -> String {
        if let pw = getpwuid(getuid()), let dir = pw.pointee.pw_dir {
            let path = String(cString: dir)
            if !path.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                return path
            }
        }
        return NSHomeDirectoryForUser(NSUserName()) ?? FileManager.default.homeDirectoryForCurrentUser.path
    }

    private static func nexusSSHDir() -> String {
        (realUserHome() as NSString).appendingPathComponent(".nexus/ssh")
    }

    private static func sshConfigPath() -> String {
        (realUserHome() as NSString).appendingPathComponent(".ssh/config")
    }

    /// Ensures `~/.ssh/config` contains the `Include ~/.nexus/ssh/*.ssh.config` block
    /// (plus sandbox container path variants). If the line is already present this is a no-op.
    /// The include must be the very first line so that nexus VM host aliases are resolved
    /// before any catch-all `Host *` block.
    public static func installIncludeIfNeeded() throws {
        let sshDir = (realUserHome() as NSString).appendingPathComponent(".ssh")
        let cfgPath = sshConfigPath()
        try FileManager.default.createDirectory(atPath: sshDir, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        try FileManager.default.createDirectory(atPath: nexusSSHDir(), withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        try installIncludeBlock(at: cfgPath)
    }

    /// Identical to `installIncludeIfNeeded()` but reads from and writes to a
    /// security-scoped file `URL` obtained through an `NSOpenPanel` bookmark.
    /// Caller must have called `startAccessingSecurityScopedResource()` on the URL
    /// and must call `stopAccessingSecurityScopedResource()` after.
    public static func installIncludeIfNeeded(at sshConfigURL: URL) throws {
        // Ensure the container .nexus/ssh/ exists for later writeVMJumpHost calls.
        try FileManager.default.createDirectory(atPath: containerSSHDir(), withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        try installIncludeBlock(at: sshConfigURL.path)
    }

    // MARK: Internal helpers

    /// Core Include-line logic shared by both entry points.
    private static func installIncludeBlock(at cfgPath: String) throws {
        var body = ""
        if FileManager.default.fileExists(atPath: cfgPath) {
            body = try String(contentsOfFile: cfgPath, encoding: .utf8)
        }

        let hasAllIncludes = includeLines.allSatisfy { body.contains($0) }
        if hasAllIncludes {
            // Already present — ensure the first managed Include line is at the top.
            let lines = body.split(separator: "\n", omittingEmptySubsequences: false).map(String.init)
            for line in lines {
                let trimmed = line.trimmingCharacters(in: .whitespaces)
                if trimmed.isEmpty || trimmed.hasPrefix("#") { continue }
                if trimmed == includeLines[0] {
                    try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: cfgPath)
                    return
                }
                break
            }
        }

        // Remove any previous managed block and any existing copies of our include lines.
        if let range = body.range(of: "\(marker)\n") {
            body.removeSubrange(range)
        }
        for inc in includeLines {
            body = body.replacingOccurrences(of: inc, with: "")
        }
        // Clean up blank lines resulting from removals.
        body = body.replacingOccurrences(of: "\n\n\n", with: "\n\n")

        let block = marker + "\n" + includeLines.joined(separator: "\n") + "\n\n"
        let newBody = block + body
        try newBody.write(toFile: cfgPath, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: cfgPath)
    }

    /// Overwrites `~/.nexus/ssh/<hostAlias>.ssh.config` with a ProxyJump stanza for the guest bridge IP.
    /// Sets `VSCODE_AGENT_FOLDER` and `CURSOR_AGENT_FOLDER` to `/workspace/.cursor-server` so
    /// VS Code / Cursor install their server on the large workspace disk, not the small rootfs.
    /// Returns the sandbox-safe SSH config directory for writing VM jump-host snippets.
    /// In a sandboxed app, `NSHomeDirectory()` returns the container home
    /// (e.g. `~/Library/Containers/com.oursky.nexus.local/Data/`), NOT the real home.
    /// The Include lines in `~/.ssh/config` already reference this container path,
    /// so OpenSSH clients find the snippets written here.
    private static func containerSSHDir() -> String {
        (NSHomeDirectory() as NSString).appendingPathComponent(".nexus/ssh")
    }

    public static func writeVMJumpHost(hostAlias: String, guestIP: String, proxyJump: String, user: String = "root", identityFile: String?) throws {
        // Write to the sandbox container's .nexus/ssh so the file is accessible
        // to the app AND picked up by the container-path Include line in ~/.ssh/config.
        let sshDir = containerSSHDir()
        try FileManager.default.createDirectory(atPath: sshDir, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        let path = (sshDir as NSString).appendingPathComponent("\(hostAlias).ssh.config")

        // guestIP may be "host:port" (slirp4netns port-forward target) or a plain IP.
        let hostName: String
        let sshPort: String?
        if guestIP.contains(":"), let colonIdx = guestIP.lastIndex(of: ":") {
            hostName = String(guestIP[guestIP.startIndex..<colonIdx])
            sshPort = String(guestIP[guestIP.index(after: colonIdx)...])
        } else {
            hostName = guestIP
            sshPort = nil
        }

        var lines: [String] = [
            "# Generated by Nexus for workspace VM Remote-SSH (updated on each open).",
            "Host \(hostAlias)",
            "  HostName \(hostName)",
            "  User \(user)",
            "  ProxyJump \(proxyJump)",
            "  StrictHostKeyChecking accept-new",
            "  UserKnownHostsFile /dev/null",
            "  SetEnv VSCODE_AGENT_FOLDER=/workspace/.cursor-server CURSOR_AGENT_FOLDER=/workspace/.cursor-server",
        ]
        if let port = sshPort {
            lines.append("  Port \(port)")
        }
        if let idf = identityFile?.trimmingCharacters(in: .whitespacesAndNewlines), !idf.isEmpty {
            lines.append("  IdentityFile \(NSString(string: idf).expandingTildeInPath)")
        }
        try lines.joined(separator: "\n").appending("\n").write(toFile: path, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: path)
    }
}

/// Builds `cursor://` / `vscode://` URLs that open a folder on a remote host over Remote SSH.
///
/// Same URI shape as `cursor --folder-uri vscode-remote://ssh-remote+user@host/path` and VS Code’s CLI.
public enum RemoteEditorURLBuilder {
    /// Returns a URL that opens `absoluteRemotePath` on the SSH host in the given editor, or nil if inputs are unusable.
    public static func folderURL(app: RemoteEditorApp, sshTarget: String, absoluteRemotePath: String) -> URL? {
        let target = sshTarget.trimmingCharacters(in: .whitespacesAndNewlines)
        let rawPath = absoluteRemotePath.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !target.isEmpty, rawPath.hasPrefix("/") else { return nil }

        var c = URLComponents()
        c.scheme = app.rawValue
        c.host = "vscode-remote"
        c.path = "/ssh-remote+\(target)\(rawPath)"
        return c.url
    }
}
