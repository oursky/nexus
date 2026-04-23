import Foundation
import os

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

        await progress?(.checkingHost)

        // ── 1. Check daemon health first (fastest path for already-setup hosts) ──
        if await isDaemonHealthy(sshTarget: sshTarget) {
            logger.info("provision: daemon already healthy on \(sshTarget, privacy: .public)")
            await progress?(.ready)
            return
        }

        // ── 2. Ensure nexus binary is present ──
        let needsUpload = await !nexusBinaryPresent(sshTarget: sshTarget)
        if needsUpload {
            logger.info("provision: nexus binary missing on \(sshTarget, privacy: .public); uploading")
            try await uploadNexusBinary(sshTarget: sshTarget, progress: progress)
        } else {
            logger.info("provision: nexus binary already present on \(sshTarget, privacy: .public)")
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
        let result = try? runSSH(sshTarget: sshTarget, command: """
            curl -sf --max-time 2 http://127.0.0.1:\(profile.port)/healthz >/dev/null 2>&1 && echo ok || echo no
            """)
        return result?.trimmingCharacters(in: .whitespacesAndNewlines) == "ok"
    }

    private func nexusBinaryPresent(sshTarget: String) async -> Bool {
        let result = try? runSSH(sshTarget: sshTarget, command: """
            test -x "$HOME/.local/bin/nexus" && echo present || echo missing
            """)
        return result?.trimmingCharacters(in: .whitespacesAndNewlines) == "present"
    }

    /// Uploads the bundled Linux nexus binary to the remote via SSH stdin pipe.
    private func uploadNexusBinary(sshTarget: String, progress: ProgressHandler?) async throws {
        guard let binaryURL = bundledLinuxBinary() else {
            throw ProvisionError.bundledBinaryMissing
        }

        let totalBytes = (try? FileManager.default.attributesOfItem(atPath: binaryURL.path)[.size] as? Int64) ?? 0
        logger.info("provision: uploading \(binaryURL.lastPathComponent, privacy: .public) (\(totalBytes, privacy: .public) bytes) to \(sshTarget, privacy: .public)")
        await progress?(.uploadingBinary(progress: 0.0))

        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/ssh")
        proc.arguments = buildSSHArgs(sshTarget: sshTarget) + [
            sshTarget,
            """
            set -euo pipefail
            mkdir -p "$HOME/.local/bin"
            TMPFILE=$(mktemp "$HOME/.local/bin/.nexus-upload.XXXXXX")
            cat > "$TMPFILE"
            chmod +x "$TMPFILE"
            mv "$TMPFILE" "$HOME/.local/bin/nexus"
            echo installed
            """,
        ]

        let inputPipe = Pipe()
        let outputPipe = Pipe()
        let errPipe = Pipe()
        proc.standardInput = inputPipe
        proc.standardOutput = outputPipe
        proc.standardError = errPipe

        try proc.run()

        let fileHandle = try FileHandle(forReadingFrom: binaryURL)
        defer { try? fileHandle.close() }

        let chunkSize = 65536
        var bytesWritten: Int64 = 0

        while true {
            let chunk = fileHandle.readData(ofLength: chunkSize)
            if chunk.isEmpty { break }
            inputPipe.fileHandleForWriting.write(chunk)
            bytesWritten += Int64(chunk.count)
            if totalBytes > 0 {
                let pct = Double(bytesWritten) / Double(totalBytes)
                await progress?(.uploadingBinary(progress: pct))
            }
        }
        inputPipe.fileHandleForWriting.closeFile()

        proc.waitUntilExit()
        let out = String(data: outputPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8)?
            .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""

        guard proc.terminationStatus == 0, out.contains("installed") else {
            let errOut = String(data: errPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
            logger.error("provision: upload failed status=\(proc.terminationStatus, privacy: .public) stderr=\(errOut, privacy: .public)")
            throw ProvisionError.uploadFailed(message: errOut.isEmpty ? "exit \(proc.terminationStatus)" : errOut)
        }

        logger.info("provision: upload complete (\(bytesWritten, privacy: .public) bytes)")
        await progress?(.uploadingBinary(progress: 1.0))
    }

    /// Launch `nexus daemon start --json` on the remote host and parse phase events.
    ///
    /// Phase events from the rootless bootstrap are emitted as JSON lines:
    /// `{"phase":"preflight","status":"ok","message":"KVM accessible"}`
    ///
    /// The daemon runs in background (nohup &) after the bootstrap phases complete.
    private func startDaemonWithPhaseEvents(sshTarget: String, progress: ProgressHandler?) async throws {
        let remotePort = profile.port

        let cmd = """
            set -euo pipefail
            export PATH="$HOME/.local/bin:$PATH"
            NEXUS="$HOME/.local/bin/nexus"
            LOG_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/nexus"
            mkdir -p "$LOG_DIR"

            # ── SSH agent ──────────────────────────────────────────────────────
            AGENT_ENV="$LOG_DIR/ssh-agent.env"
            if [ -f "$AGENT_ENV" ]; then
              . "$AGENT_ENV" 2>/dev/null || true
            fi
            if [ -z "${SSH_AUTH_SOCK:-}" ] || ! ssh-add -l >/dev/null 2>&1; then
              eval "$(ssh-agent -s)" >/dev/null
              printf 'SSH_AUTH_SOCK=%s\\nexport SSH_AUTH_SOCK\\nSSH_AGENT_PID=%s\\nexport SSH_AGENT_PID\\n' \\
                "$SSH_AUTH_SOCK" "$SSH_AGENT_PID" > "$AGENT_ENV"
            fi
            export SSH_AUTH_SOCK SSH_AGENT_PID

            # ── Guard: already running? ────────────────────────────────────────
            if curl -sf --max-time 2 http://127.0.0.1:\(remotePort)/healthz >/dev/null 2>&1; then
              echo '{"phase":"daemon-launch","status":"ok","message":"already-running"}'
              exit 0
            fi

            # ── Run rootless bootstrap + launch daemon ─────────────────────────
            SSH_AUTH_SOCK="$SSH_AUTH_SOCK" \\
              nohup "$NEXUS" daemon start \\
                --json \\
                --network --bind 127.0.0.1 --port \(remotePort) \\
                >>"$LOG_DIR/provision.log" 2>&1 &
            disown
            echo '{"phase":"daemon-launch","status":"start","message":"started"}'
            """

        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/ssh")
        proc.arguments = buildSSHArgs(sshTarget: sshTarget) + [sshTarget, cmd]

        let outPipe = Pipe()
        let errPipe = Pipe()
        proc.standardOutput = outPipe
        proc.standardError = errPipe

        try proc.run()
        proc.waitUntilExit()

        let rawOut = String(data: outPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
        let rawErr = String(data: errPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""

        // Parse JSON phase events and report progress
        var encounteredError: String?
        for line in rawOut.components(separatedBy: "\n") {
            let trimmed = line.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !trimmed.isEmpty else { continue }
            if let event = parsePhaseEvent(trimmed) {
                logger.info("provision: phase \(event.phase, privacy: .public) \(event.status, privacy: .public) \(event.message, privacy: .public)")
                let msg = event.message.isEmpty ? event.phase : "\(event.phase): \(event.message)"
                await progress?(.bootstrapPhase(phase: event.phase, message: msg))
                if event.status == "error" {
                    encounteredError = "provision_error.remote_daemon_start: \(event.phase) failed: \(event.message)"
                }
            }
        }

        if proc.terminationStatus != 0 {
            let errMsg = rawErr.trimmingCharacters(in: .whitespacesAndNewlines)
            logger.error("provision: daemon start failed status=\(proc.terminationStatus, privacy: .public) stderr=\(errMsg, privacy: .public)")
            throw ProvisionError.daemonStartFailed(message: errMsg.isEmpty ? "exit \(proc.terminationStatus)" : errMsg)
        }

        if let err = encounteredError {
            throw ProvisionError.daemonStartFailed(message: err)
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

    private func parsePhaseEvent(_ line: String) -> PhaseEvent? {
        guard let data = line.data(using: .utf8),
              let event = try? JSONDecoder().decode(PhaseEvent.self, from: data) else {
            return nil
        }
        return event
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

    private func buildSSHArgs(sshTarget: String) -> [String] {
        let sshPort = profile.sshPort ?? 22
        var args = [
            "-p", "\(sshPort)",
            "-F", "/dev/null",
            "-o", "BatchMode=yes",
            "-o", "StrictHostKeyChecking=no",
            "-o", "UserKnownHostsFile=/dev/null",
            "-o", "GlobalKnownHostsFile=/dev/null",
            "-o", "ConnectTimeout=10",
        ]
        if let identity = profile.sshIdentity, !identity.isEmpty {
            args += ["-i", identity]
        }
        return args
    }

    @discardableResult
    private func runSSH(sshTarget: String, command: String) throws -> String {
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/ssh")
        proc.arguments = buildSSHArgs(sshTarget: sshTarget) + [sshTarget, command]
        let outPipe = Pipe()
        let errPipe = Pipe()
        proc.standardOutput = outPipe
        proc.standardError = errPipe
        try proc.run()
        proc.waitUntilExit()
        guard proc.terminationStatus == 0 else {
            let err = String(data: errPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8)?
                .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            throw ProvisionError.sshFailed(command: command, message: err.isEmpty ? "exit \(proc.terminationStatus)" : err)
        }
        let out = String(data: outPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
        return out
    }
}

// MARK: - Errors

public enum ProvisionError: Error, LocalizedError, Sendable {
    case noSSHTarget
    case bundledBinaryMissing
    case uploadFailed(message: String)
    case daemonStartFailed(message: String)
    case daemonReadyTimeout(seconds: TimeInterval)
    case sshFailed(command: String, message: String)

    public var errorDescription: String? {
        switch self {
        case .noSSHTarget:
            return "No SSH target configured."
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
        }
    }
}
