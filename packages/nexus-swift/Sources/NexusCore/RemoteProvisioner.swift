import Foundation
import CryptoKit
import os
import Darwin

/// Provisions the Nexus daemon on a remote Linux host before the tunnel connects.
///
/// `provision()` returns only when the daemon is actually ready to accept connections
/// (healthz passes), not when the start command merely exits.
///
/// Flow for a fresh host:
///   1. SSH: check if daemon is already running (fast-path)
///   2. SSH: check if nexus binary exists
///   3. If missing: upload the bundled Linux binary via stdin pipe
///   4. SSH: launch `nexus daemon start --json` — emits structured phase events
///   5. Parse rootless bootstrap phases: preflight → asset-install → runtime-verify → daemon-launch
///   6. Poll daemon healthz until ready or timeout
public actor RemoteProvisioner {
    /// Progress step reported to the UI during provisioning.
    public enum Step: Sendable, Equatable {
        case checkingHost
        case uploadingBinary(progress: Double)  // 0.0–1.0
        case startingDaemon
        case bootstrapPhase(phase: String, message: String)
        case waitingForDaemon(attempt: Int)
        case ready
    }

    public typealias ProgressHandler = @Sendable (Step) async -> Void

    private let profile: DaemonProfile
    private let logger = Logger(subsystem: "com.nexus.NexusApp", category: "RemoteProvisioner")
    private var sshScopedPaths: SSHSecurityScopedPaths = .empty

    /// Probe SSH connectivity only — no daemon check, no upload, no tunnel.
    /// Runs `echo ok` over SSH and returns true on success.
    /// Throws a descriptive error (with raw SSH stderr) if auth fails or host is unreachable.
    public static func probeSSH(profile: DaemonProfile) async throws -> Bool {
        let provisioner = RemoteProvisioner(profile: profile)
        return try await provisioner._probeSSH()
    }

    private func _probeSSH() throws -> Bool {
        guard let sshTarget = profile.sshTarget?.trimmingCharacters(in: .whitespacesAndNewlines),
              !sshTarget.isEmpty else {
            throw ProvisionError.noSSHTarget
        }
        sshScopedPaths = SSHSecurityScope.resolve(profile: profile, category: "probe")
        defer {
            SSHSecurityScope.stop(sshScopedPaths)
            sshScopedPaths = .empty
        }
        let result = try runSSH(sshTarget: sshTarget, script: "echo ok")
        return result.trimmingCharacters(in: .whitespacesAndNewlines) == "ok"
    }

    /// Minimum daemon version we require on the remote.
    static let minimumVersion = "0.0.1"

    public init(profile: DaemonProfile) {
        self.profile = profile
    }

    /// Ensure the remote host has a running nexus daemon.
    ///
    /// Returns immediately if the daemon is already running.
    /// Throws `ProvisionError` if provisioning fails after all retries.
    public func provision(progress: ProgressHandler? = nil) async throws {
        guard let sshTarget = profile.sshTarget?.trimmingCharacters(in: .whitespacesAndNewlines),
              !sshTarget.isEmpty else {
            throw ProvisionError.noSSHTarget
        }
        guard let binaryURL = bundledLinuxBinary() else {
            throw ProvisionError.bundledBinaryMissing
        }
        sshScopedPaths = SSHSecurityScope.resolve(profile: profile, category: "provision")
        defer {
            SSHSecurityScope.stop(sshScopedPaths)
            sshScopedPaths = .empty
        }
        let identityPath = sshScopedPaths.identityPath ?? profile.sshIdentity
        guard let identityPath, !identityPath.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            throw ProvisionError.sshIdentityRequired
        }

        await progress?(.checkingHost)

        // ── 0. Probe SSH connectivity — fail fast before any upload attempt ──────
        // A simple `echo ok` will exit 255 with "Permission denied" on auth failure,
        // throwing sshAuthFailed immediately rather than hanging in the upload pipe.
        _ = try runSSH(sshTarget: sshTarget, script: "echo ok")

        // ── 1. Check daemon health + binary freshness ───────────────────────────
        let daemonInitiallyHealthy = await isDaemonHealthy(sshTarget: sshTarget)

        // ── 2. Ensure nexus binary exists and matches bundled build ─────────────
        let needsUpload = await binaryNeedsUpload(sshTarget: sshTarget, bundledBinaryURL: binaryURL)
        if needsUpload {
            logger.info("provision: updating nexus binary on \(sshTarget, privacy: .public)")
            try await uploadNexusBinary(sshTarget: sshTarget, binaryURL: binaryURL, progress: progress)
            // The running daemon still uses the previous executable image.
            if daemonInitiallyHealthy {
                try await stopDaemonIfRunning(sshTarget: sshTarget)
            }
        } else {
            logger.info("provision: nexus binary already up-to-date on \(sshTarget, privacy: .public)")
        }

        // If daemon was healthy and no binary update was needed, we're done.
        if daemonInitiallyHealthy && !needsUpload {
            logger.info("provision: daemon already healthy on \(sshTarget, privacy: .public)")
            await progress?(.ready)
            return
        }

        // ── 3. Start daemon with rootless bootstrap (streaming --json events) ──
        await progress?(.startingDaemon)
        try await startDaemonWithPhaseEvents(sshTarget: sshTarget, progress: progress)

        // ── 4. Poll until daemon healthz responds ──────────────────────────────
        try await waitForDaemon(sshTarget: sshTarget, progress: progress)

        await progress?(.ready)
        logger.info("provision: daemon ready on \(sshTarget, privacy: .public)")
    }

    // MARK: - Private helpers

    private func isDaemonHealthy(sshTarget: String) async -> Bool {
        let result = try? runSSH(sshTarget: sshTarget, script: """
            curl -sf --max-time 2 http://127.0.0.1:\(profile.port)/healthz >/dev/null 2>&1 && echo ok || echo no
            """)
        return result?.trimmingCharacters(in: .whitespacesAndNewlines) == "ok"
    }

    private func binaryNeedsUpload(sshTarget: String, bundledBinaryURL: URL) async -> Bool {
        let localDigest = localSHA256(fileURL: bundledBinaryURL)
        if localDigest.isEmpty {
            logger.warning("provision: failed to digest bundled binary; forcing upload")
            return true
        }
        guard let remoteDigest = remoteNexusSHA256(sshTarget: sshTarget), !remoteDigest.isEmpty else {
            logger.info("provision: remote nexus missing or checksum unavailable; upload required")
            return true
        }
        if remoteDigest == localDigest {
            return false
        }
        logger.info("provision: remote nexus checksum mismatch; upload required")
        return true
    }

    private func localSHA256(fileURL: URL) -> String {
        guard let data = try? Data(contentsOf: fileURL) else { return "" }
        let digest = SHA256.hash(data: data)
        return digest.map { String(format: "%02x", $0) }.joined()
    }

    private func remoteNexusSHA256(sshTarget: String) -> String? {
        let result = try? runSSH(sshTarget: sshTarget, script: """
            set -euo pipefail
            BIN="$HOME/.local/bin/nexus"
            if [ ! -x "$BIN" ]; then
              echo missing
              exit 0
            fi
            if command -v sha256sum >/dev/null 2>&1; then
              sha256sum "$BIN" | awk '{print $1}'
              exit 0
            fi
            if command -v shasum >/dev/null 2>&1; then
              shasum -a 256 "$BIN" | awk '{print $1}'
              exit 0
            fi
            echo missing
            """)
        let output = result?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        if output == "missing" || output.isEmpty {
            return nil
        }
        return output
    }

    /// Uploads the bundled Linux nexus binary to the remote via SSH stdin pipe.
    private func uploadNexusBinary(sshTarget: String, binaryURL: URL, progress: ProgressHandler?) async throws {
        let totalBytes = (try? FileManager.default.attributesOfItem(atPath: binaryURL.path)[.size] as? Int64) ?? 0
        logger.info("provision: uploading \(binaryURL.lastPathComponent, privacy: .public) (\(totalBytes, privacy: .public) bytes) to \(sshTarget, privacy: .public)")
        await progress?(.uploadingBinary(progress: 0.0))

        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/ssh")
        proc.arguments = makeSSHClient(sshTarget: sshTarget).shellArgs(script: """
            set -euo pipefail
            mkdir -p "$HOME/.local/bin"
            TMPFILE=$(mktemp "$HOME/.local/bin/.nexus-upload.XXXXXX")
            cat > "$TMPFILE"
            chmod +x "$TMPFILE"
            mv "$TMPFILE" "$HOME/.local/bin/nexus"
            echo installed
            """)

        let inputPipe = Pipe()
        let outputPipe = Pipe()
        let errPipe = Pipe()
        proc.standardInput = inputPipe
        proc.standardOutput = outputPipe
        proc.standardError = errPipe

        try proc.run()

        // Monitor stderr on a background thread so we can detect auth failures
        // immediately and terminate the process — without this, a bad SSH key causes
        // the write loop below to block forever trying to pipe 17 MB into a dead SSH.
        let stderrCollected = OSAllocatedUnfairLock(initialState: "")
        errPipe.fileHandleForReading.readabilityHandler = { fh in
            let chunk = String(data: fh.availableData, encoding: .utf8) ?? ""
            stderrCollected.withLock { $0 += chunk }
            // SSH exits with status 255 on auth failure; stderr contains "Permission denied".
            // Kill the process immediately so the write loop unblocks.
            if chunk.contains("Permission denied") {
                proc.terminate()
            }
        }

        let fileHandle = try FileHandle(forReadingFrom: binaryURL)
        defer { try? fileHandle.close() }
        let inputFD = inputPipe.fileHandleForWriting.fileDescriptor

        // Ignore SIGPIPE so Darwin.write() returns EPIPE (-1/errno=32) instead of
        // killing this process when SSH exits early (e.g. auth failure).
        // The write loop catches the error and breaks, then waitUntilExit() + stderr
        // inspection tells us whether it was an auth failure or something else.
        signal(SIGPIPE, SIG_IGN)

        let chunkSize = 65536
        var bytesWritten: Int64 = 0
        var pipeWriteFailed = false

        while true {
            // Stop writing if the process has already exited (e.g. auth rejected).
            if !proc.isRunning { break }
            let chunk = fileHandle.readData(ofLength: chunkSize)
            if chunk.isEmpty { break }
            do {
                try Self.writeAll(chunk, to: inputFD)
            } catch {
                pipeWriteFailed = true
                logger.error("provision: upload pipe write failed: \(error.localizedDescription, privacy: .public)")
                break
            }
            bytesWritten += Int64(chunk.count)
            if totalBytes > 0 {
                let pct = Double(bytesWritten) / Double(totalBytes)
                await progress?(.uploadingBinary(progress: pct))
            }
        }
        inputPipe.fileHandleForWriting.closeFile()
        errPipe.fileHandleForReading.readabilityHandler = nil
        // Synchronously drain any stderr that arrived after the last handler fire
        // but before the process fully exited. Without this, "Permission denied"
        // can be missed and errOut ends up empty, preventing sshAuthFailed detection.
        let remainingStderr = String(data: errPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
        stderrCollected.withLock { $0 += remainingStderr }

        proc.waitUntilExit()
        let errOut = stderrCollected.withLock { $0 }.trimmingCharacters(in: .whitespacesAndNewlines)

        // Auth failure: SSH exits 255 with "Permission denied" in stderr.
        if proc.terminationStatus == 255 && errOut.contains("Permission denied") {
            logger.error("provision: SSH auth failure during upload: \(errOut, privacy: .public)")
            throw ProvisionError.sshAuthFailed(message: errOut)
        }

        let out = String(data: outputPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8)?
            .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""

        guard !pipeWriteFailed, proc.terminationStatus == 0, out.contains("installed") else {
            logger.error("provision: upload failed status=\(proc.terminationStatus, privacy: .public) stderr=\(errOut, privacy: .public)")
            let fallback = pipeWriteFailed ? "upload stream closed by remote (broken pipe)" : "exit \(proc.terminationStatus)"
            throw ProvisionError.uploadFailed(message: errOut.isEmpty ? fallback : errOut)
        }

        logger.info("provision: upload complete (\(bytesWritten, privacy: .public) bytes)")
        await progress?(.uploadingBinary(progress: 1.0))
    }

    private static func writeAll(_ data: Data, to fd: Int32) throws {
        try data.withUnsafeBytes { rawBuffer in
            guard let base = rawBuffer.baseAddress else { return }
            var sent = 0
            while sent < rawBuffer.count {
                let ptr = base.advanced(by: sent)
                let written = Darwin.write(fd, ptr, rawBuffer.count - sent)
                if written > 0 {
                    sent += written
                    continue
                }
                if written == 0 { break }
                let code = errno
                if code == EINTR { continue }
                throw ProvisionError.uploadFailed(message: "upload write failed errno=\(code)")
            }
        }
    }

    private func stopDaemonIfRunning(sshTarget: String) async throws {
        _ = try runSSH(sshTarget: sshTarget, script: """
            set -euo pipefail
            export PATH="$HOME/.local/bin:$PATH"
            "$HOME/.local/bin/nexus" daemon stop >/dev/null 2>&1 || true
            """)
        logger.info("provision: stopped running daemon to apply updated binary")
    }

    /// Launch `nexus daemon start --json` on the remote host and stream phase events.
    ///
    /// Phase events are emitted as JSON lines to stdout while the process runs:
    /// `{"phase":"preflight","status":"ok","message":"KVM accessible"}`
    ///
    /// `nexus daemon start` self-daemonizes: it runs bootstrap synchronously
    /// (streaming JSON events), then re-execs the daemon in the background and
    /// exits once the socket is ready.  The SSH session ends naturally at that
    /// point, so no nohup/disown is needed.
    private func startDaemonWithPhaseEvents(sshTarget: String, progress: ProgressHandler?) async throws {
        let remotePort = profile.port

        let cmd = """
            set -euo pipefail
            export PATH="$HOME/.local/bin:$PATH"
            NEXUS="$HOME/.local/bin/nexus"

            # Fast path: daemon already healthy.
            if curl -sf --max-time 2 http://127.0.0.1:\(remotePort)/healthz >/dev/null 2>&1; then
              echo '{"phase":"daemon-launch","status":"ok","message":"already-running"}'
              exit 0
            fi

            # Run bootstrap + self-daemonize.
            # nexus daemon start streams JSON phase events to stdout, re-execs the
            # daemon in the background, and exits once the socket is ready (~10s).
            exec "$NEXUS" daemon start \\
              --json \\
              --ready-timeout 180s \\
              --network --bind 127.0.0.1 --port \(remotePort)
            """

        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/ssh")
        proc.arguments = makeSSHClient(sshTarget: sshTarget).shellArgs(script: cmd)

        let outPipe = Pipe()
        let errPipe = Pipe()
        proc.standardOutput = outPipe
        proc.standardError = errPipe

        try proc.run()
        let stdoutTask = Task { [logger] () -> String in
            let raw = await Self.readProcessLines(from: outPipe.fileHandleForReading) { line in
                guard let event = Self.parsePhaseEvent(line) else { return }
                logger.info("provision: phase \(event.phase, privacy: .public) \(event.status, privacy: .public) \(event.message, privacy: .public)")
                let msg = event.message.isEmpty ? event.phase : "\(event.phase): \(event.message)"
                await progress?(.bootstrapPhase(phase: event.phase, message: msg))
            }
            return raw
        }
        let stderrTask = Task {
            await Self.readProcessLines(from: errPipe.fileHandleForReading) { _ in }
        }

        proc.waitUntilExit()
        let rawOut = await stdoutTask.value
        let rawErr = await stderrTask.value

        let errorPhase = rawOut
            .components(separatedBy: "\n")
            .compactMap { Self.parsePhaseEvent($0.trimmingCharacters(in: .whitespacesAndNewlines)) }
            .first(where: { $0.status == "error" })
            .map { $0.message.isEmpty ? $0.phase : $0.message }

        if let errMsg = errorPhase {
            throw ProvisionError.daemonStartFailed(message: errMsg)
        }

        if proc.terminationStatus != 0 {
            let errMsg = rawErr.trimmingCharacters(in: .whitespacesAndNewlines)
            logger.error("provision: daemon start failed status=\(proc.terminationStatus, privacy: .public) stderr=\(errMsg, privacy: .public)")
            throw ProvisionError.daemonStartFailed(message: errMsg.isEmpty ? "daemon start exited \(proc.terminationStatus)" : errMsg)
        }
    }

    private struct PhaseEvent: Decodable {
        let phase: String
        let status: String
        let message: String

        enum CodingKeys: String, CodingKey {
            case phase, status, message
        }
        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            phase = try c.decode(String.self, forKey: .phase)
            status = try c.decode(String.self, forKey: .status)
            message = (try? c.decode(String.self, forKey: .message)) ?? ""
        }
    }

    private nonisolated static func parsePhaseEvent(_ line: String) -> PhaseEvent? {
        guard let data = line.data(using: .utf8),
              let event = try? JSONDecoder().decode(PhaseEvent.self, from: data) else {
            return nil
        }
        return event
    }

    private nonisolated static func readProcessLines(
        from handle: FileHandle,
        onLine: @Sendable (String) async -> Void
    ) async -> String {
        var raw = ""
        var pending = ""
        while true {
            let data = handle.availableData
            if data.isEmpty { break }
            let chunk = String(decoding: data, as: UTF8.self)
            raw += chunk
            pending += chunk
            while let newline = pending.firstIndex(of: "\n") {
                let line = String(pending[..<newline]).trimmingCharacters(in: .whitespacesAndNewlines)
                pending = String(pending[pending.index(after: newline)...])
                if !line.isEmpty {
                    await onLine(line)
                }
            }
        }
        let trailing = pending.trimmingCharacters(in: .whitespacesAndNewlines)
        if !trailing.isEmpty {
            await onLine(trailing)
        }
        return raw
    }

    /// Poll the daemon's healthz endpoint via SSH until it responds 200 or we time out.
    private func waitForDaemon(sshTarget: String, progress: ProgressHandler?, timeout: TimeInterval = 120) async throws {
        let deadline = Date().addingTimeInterval(timeout)
        var attempt = 0

        while Date() < deadline {
            attempt += 1
            await progress?(.waitingForDaemon(attempt: attempt))

            if await isDaemonHealthy(sshTarget: sshTarget) {
                logger.info("provision: daemon healthy after \(attempt, privacy: .public) poll attempts")
                return
            }

            let delay = min(Double(attempt) * 0.5, 3.0)
            try await Task.sleep(nanoseconds: UInt64(delay * 1_000_000_000))
        }

        throw ProvisionError.daemonReadyTimeout(seconds: timeout)
    }

    // MARK: - Binary bundling

    private func bundledLinuxBinary() -> URL? {
        let candidates = ["nexus-linux-amd64", "nexus-linux-arm64"]
        for name in candidates {
            if let url = Bundle.main.url(forResource: name, withExtension: nil) {
                return url
            }
        }
        for name in candidates {
            let devPath = URL(fileURLWithPath: #filePath)
                .deletingLastPathComponent()
                .deletingLastPathComponent()
                .deletingLastPathComponent()
                .deletingLastPathComponent()
                .appendingPathComponent("packages/nexus-swift/Resources/\(name)")
            if FileManager.default.fileExists(atPath: devPath.path) {
                logger.info("provision: using dev binary at \(devPath.path, privacy: .public)")
                return devPath
            }
        }
        return nil
    }

    // MARK: - SSH helpers

    private func makeSSHClient(sshTarget: String) -> SSHClientArgs {
        let client = SSHClientArgs(profile: profile, scopedPaths: sshScopedPaths)
        AppLifecycleLog.info("provision", "ssh \(client.logDescription)")
        logger.info("provision: SSH command: ssh \(client.commandArgs(remoteCommand: ["<cmd>"]).joined(separator: " "), privacy: .public)")
        return client
    }

    @discardableResult
    private func runSSH(sshTarget: String, script: String) throws -> String {
        let client = makeSSHClient(sshTarget: sshTarget)
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/ssh")
        proc.arguments = client.shellArgs(script: script)
        let outPipe = Pipe()
        let errPipe = Pipe()
        proc.standardOutput = outPipe
        proc.standardError = errPipe
        try proc.run()
        proc.waitUntilExit()
        guard proc.terminationStatus == 0 else {
            let err = String(data: errPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8)?
                .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            // SSH exit 255 with "Permission denied" indicates authentication failure.
            // Distinguish this from other failures so callers can bail immediately.
            if proc.terminationStatus == 255 && err.contains("Permission denied") {
                throw ProvisionError.sshAuthFailed(message: err)
            }
            throw ProvisionError.sshFailed(command: script, message: err.isEmpty ? "exit \(proc.terminationStatus)" : err)
        }
        let out = String(data: outPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
        return out
    }
}

// MARK: - Errors

public enum ProvisionError: Error, LocalizedError, Sendable {
    case noSSHTarget
    case sshIdentityRequired
    case bundledBinaryMissing
    case uploadFailed(message: String)
    case daemonStartFailed(message: String)
    case daemonReadyTimeout(seconds: TimeInterval)
    case sshFailed(command: String, message: String)
    /// SSH authentication failed (wrong key, key not accepted). User must fix profile.
    case sshAuthFailed(message: String)

    public var errorDescription: String? {
        switch self {
        case .noSSHTarget:
            return "No SSH target configured."
        case .sshIdentityRequired:
            return "SSH identity key is required for sandboxed app connection."
        case .bundledBinaryMissing:
            return "Nexus Linux binary not found in app bundle. Please reinstall the app."
        case .uploadFailed(let msg):
            return "Failed to upload nexus binary to remote host: \(msg)"
        case .daemonStartFailed(let msg):
            return "Failed to start nexus daemon: \(msg)"
        case .daemonReadyTimeout(let secs):
            return "Nexus daemon did not become ready within \(Int(secs)) seconds."
        case .sshFailed(_, let msg):
            return "SSH command failed: \(msg)"
        case .sshAuthFailed(let msg):
            return "SSH authentication failed — check your SSH key in Settings. \(msg)"
        }
    }
}
