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
    private let agentSocket: String?
    private let logger = Logger(subsystem: "com.oursky.nexus", category: "RemoteProvisioner")
    private var sshScopedPaths: SSHSecurityScopedPaths = .empty

    /// Probe SSH connectivity only — no daemon check, no upload, no tunnel.
    /// Runs `echo ok` over SSH and returns true on success.
    /// Throws a descriptive error (with raw SSH stderr) if auth fails or host is unreachable.
    public static func probeSSH(profile: DaemonProfile, agentSocket: String? = nil) async throws -> Bool {
        let provisioner = RemoteProvisioner(profile: profile, agentSocket: agentSocket)
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

    public init(profile: DaemonProfile, agentSocket: String? = nil) {
        self.profile = profile
        self.agentSocket = agentSocket
    }

    /// Ensure the remote host has a running nexus daemon.
    ///
    /// Flow:
    ///   1. Check if daemon is already healthy (fast-path)
    ///   2. Check if nexus binary exists and matches bundled binary
    ///   3. If missing or mismatched: upload bundled Linux binary
    ///   4. Start daemon with phase event streaming
    ///   5. Poll healthz until ready
    public func provision(progress: ProgressHandler? = nil) async throws {
        guard let sshTarget = profile.sshTarget?.trimmingCharacters(in: .whitespacesAndNewlines),
              !sshTarget.isEmpty else {
            throw ProvisionError.noSSHTarget
        }

        sshScopedPaths = SSHSecurityScope.resolve(profile: profile, category: "provision")
        defer {
            SSHSecurityScope.stop(sshScopedPaths)
            sshScopedPaths = .empty
        }

        await progress?(.checkingHost)
        writeProvisionLog(step: "checking-host", message: "SSH target: \(sshTarget)")

        // Fast path: daemon already healthy.
        if await isDaemonHealthy(sshTarget: sshTarget) {
            logger.info("provision: daemon already healthy")
            await progress?(.ready)
            return
        }

        // Resolve bundled binary.
        guard let binaryURL = bundledLinuxBinary() else {
            throw ProvisionError.bundledBinaryMissing
        }

        // Upload if missing or checksum mismatch.
        if await binaryNeedsUpload(sshTarget: sshTarget, bundledBinaryURL: binaryURL) {
            try await uploadNexusBinary(sshTarget: sshTarget, binaryURL: binaryURL, progress: progress)
        } else {
            logger.info("provision: remote binary matches bundled; skipping upload")
        }

        // Stop any running daemon so the new binary takes effect.
        try await stopDaemonIfRunning(sshTarget: sshTarget)

        // Start daemon and stream bootstrap phases.
        await progress?(.startingDaemon)
        writeProvisionLog(step: "starting-daemon", message: "Launching daemon with phase events")
        let phaseTracker = PhaseTracker()
        try await startDaemonWithPhaseEvents(sshTarget: sshTarget, progress: progress, phaseTracker: phaseTracker)

        // Wait for healthz.
        try await waitForDaemon(sshTarget: sshTarget, progress: progress, phaseTracker: phaseTracker)
        await progress?(.ready)
        writeProvisionLog(step: "ready", message: "Daemon healthy and ready")
    }

    private class PhaseTracker {
        private var observedPhases: Set<String> = []
        private var lastPhaseTime: Date = Date()
        private(set) var lastPhase: String = ""

        func observe(phase: String) {
            observedPhases.insert(phase)
            lastPhase = phase
            lastPhaseTime = Date()
        }

        var hasObservedRootfsBake: Bool {
            observedPhases.contains("rootfs-bake")
        }

        var timeSinceLastPhase: TimeInterval {
            Date().timeIntervalSince(lastPhaseTime)
        }

        var baseTimeout: TimeInterval {
            // First boot with rootfs bake can take 5-6 minutes total
            // (bake ~40s + daemon launch + prewarm base images + healthz ready)
            hasObservedRootfsBake ? 360 : 60
        }

        var allObservedPhases: [String] {
            Array(observedPhases)
        }
        
        var hasObservedDaemonLaunch: Bool {
            observedPhases.contains("daemon-launch")
        }
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
        writeProvisionLog(step: "uploading-binary", message: "Starting binary upload", progress: 0.0)

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
        var uploadEnv = ProcessInfo.processInfo.environment
        if let sock = agentSocket { uploadEnv["SSH_AUTH_SOCK"] = sock }
        proc.environment = uploadEnv

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
        writeProvisionLog(step: "uploading-binary", message: "Binary upload complete", progress: 1.0)
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
    private func startDaemonWithPhaseEvents(sshTarget: String, progress: ProgressHandler?, phaseTracker: PhaseTracker) async throws {
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
        var daemonEnv = ProcessInfo.processInfo.environment
        if let sock = agentSocket { daemonEnv["SSH_AUTH_SOCK"] = sock }
        proc.environment = daemonEnv

        let outPipe = Pipe()
        let errPipe = Pipe()
        proc.standardOutput = outPipe
        proc.standardError = errPipe

        try proc.run()
        let stdoutTask = Task { [logger, self] () -> String in
            let raw = await Self.readProcessLines(from: outPipe.fileHandleForReading) { line in
                guard let event = Self.parsePhaseEvent(line) else { return }
                logger.info("provision: phase \(event.phase, privacy: .public) \(event.status, privacy: .public) \(event.message, privacy: .public)")
                phaseTracker.observe(phase: event.phase)
                let msg = event.message.isEmpty ? event.phase : "\(event.phase): \(event.message)"
                await progress?(.bootstrapPhase(phase: event.phase, message: msg))
                await self.writeProvisionLog(step: "bootstrap", phase: event.phase, message: event.message)
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
    private func waitForDaemon(sshTarget: String, progress: ProgressHandler?, phaseTracker: PhaseTracker) async throws {
        let timeout = phaseTracker.baseTimeout
        let deadline = Date().addingTimeInterval(timeout)
        var attempt = 0

        while Date() < deadline {
            attempt += 1
            await progress?(.waitingForDaemon(attempt: attempt))
            writeProvisionLog(step: "waiting", message: "Polling healthz", attempt: attempt)

            if await isDaemonHealthy(sshTarget: sshTarget) {
                logger.info("provision: daemon healthy after \(attempt) poll attempts")
                return
            }

            // If no phase has been seen for 60s AND we haven't seen daemon-launch yet, something is wrong.
            // daemon-launch is the last bootstrap phase; after that the daemon just needs time to start.
            if phaseTracker.timeSinceLastPhase > 60 && !phaseTracker.hasObservedDaemonLaunch {
                throw ProvisionError.daemonStalled(phase: phaseTracker.lastPhase)
            }

            let delay = min(Double(attempt) * 0.5, 3.0)
            try await Task.sleep(nanoseconds: UInt64(delay * 1_000_000_000))
        }

        throw ProvisionError.daemonReadyTimeout(seconds: timeout, observedPhases: phaseTracker.allObservedPhases)
    }

    // MARK: - Provision log

    private func writeProvisionLog(step: String, phase: String? = nil, message: String? = nil, progress: Double? = nil, attempt: Int? = nil) {
        let logDir = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first?
            .appendingPathComponent("nexus", isDirectory: true)
        let logURL = logDir?.appendingPathComponent("provision.log")

        guard let url = logURL else { return }

        // Ensure directory exists
        try? FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)

        var entry: [String: Any] = [
            "timestamp": ISO8601DateFormatter().string(from: Date()),
            "step": step
        ]
        if let phase = phase { entry["phase"] = phase }
        if let message = message { entry["message"] = message }
        if let progress = progress { entry["progress"] = progress }
        if let attempt = attempt { entry["attempt"] = attempt }

        guard let data = try? JSONSerialization.data(withJSONObject: entry),
              let line = String(data: data, encoding: .utf8) else { return }

        if let handle = try? FileHandle(forWritingTo: url) {
            handle.seekToEndOfFile()
            handle.write((line + "\n").data(using: .utf8)!)
            handle.closeFile()
        } else {
            try? (line + "\n").write(to: url, atomically: true, encoding: .utf8)
        }
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
                .deletingLastPathComponent()  // NexusCore
                .deletingLastPathComponent()  // Sources
                .deletingLastPathComponent()  // nexus-swift
                .appendingPathComponent("Resources/\(name)")
            if FileManager.default.fileExists(atPath: devPath.path) {
                logger.info("provision: using dev binary at \(devPath.path, privacy: .public)")
                return devPath
            }
        }
        return nil
    }

    // MARK: - SSH helpers

    private func makeSSHClient(sshTarget: String) -> SSHClientArgs {
        let client = SSHClientArgs(profile: profile, scopedPaths: sshScopedPaths, agentSocket: agentSocket)
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
        var env = ProcessInfo.processInfo.environment
        if let sock = agentSocket { env["SSH_AUTH_SOCK"] = sock }
        proc.environment = env
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
    case daemonReadyTimeout(seconds: TimeInterval, observedPhases: [String])
    case daemonStalled(phase: String)
    case sshFailed(command: String, message: String)
    /// SSH authentication failed (wrong key, key not accepted). User must fix profile.
    case sshAuthFailed(message: String)
    /// Provisioning from the Mac app is disabled. Install the binary manually.
    case provisioningDisabled

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
        case .daemonReadyTimeout(let secs, let phases):
            let phasesStr = phases.isEmpty ? "none" : phases.joined(separator: ", ")
            return "Nexus daemon did not become ready within \(Int(secs)) seconds. Observed phases: \(phasesStr)."
        case .daemonStalled(let phase):
            return "Daemon appears stalled at phase '\(phase)' — no progress for 60 seconds."
        case .sshFailed(_, let msg):
            return "SSH command failed: \(msg)"
        case .sshAuthFailed(let msg):
            return "SSH authentication failed — check your SSH key in Settings. \(msg)"
        case .provisioningDisabled:
            return "Automatic provisioning from the Mac app is not supported. Download the nexus binary from https://github.com/oursky/nexus/releases and install it on the remote host manually, then run: nexus daemon start"
        }
    }
}
