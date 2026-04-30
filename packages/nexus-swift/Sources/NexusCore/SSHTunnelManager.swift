import Foundation
import os

public actor SSHTunnelManager {
    public enum State: Sendable {
        case idle
        case connecting
        case connected
        case failed(any Error)
    }

    private let profile: DaemonProfile
    private var process: Process?
    private var _state: State = .idle
    private var _localPort: Int?
    private var restartTask: Task<Void, Never>?
    private var stderrPipe: Pipe?
    private var stdoutPipe: Pipe?
    private var activeScopedPaths: SSHSecurityScopedPaths = .empty
    private let logger = Logger(subsystem: "com.nexus.NexusApp", category: "SSHTunnel")

    public init(profile: DaemonProfile) {
        self.profile = profile
    }

    public var state: State { _state }
    public var localPort: Int? { _localPort }

    public func start() async throws -> Int {
        _state = .connecting
        AppLifecycleLog.info("ssh-tunnel", "start target=\(profile.sshTarget ?? "") daemonPort=\(profile.port)")
        let maxAttempts = 5
        var lastError: Error = TunnelError.portAllocation(operation: "start", errnoCode: nil)

        for attempt in 1...maxAttempts {
            do {
                let port = try allocateLocalPort()
                _localPort = port
                let resolvedPaths = resolveScopedPaths()
                let configPath = resolvedPaths.configPath ?? existingSSHConfigPath()
                let identityPath = resolvedPaths.identityPath
                guard let identityPath, !identityPath.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
                    throw TunnelError.identityRequired
                }
                let configModes: [String?] = configPath == nil ? [nil] : [configPath, nil]
                var connected = false

                for mode in configModes {
                    do {
                        try launchSSH(localPort: port, configPath: mode, identityPath: identityPath)
                        try await waitForHealthz(localPort: port)
                        _state = .connected
                        AppLifecycleLog.info("ssh-tunnel", "start success localPort=\(port)")
                        startRestartLoop(localPort: port)
                        connected = true
                        break
                    } catch {
                        lastError = error
                        AppLifecycleLog.warn(
                            "ssh-tunnel",
                            "launch mode failed config=\(mode ?? "<default>"): \(error.localizedDescription)"
                        )
                        process?.terminate()
                        process = nil
                        clearPipes()
                    }
                }
                if connected { return port }
                throw lastError
            } catch {
                lastError = error
                logger.error("tunnel start attempt \(attempt, privacy: .public) failed: \(error.localizedDescription, privacy: .public)")
                AppLifecycleLog.warn("ssh-tunnel", "attempt \(attempt) failed: \(error.localizedDescription)")
                process?.terminate()
                process = nil
                clearPipes()

                if case TunnelError.noTarget = error {
                    break
                }
                // SSH process died immediately — auth failure or host rejected connection.
                // Retrying won't help and spawns multiple SSH subprocesses, burning CPU.
                if case TunnelError.processDied = error {
                    break
                }
                if attempt < maxAttempts {
                    let delay = UInt64(attempt) * 300_000_000
                    try? await Task.sleep(nanoseconds: delay)
                }
            }
        }

        _state = .failed(lastError)
        AppLifecycleLog.error("ssh-tunnel", "start failed after retries: \(lastError.localizedDescription)")
        throw lastError
    }

    /// Fetches the daemon token from the remote host via SSH.
    /// Calls `<remoteBin> daemon token` on the remote — the running daemon is the
    /// single source of truth for the auth token.
    public func fetchRemoteToken() async throws -> String {
        guard let sshTarget = profile.sshTarget, !sshTarget.isEmpty else {
            throw TunnelError.noTarget
        }

        let remoteBin = "/home/newman/.local/bin/nexus"
        let resolvedPaths = resolveScopedPaths()
        let configPath = resolvedPaths.configPath ?? existingSSHConfigPath()
        guard let identityPath = resolvedPaths.identityPath,
              !identityPath.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            throw TunnelError.identityRequired
        }
        let token = try runSSH(
            sshTarget: sshTarget,
            command: [remoteBin, "daemon", "token"],
            configPath: configPath,
            identityPath: identityPath
        )
        if token.isEmpty { throw TunnelError.tokenFetchFailed }
        return token
    }

    private func runSSH(sshTarget: String, command: [String], configPath: String?, identityPath: String?) throws -> String {
        let sshPort = profile.sshPort ?? 22
        var args = [
            "-p", "\(sshPort)",
            "-o", "BatchMode=yes",
            "-o", "StrictHostKeyChecking=no",
            "-o", "UserKnownHostsFile=/dev/null",
            "-o", "GlobalKnownHostsFile=/dev/null"
        ]
        if let configPath {
            args.insert(contentsOf: ["-F", configPath], at: 0)
        }
        if let identity = identityPath, !identity.isEmpty {
            args += ["-i", identity]
        }
        args += [sshTarget] + command
        AppLifecycleLog.info(
            "ssh-tunnel",
            "token-fetch target=\(sshTarget) port=\(sshPort) config=\(configPath ?? "<default>") identity=\(identityPath?.isEmpty == false ? "set" : "unset")"
        )

        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/ssh")
        proc.arguments = args
        let pipe = Pipe()
        proc.standardOutput = pipe
        let errPipe = Pipe()
        proc.standardError = errPipe
        try proc.run()
        proc.waitUntilExit()
        let stderrData = errPipe.fileHandleForReading.readDataToEndOfFile()
        if !stderrData.isEmpty, let stderrStr = String(data: stderrData, encoding: .utf8), !stderrStr.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            logger.warning("token-fetch ssh stderr: \(stderrStr, privacy: .public)")
        }
        guard proc.terminationStatus == 0 else {
            throw TunnelError.tokenFetchFailed
        }
        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        let output = String(data: data, encoding: .utf8)?
            .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return output
    }

    public func stop() async {
        AppLifecycleLog.info("ssh-tunnel", "stop")
        restartTask?.cancel()
        restartTask = nil
        process?.terminate()
        process = nil
        clearPipes()
        SSHSecurityScope.stop(activeScopedPaths)
        activeScopedPaths = .empty
        _state = .idle
    }

    private func clearPipes() {
        stdoutPipe?.fileHandleForReading.readabilityHandler = nil
        stdoutPipe = nil
        stderrPipe?.fileHandleForReading.readabilityHandler = nil
        stderrPipe = nil
    }

    private func allocateLocalPort() throws -> Int {
        let sock = socket(AF_INET, SOCK_STREAM, 0)
        guard sock >= 0 else {
            let err = errno
            logger.error("allocateLocalPort socket() failed errno=\(err, privacy: .public)")
            throw TunnelError.portAllocation(operation: "socket", errnoCode: err)
        }
        defer { close(sock) }

        var reuse: Int32 = 1
        _ = setsockopt(sock, SOL_SOCKET, SO_REUSEADDR, &reuse, socklen_t(MemoryLayout<Int32>.size))

        var addr = sockaddr_in()
#if os(macOS)
        addr.sin_len = UInt8(MemoryLayout<sockaddr_in>.size)
#endif
        addr.sin_family = sa_family_t(AF_INET)
        addr.sin_port = 0
        addr.sin_addr.s_addr = inet_addr("127.0.0.1")
        let bindResult = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                bind(sock, $0, socklen_t(MemoryLayout<sockaddr_in>.size))
            }
        }
        guard bindResult == 0 else {
            let err = errno
            logger.error("allocateLocalPort bind() failed errno=\(err, privacy: .public)")
            throw TunnelError.portAllocation(operation: "bind", errnoCode: err)
        }
        var len = socklen_t(MemoryLayout<sockaddr_in>.size)
        let nameResult = withUnsafeMutablePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                getsockname(sock, $0, &len)
            }
        }
        guard nameResult == 0 else {
            let err = errno
            logger.error("allocateLocalPort getsockname() failed errno=\(err, privacy: .public)")
            throw TunnelError.portAllocation(operation: "getsockname", errnoCode: err)
        }
        return Int(addr.sin_port.bigEndian)
    }

    private func launchSSH(localPort: Int, configPath: String?, identityPath: String?) throws {
        guard let sshTarget = profile.sshTarget, !sshTarget.isEmpty else {
            throw TunnelError.noTarget
        }
        let remotePort = profile.port
        let sshPort = profile.sshPort ?? 22

        var args = [
            "-N",
            "-o", "ExitOnForwardFailure=yes",
            "-o", "ServerAliveInterval=10",
            "-o", "StrictHostKeyChecking=no",
            "-o", "UserKnownHostsFile=/dev/null",
            "-o", "GlobalKnownHostsFile=/dev/null",
            "-L", "\(localPort):127.0.0.1:\(remotePort)",
            "-p", "\(sshPort)"
        ]
        if let configPath {
            args.insert(contentsOf: ["-F", configPath], at: 0)
        }
        if let identity = identityPath, !identity.isEmpty {
            args += ["-i", identity]
        }
        args.append(sshTarget)
        AppLifecycleLog.info(
            "ssh-tunnel",
            "launch target=\(sshTarget) sshPort=\(sshPort) localPort=\(localPort) remotePort=\(remotePort) config=\(configPath ?? "<default>") identity=\(identityPath?.isEmpty == false ? "set" : "unset")"
        )

        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/ssh")
        proc.arguments = args

        let outPipe = Pipe()
        proc.standardOutput = outPipe
        outPipe.fileHandleForReading.readabilityHandler = { [logger] handle in
            let data = handle.availableData
            guard !data.isEmpty, let str = String(data: data, encoding: .utf8) else { return }
            for line in str.components(separatedBy: .newlines) {
                let trimmed = line.trimmingCharacters(in: .whitespacesAndNewlines)
                if !trimmed.isEmpty { logger.debug("ssh stdout: \(trimmed, privacy: .public)") }
            }
        }
        self.stdoutPipe = outPipe

        let errPipe = Pipe()
        proc.standardError = errPipe
        errPipe.fileHandleForReading.readabilityHandler = { [logger] handle in
            let data = handle.availableData
            guard !data.isEmpty, let str = String(data: data, encoding: .utf8) else { return }
            for line in str.components(separatedBy: .newlines) {
                let trimmed = line.trimmingCharacters(in: .whitespacesAndNewlines)
                if !trimmed.isEmpty { logger.warning("ssh stderr: \(trimmed, privacy: .public)") }
            }
        }
        self.stderrPipe = errPipe

        try proc.run()
        self.process = proc
    }

    private func waitForHealthz(localPort: Int) async throws {
        let url = URL(string: "http://127.0.0.1:\(localPort)/healthz")!
        let deadline = Date().addingTimeInterval(30)
        let startTime = Date()
        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = 1
        let session = URLSession(configuration: config)
        var attempt = 0
        while Date() < deadline {
            if let proc = process, !proc.isRunning {
                logger.error("ssh process died on attempt \(attempt, privacy: .public)")
                throw TunnelError.processDied
            }
            attempt += 1
            var statusCode = "error"
            if let (_, response) = try? await session.data(from: url),
               let code = (response as? HTTPURLResponse)?.statusCode {
                statusCode = "\(code)"
                logger.debug("healthz attempt \(attempt, privacy: .public) elapsed=\(Date().timeIntervalSince(startTime), privacy: .public)s status=\(statusCode, privacy: .public)")
                if code == 200 {
                    logger.info("healthz ok on attempt \(attempt, privacy: .public) elapsed=\(Date().timeIntervalSince(startTime), privacy: .public)s")
                    return
                }
            } else {
                logger.debug("healthz attempt \(attempt, privacy: .public) elapsed=\(Date().timeIntervalSince(startTime), privacy: .public)s status=\(statusCode, privacy: .public)")
            }
            try await Task.sleep(nanoseconds: 300_000_000)
        }
        logger.error("healthz timeout after \(attempt, privacy: .public) attempts")
        throw TunnelError.timeout
    }

    private func startRestartLoop(localPort: Int) {
        restartTask = Task { [weak self] in
            var backoff: UInt64 = 1_000_000_000
            while !Task.isCancelled {
                guard let self = self else { return }
                let proc = await self.process
                if let proc, proc.isRunning {
                    try? await Task.sleep(nanoseconds: 2_000_000_000)
                    continue
                }
                await self.setStateFailed()
                try? await Task.sleep(nanoseconds: backoff)
                backoff = min(backoff * 2, 30_000_000_000)
                if Task.isCancelled { return }
                do {
                    try await self.relaunchSSH(localPort: localPort)
                } catch {
                    continue
                }
            }
        }
    }

    private func setStateFailed() {
        _state = .failed(TunnelError.processDied)
    }

    private func relaunchSSH(localPort: Int) throws {
        process?.terminate()
        process = nil
        clearPipes()
        let resolvedPaths = resolveScopedPaths()
        let configPath = resolvedPaths.configPath ?? existingSSHConfigPath()
        try launchSSH(localPort: localPort, configPath: configPath, identityPath: resolvedPaths.identityPath)
        _state = .connected
    }

    private func resolveScopedPaths() -> SSHSecurityScopedPaths {
        SSHSecurityScope.stop(activeScopedPaths)
        let resolved = SSHSecurityScope.resolve(profile: profile, category: "ssh-tunnel")
        activeScopedPaths = resolved
        return resolved
    }
    private func existingSSHConfigPath() -> String? {
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        let candidates = [
            "\(home)/.config/nexus/ssh/nexus.ssh.config",
            "\(home)/.ssh/config",
        ]
        if let matched = candidates.first(where: { FileManager.default.fileExists(atPath: $0) }) {
            AppLifecycleLog.info("ssh-tunnel", "using ssh config path=\(matched)")
            return matched
        }
        AppLifecycleLog.info("ssh-tunnel", "using default ssh config resolution")
        return nil
    }
}

public enum TunnelError: Error, LocalizedError {
    case portAllocation(operation: String, errnoCode: Int32?)
    case noTarget
    case identityRequired
    case processDied
    case timeout
    case tokenFetchFailed

    public var errorDescription: String? {
        switch self {
        case let .portAllocation(operation, errnoCode):
            if let errnoCode {
                if let cString = strerror(errnoCode) {
                    let message = String(cString: cString)
                    return "Failed to allocate local port (\(operation), errno=\(errnoCode): \(message))"
                }
                return "Failed to allocate local port (\(operation), errno=\(errnoCode))"
            }
            return "Failed to allocate local port"
        case .noTarget: return "No SSH target configured"
        case .identityRequired: return "SSH identity key is required for sandboxed app connection"
        case .processDied: return "SSH process exited unexpectedly"
        case .timeout: return "Tunnel did not become ready within 15 seconds"
        case .tokenFetchFailed: return "Could not fetch daemon token from remote host"
        }
    }
}
