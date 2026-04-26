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

/// Writes `~/.config/nexus/ssh` snippets so Remote-SSH can reach libkrun VM guests via `ProxyJump` through the engine host.
/// `~/.ssh/config` must already contain `Include ~/.config/nexus/ssh/*.ssh.config` (added by `installIncludeIfNeeded`).
public enum NexusSSHConfigSnippet {
    private static let marker = "# nexus-vm-remote-editor (managed by Nexus — do not remove)"
    private static let includeLine = "Include ~/.config/nexus/ssh/*.ssh.config"

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
        (realUserHome() as NSString).appendingPathComponent(".config/nexus/ssh")
    }

    private static func sshConfigPath() -> String {
        (realUserHome() as NSString).appendingPathComponent(".ssh/config")
    }

    /// Ensures `~/.ssh/config` contains `Include ~/.config/nexus/ssh/*.ssh.config`.
    /// If the line is already present (e.g. added by the Lima setup) this is a no-op.
    public static func installIncludeIfNeeded() throws {
        let sshDir = (realUserHome() as NSString).appendingPathComponent(".ssh")
        let cfgPath = sshConfigPath()
        try FileManager.default.createDirectory(atPath: sshDir, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        try FileManager.default.createDirectory(atPath: nexusSSHDir(), withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        var body = ""
        if FileManager.default.fileExists(atPath: cfgPath) {
            body = try String(contentsOfFile: cfgPath, encoding: .utf8)
        }

        // If the include already exists but not at the top, move it there. This
        // must come before any `Host *` block or OpenSSH will keep using the raw
        // alias as HostName, causing Remote-SSH to fail to resolve `nexus-vm-*`.
        if body.contains(includeLine) || body.contains(marker) {
            let lines = body.split(separator: "\n", omittingEmptySubsequences: false).map(String.init)
            var firstMeaningfulLine: String?
            for line in lines {
                let trimmed = line.trimmingCharacters(in: .whitespaces)
                if trimmed.isEmpty || trimmed.hasPrefix("#") { continue }
                firstMeaningfulLine = trimmed
                break
            }
            if firstMeaningfulLine == includeLine {
                try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: cfgPath)
                return
            }

            let filteredLines = lines.filter { line in
                let trimmed = line.trimmingCharacters(in: .whitespaces)
                return trimmed != marker && trimmed != includeLine
            }
            body = filteredLines.joined(separator: "\n")
            if !body.isEmpty, !body.hasPrefix("\n") {
                body = "\n" + body
            }
        }

        let block = "\(marker)\n\(includeLine)\n"
        try (block + body).write(toFile: cfgPath, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: cfgPath)
    }

    /// Overwrites `~/.config/nexus/ssh/<hostAlias>.ssh.config` with a ProxyJump stanza for the guest bridge IP.
    /// Sets `VSCODE_AGENT_FOLDER` and `CURSOR_AGENT_FOLDER` to `/workspace/.cursor-server` so
    /// VS Code / Cursor install their server on the large workspace disk, not the small rootfs.
    public static func writeVMJumpHost(hostAlias: String, guestIP: String, proxyJump: String, user: String = "root", identityFile: String?) throws {
        let sshDir = nexusSSHDir()
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
