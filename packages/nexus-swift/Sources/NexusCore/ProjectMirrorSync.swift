import Foundation
import os

private let mirrorLogger = Logger(subsystem: "com.nexus.app", category: "ProjectMirror")

/// Keeps a per-project directory on the SSH daemon host in sync with a Mac folder via Mutagen,
/// so Firecracker and host-side PTY see a real Linux path.
public enum ProjectMirrorError: Error, LocalizedError {
    case noSSHTarget
    case mutagenNotFound
    case sshFailed(String)
    case mutagenFailed(String)

    public var errorDescription: String? {
        switch self {
        case .noSSHTarget:
            return "SSH target is not configured for this daemon profile."
        case .mutagenNotFound:
            return "Mutagen was not found. Install the nexus CLI (it embeds Mutagen on macOS) or add mutagen to your PATH."
        case .sshFailed(let msg):
            return "SSH failed: \(msg)"
        case .mutagenFailed(let msg):
            return "Mutagen failed: \(msg)"
        }
    }
}

public final class ProjectMirrorSync: @unchecked Sendable {
    public static let shared = ProjectMirrorSync()

    private let lock = NSLock()
    /// Remote absolute mirror path keyed by project id (stable for this app session).
    private var remotePathByProject: [String: String] = [:]
    private var sshWrapperDirs: [String: String] = [:]

    private init() {}

    private func sessionName(projectId: String) -> String {
        "nexus-mirror-\(mirrorSlug(projectId))"
    }

    /// Directory name under `~/.local/share/nexus/mirrors/` — filesystem- and shell-safe.
    /// (A bare `-` or other edge cases can make plain `mkdir -p …/mirrors/$slug` fail with "missing operand".)
    private func mirrorSlug(_ projectId: String) -> String {
        let raw = projectId.replacingOccurrences(of: "/", with: "-")
        let allowed = CharacterSet.alphanumerics.union(CharacterSet(charactersIn: "._-"))
        let filtered = String(raw.unicodeScalars.filter { allowed.contains($0) })
        if !filtered.isEmpty { return filtered }
        var h: UInt32 = 2166136261
        for b in projectId.utf8 {
            h ^= UInt32(b)
            h &*= 16777619
        }
        return "p\(String(format: "%08x", h))"
    }

    /// Ensures Mutagen is mirroring `localPath` to `~/.local/share/nexus/mirrors/<slug>` on the SSH host.
    /// Returns the **absolute path on the remote host** to pass as `workspace.repo` to the daemon.
    public func ensureMirror(localPath: String, projectId: String, profile: DaemonProfile) throws -> String {
        let pid = projectId.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !pid.isEmpty else {
            throw ProjectMirrorError.sshFailed("project id is empty (cannot create mirror directory)")
        }
        let trimmed = localPath.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            throw ProjectMirrorError.sshFailed("local path is empty")
        }
        guard let target = profile.sshTarget?.trimmingCharacters(in: .whitespacesAndNewlines), !target.isEmpty else {
            throw ProjectMirrorError.noSSHTarget
        }
        guard FileManager.default.fileExists(atPath: trimmed) else {
            throw ProjectMirrorError.sshFailed("local path does not exist: \(trimmed)")
        }

        lock.lock()
        if let cached = remotePathByProject[pid] {
            lock.unlock()
            return cached
        }
        lock.unlock()

        let slug = mirrorSlug(pid)
        let remoteAbs = try prepareRemoteMirrorDirectory(profile: profile, slug: slug)

        guard let mutagen = resolveMutagenBinary() else {
            throw ProjectMirrorError.mutagenNotFound
        }

        let wrapperDir = writeSSHWrapperIfNeeded(projectId: pid)
        ensureMutagenDaemon(mutagenPath: mutagen, sshWrapperDir: wrapperDir)

        let session = sessionName(projectId: pid)
        let betaURL = "\(target):\(remoteAbs)"

        // Stale sessions (e.g. halted-on-root-emptied from an earlier run) still appear in `sync list`, so we would
        // skip `create` and then never reach `watching`. Terminate halted sessions and recreate.
        if mutagenSessionListed(mutagenPath: mutagen, sessionName: session) {
            let prior = try mutagenSessionStatus(mutagenPath: mutagen, sessionName: session, sshWrapperDir: wrapperDir)
            if Self.shouldRecreateMutagenSession(status: prior) {
                mirrorLogger.info("terminating mutagen session \(session, privacy: .public) (status=\(prior ?? "nil", privacy: .public))")
                try terminateMutagenSession(mutagenPath: mutagen, sessionName: session, sshWrapperDir: wrapperDir)
            }
        }

        if !mutagenSessionListed(mutagenPath: mutagen, sessionName: session) {
            try createMutagenSession(
                mutagenPath: mutagen,
                sessionName: session,
                alpha: trimmed,
                beta: betaURL,
                sshWrapperDir: wrapperDir
            )
        }

        // Wait until the session is actually watching (beta connected + scanned). Flush before that fails with
        // "unable to flush … not currently able to synchronize" — that's a lifecycle ordering issue, not randomness.
        try waitUntilMutagenSessionWatching(
            mutagenPath: mutagen,
            sessionName: session,
            sshWrapperDir: wrapperDir
        )
        try flushMutagenSessionOnce(
            mutagenPath: mutagen,
            sessionName: session,
            sshWrapperDir: wrapperDir
        )

        lock.lock()
        remotePathByProject[pid] = remoteAbs
        lock.unlock()

        mirrorLogger.info("Project mirror ready \(session) -> \(remoteAbs, privacy: .public)")
        return remoteAbs
    }

    /// Stops Mutagen and removes the remote mirror directory (best-effort).
    public func stopMirror(projectId: String, profile: DaemonProfile) {
        let session = sessionName(projectId: projectId)
        let slug = mirrorSlug(projectId)

        lock.lock()
        remotePathByProject.removeValue(forKey: projectId)
        let wrapperPath = sshWrapperDirs.removeValue(forKey: projectId)
        lock.unlock()

        if let wrapperPath {
            try? FileManager.default.removeItem(atPath: wrapperPath)
        }

        guard let mutagen = resolveMutagenBinary() else { return }

        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: mutagen)
        proc.arguments = ["sync", "terminate", session]
        proc.standardOutput = Pipe()
        proc.standardError = Pipe()
        try? proc.run()
        proc.waitUntilExit()

        // Single-line script — multiline `set -euo pipefail` was misparsed on some remotes (bare `set` dumped env to stdout).
        let rm = ": \"${HOME:?}\"; _d=\"$HOME/.local/share/nexus/mirrors/\(slug)\"; rm -rf -- \"$_d\""
        do {
            _ = try runSSH(profile: profile, bash: rm)
        } catch {
            mirrorLogger.warning("mirror cleanup: \(error.localizedDescription, privacy: .public)")
        }
    }

    // MARK: - Remote prep

    /// Picks the last line that looks like an absolute path (handles stray MOTD or shell noise on stdout).
    private static func extractRemoteAbsolutePath(from sshOutput: String) -> String? {
        let lines = sshOutput.split(whereSeparator: \.isNewline).map { String($0).trimmingCharacters(in: .whitespacesAndNewlines) }
        if let last = lines.last(where: { $0.hasPrefix("/") && !$0.contains("=") }) {
            return last
        }
        let single = sshOutput.trimmingCharacters(in: .whitespacesAndNewlines)
        if single.hasPrefix("/"), !single.contains("=") { return single }
        return nil
    }

    private func prepareRemoteMirrorDirectory(profile: DaemonProfile, slug: String) throws -> String {
        guard !slug.isEmpty else {
            throw ProjectMirrorError.sshFailed("internal error: mirror slug is empty")
        }
        // Single-line only: avoid `set -euo pipefail` + newlines — some remotes treated `set` as a no-arg `set` and dumped the environment.
        let bash = ": \"${HOME:?}\"; _d=\"$HOME/.local/share/nexus/mirrors/\(slug)\"; mkdir -p -- \"$_d\" && cd \"$_d\" && pwd"
        let out = try runSSH(profile: profile, bash: bash)
        guard let path = Self.extractRemoteAbsolutePath(from: out) else {
            throw ProjectMirrorError.sshFailed("remote pwd returned unexpected output: \(out.prefix(500))")
        }
        return path
    }

    private func runSSH(profile: DaemonProfile, bash: String) throws -> String {
        guard let target = profile.sshTarget, !target.isEmpty else {
            throw ProjectMirrorError.noSSHTarget
        }
        var args: [String] = ["-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=no"]
        if let port = profile.sshPort, port != 22 {
            args.insert(contentsOf: ["-p", "\(port)"], at: 0)
        }
        if let identity = profile.sshIdentity, !identity.isEmpty {
            args += ["-i", identity]
        }
        args += [target, "bash", "-lc", bash]

        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/ssh")
        proc.arguments = args
        let out = Pipe()
        let err = Pipe()
        proc.standardOutput = out
        proc.standardError = err
        try proc.run()
        proc.waitUntilExit()

        let errData = err.fileHandleForReading.readDataToEndOfFile()
        let errStr = String(data: errData, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        let outData = out.fileHandleForReading.readDataToEndOfFile()
        let outStr = String(data: outData, encoding: .utf8) ?? ""

        guard proc.terminationStatus == 0 else {
            throw ProjectMirrorError.sshFailed(errStr.isEmpty ? "exit \(proc.terminationStatus)" : errStr)
        }
        return outStr
    }

    // MARK: - Mutagen

    private func resolveMutagenBinary() -> String? {
        let searchPaths = (ProcessInfo.processInfo.environment["PATH"] ?? "/usr/local/bin:/usr/bin:/bin")
            .split(separator: ":").map(String.init)
        for dir in searchPaths {
            let candidate = "\(dir)/mutagen"
            if FileManager.default.isExecutableFile(atPath: candidate) { return candidate }
        }
        return nil
    }

    private func mutagenSessionListed(mutagenPath: String, sessionName: String) -> Bool {
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: mutagenPath)
        proc.arguments = ["sync", "list"]
        let out = Pipe()
        proc.standardOutput = out
        proc.standardError = Pipe()
        guard (try? proc.run()) != nil else { return false }
        proc.waitUntilExit()
        guard proc.terminationStatus == 0 else { return false }
        let data = out.fileHandleForReading.readDataToEndOfFile()
        let str = String(data: data, encoding: .utf8) ?? ""
        return str.contains(sessionName)
    }

    private func createMutagenSession(
        mutagenPath: String,
        sessionName: String,
        alpha: String,
        beta: String,
        sshWrapperDir: String?
    ) throws {
        let args = [
            "sync", "create",
            "--name", sessionName,
            "--sync-mode", "two-way-safe",
            "--watch-polling-interval", "5",
            "--symlink-mode", "ignore",
            alpha,
            beta,
        ]
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: mutagenPath)
        proc.arguments = args
        if let sshWrapperDir {
            var env = ProcessInfo.processInfo.environment
            env["MUTAGEN_SSH_PATH"] = sshWrapperDir
            proc.environment = env
        }
        let errPipe = Pipe()
        proc.standardError = errPipe
        proc.standardOutput = Pipe()
        try proc.run()
        proc.waitUntilExit()
        let errData = errPipe.fileHandleForReading.readDataToEndOfFile()
        let errStr = String(data: errData, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        guard proc.terminationStatus == 0 else {
            throw ProjectMirrorError.mutagenFailed(errStr.isEmpty ? "sync create failed" : errStr)
        }
    }

    /// Blocks until `mutagen sync list` reports `status == "watching"` for this session (matches when flush is valid).
    private func waitUntilMutagenSessionWatching(
        mutagenPath: String,
        sessionName: String,
        sshWrapperDir: String?,
        deadlineSeconds: TimeInterval = 60
    ) throws {
        let deadline = Date().addingTimeInterval(deadlineSeconds)
        while Date() < deadline {
            let status = try mutagenSessionStatus(mutagenPath: mutagenPath, sessionName: sessionName, sshWrapperDir: sshWrapperDir)
            if status == "watching" {
                return
            }
            if let s = status, s.hasPrefix("halted") {
                throw ProjectMirrorError.mutagenFailed("mutagen session \(sessionName) is halted: \(s)")
            }
            Thread.sleep(forTimeInterval: 0.15)
        }
        let last = try? mutagenSessionStatus(mutagenPath: mutagenPath, sessionName: sessionName, sshWrapperDir: sshWrapperDir)
        throw ProjectMirrorError.mutagenFailed(
            "timeout waiting for mutagen session \(sessionName) to reach watching (last status: \(last ?? "nil"))"
        )
    }

    /// Parses `mutagen sync list --template '{{json .}}'` and returns the `status` field for `sessionName`.
    private func mutagenSessionStatus(mutagenPath: String, sessionName: String, sshWrapperDir: String?) throws -> String? {
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: mutagenPath)
        proc.arguments = ["sync", "list", "--template", "{{json .}}"]
        if let sshWrapperDir {
            var env = ProcessInfo.processInfo.environment
            env["MUTAGEN_SSH_PATH"] = sshWrapperDir
            proc.environment = env
        }
        let outPipe = Pipe()
        let errPipe = Pipe()
        proc.standardOutput = outPipe
        proc.standardError = errPipe
        try proc.run()
        proc.waitUntilExit()
        let data = outPipe.fileHandleForReading.readDataToEndOfFile()
        let errData = errPipe.fileHandleForReading.readDataToEndOfFile()
        let errStr = String(data: errData, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        guard proc.terminationStatus == 0 else {
            throw ProjectMirrorError.mutagenFailed(errStr.isEmpty ? "sync list failed (exit \(proc.terminationStatus))" : errStr)
        }
        guard !data.isEmpty else { return nil }
        let obj: [[String: Any]]
        do {
            guard let parsed = try JSONSerialization.jsonObject(with: data) as? [[String: Any]] else {
                return nil
            }
            obj = parsed
        } catch {
            mirrorLogger.warning("mutagen sync list JSON parse: \(error.localizedDescription, privacy: .public)")
            return nil
        }
        for row in obj {
            guard let name = row["name"] as? String, name == sessionName else { continue }
            return row["status"] as? String
        }
        return nil
    }

    private func flushMutagenSessionOnce(
        mutagenPath: String,
        sessionName: String,
        sshWrapperDir: String?
    ) throws {
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: mutagenPath)
        proc.arguments = ["sync", "flush", sessionName]
        if let sshWrapperDir {
            var env = ProcessInfo.processInfo.environment
            env["MUTAGEN_SSH_PATH"] = sshWrapperDir
            proc.environment = env
        }
        let errPipe = Pipe()
        proc.standardError = errPipe
        proc.standardOutput = Pipe()
        try proc.run()
        proc.waitUntilExit()
        let errData = errPipe.fileHandleForReading.readDataToEndOfFile()
        let errStr = String(data: errData, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        guard proc.terminationStatus == 0 else {
            throw ProjectMirrorError.mutagenFailed(errStr.isEmpty ? "sync flush failed" : errStr)
        }
    }

    private static func shouldRecreateMutagenSession(status: String?) -> Bool {
        guard let s = status else { return false }
        return s.hasPrefix("halted")
    }

    private func terminateMutagenSession(mutagenPath: String, sessionName: String, sshWrapperDir: String?) throws {
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: mutagenPath)
        proc.arguments = ["sync", "terminate", sessionName]
        if let sshWrapperDir {
            var env = ProcessInfo.processInfo.environment
            env["MUTAGEN_SSH_PATH"] = sshWrapperDir
            proc.environment = env
        }
        let outPipe = Pipe()
        let errPipe = Pipe()
        proc.standardOutput = outPipe
        proc.standardError = errPipe
        try proc.run()
        proc.waitUntilExit()
        let errData = errPipe.fileHandleForReading.readDataToEndOfFile()
        let errStr = String(data: errData, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        if proc.terminationStatus == 0 {
            return
        }
        let benign = errStr.localizedCaseInsensitiveContains("unable to terminate")
            || errStr.localizedCaseInsensitiveContains("not found")
            || errStr.localizedCaseInsensitiveContains("does not exist")
        if benign {
            mirrorLogger.warning("mutagen sync terminate \(sessionName): \(errStr, privacy: .public)")
            return
        }
        throw ProjectMirrorError.mutagenFailed(errStr.isEmpty ? "sync terminate failed (exit \(proc.terminationStatus))" : errStr)
    }

    /// Matches `mirror.ensureMutagenDaemon` in Go: start the daemon only if `daemon status` fails — never stop a running daemon.
    /// Restarting mutagen on every mirror broke sessions and caused flush races.
    private func ensureMutagenDaemon(mutagenPath: String, sshWrapperDir: String?) {
        let probe = Process()
        probe.executableURL = URL(fileURLWithPath: mutagenPath)
        probe.arguments = ["daemon", "status"]
        probe.standardOutput = Pipe()
        probe.standardError = Pipe()
        guard (try? probe.run()) != nil else { return }
        probe.waitUntilExit()
        if probe.terminationStatus == 0 {
            return
        }

        let start = Process()
        start.executableURL = URL(fileURLWithPath: mutagenPath)
        start.arguments = ["daemon", "start"]
        var env = ProcessInfo.processInfo.environment
        if let sshWrapperDir {
            env["MUTAGEN_SSH_PATH"] = sshWrapperDir
        }
        start.environment = env
        start.standardOutput = Pipe()
        start.standardError = Pipe()
        try? start.run()
        start.waitUntilExit()
    }

    private func nexusSSHConfigPath() -> String {
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        return "\(home)/.nexus/ssh/nexus.ssh.config"
    }

    private func writeSSHWrapperIfNeeded(projectId: String) -> String? {
        let cfg = nexusSSHConfigPath()
        guard FileManager.default.fileExists(atPath: cfg) else { return nil }
        let base = (NSTemporaryDirectory() as NSString).appendingPathComponent("nexus-project-mirror")
        let wrapperDir = (base as NSString).appendingPathComponent(projectId)
        let fm = FileManager.default
        try? fm.createDirectory(atPath: wrapperDir, withIntermediateDirectories: true)
        let quoted = "'" + cfg.replacingOccurrences(of: "'", with: "'\\''") + "'"
        let sshScript = "#!/bin/sh\nexec /usr/bin/ssh -F \(quoted) \"$@\"\n"
        let scpScript = "#!/bin/sh\nexec /usr/bin/scp -F \(quoted) \"$@\"\n"
        do {
            try sshScript.write(toFile: (wrapperDir as NSString).appendingPathComponent("ssh"), atomically: true, encoding: .utf8)
            try scpScript.write(toFile: (wrapperDir as NSString).appendingPathComponent("scp"), atomically: true, encoding: .utf8)
            try fm.setAttributes([.posixPermissions: 0o755], ofItemAtPath: (wrapperDir as NSString).appendingPathComponent("ssh"))
            try fm.setAttributes([.posixPermissions: 0o755], ofItemAtPath: (wrapperDir as NSString).appendingPathComponent("scp"))
            lock.lock()
            sshWrapperDirs[projectId] = wrapperDir
            lock.unlock()
            return wrapperDir
        } catch {
            mirrorLogger.warning("SSH wrapper failed: \(error.localizedDescription, privacy: .public)")
            return nil
        }
    }
}
