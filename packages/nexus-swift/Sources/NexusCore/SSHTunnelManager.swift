import Foundation
import Network
import os

public actor SSHTunnelManager {
    public enum State: Sendable {
        case idle
        case connecting
        case connected
        case failed(any Error)
    }

    // MARK: - SSHTunnelProcess

    /// A private struct that owns an SSH subprocess and its I/O pipes.
    /// Always call `terminate()` before releasing an instance to prevent
    /// fd_monitoring CPU leaks from dangling readabilityHandlers.
    private struct SSHTunnelProcess {
        var process: Process
        var stdoutPipe: Pipe
        var stderrPipe: Pipe

        /// Deterministically clears both readabilityHandlers before terminating
        /// the process. Safe to call multiple times.
        mutating func terminate() {
            stdoutPipe.fileHandleForReading.readabilityHandler = nil
            stderrPipe.fileHandleForReading.readabilityHandler = nil
            process.terminate()
        }

        /// Drain stderr after the process has exited.
        /// No readabilityHandler is set on the control tunnel's errPipe, so this
        /// gets the complete output in one synchronous read.
        func drainStderr() -> String {
            let data = stderrPipe.fileHandleForReading.readDataToEndOfFile()
            return String(data: data, encoding: .utf8)?
                .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        }
    }

    // MARK: - State stream

    public let stateStream: AsyncStream<State>
    private let stateContinuation: AsyncStream<State>.Continuation

    // MARK: - Properties

    private let profile: DaemonProfile
    private var controlTunnel: SSHTunnelProcess?
    private var spotlightTunnels: [Int: SSHTunnelProcess] = [:]   // keyed by remotePort
    private var _state: State = .idle
    private var _localPort: Int?
    private var _reversePort: Int = 0
    private var restartTask: Task<Void, Never>?
    private var activeScopedPaths: SSHSecurityScopedPaths = .empty
    private let logger = Logger(subsystem: "com.nexus.NexusApp", category: "SSHTunnel")

    public init(profile: DaemonProfile) {
        self.profile = profile
        var cont: AsyncStream<State>.Continuation!
        self.stateStream = AsyncStream { cont = $0 }
        self.stateContinuation = cont
    }

    public var state: State { _state }
    public var localPort: Int? { _localPort }

    /// The port on the REMOTE host that tunnels back to this Mac's SSH server (port 22).
    /// Zero if no reverse tunnel has been established.
    public var reversePort: Int { _reversePort }

    // MARK: - setState helper

    private func setState(_ newState: State) {
        _state = newState
        stateContinuation.yield(newState)
    }

    // MARK: - Control tunnel

    public func start() async throws -> Int {
        setState(.connecting)
        AppLifecycleLog.info("ssh-tunnel", "start target=\(profile.sshTarget ?? "") daemonPort=\(profile.port)")
        let maxAttempts = 5
        var lastError: Error = TunnelError.portAllocation(operation: "start", errnoCode: nil)

        for attempt in 1...maxAttempts {
            do {
                let port = try allocateLocalPort()
                _localPort = port
                let rp = try allocateLocalPort()
                _reversePort = rp
                let resolvedPaths = resolveScopedPaths()
                let identityPath = resolvedPaths.identityPath
                guard let identityPath, !identityPath.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
                    throw TunnelError.identityRequired
                }
                // When an explicit identity is provided, always use strict-key mode
                // (SSHClientArgs passes -F /dev/null so ~/.ssh/config is bypassed).
                // Never fall back to a config-based mode — that would allow the agent
                // or config to inject the real key and mask a wrong-key error.
                try launchControlTunnel(localPort: port, reversePort: rp, configPath: nil, identityPath: identityPath)
                try await waitForHealthz(localPort: port)
                setState(.connected)
                AppLifecycleLog.info("ssh-tunnel", "start success localPort=\(port)")
                startRestartLoop(localPort: port)
                return port
            } catch {
                lastError = error
                logger.error("tunnel start attempt \(attempt, privacy: .public) failed: \(error.localizedDescription, privacy: .public)")
                AppLifecycleLog.warn("ssh-tunnel", "attempt \(attempt) failed: \(error.localizedDescription)")
                controlTunnel?.terminate()
                controlTunnel = nil

                if case TunnelError.noTarget = error { break }
                // SSH process died immediately — auth failure or host rejected connection.
                // Retrying won't help and spawns multiple SSH subprocesses, burning CPU.
                if let te = error as? TunnelError, case TunnelError.processDied = te { break }
                if attempt < maxAttempts {
                    let delay = UInt64(attempt) * 300_000_000
                    try? await Task.sleep(nanoseconds: delay)
                }
            }
        }

        setState(.failed(lastError))
        AppLifecycleLog.error("ssh-tunnel", "start failed after retries: \(lastError.localizedDescription)")
        throw lastError
    }

    public func stop() async {
        AppLifecycleLog.info("ssh-tunnel", "stop")
        restartTask?.cancel()
        restartTask = nil
        controlTunnel?.terminate()
        controlTunnel = nil
        stopAllSpotlightTunnels()
        SSHSecurityScope.stop(activeScopedPaths)
        activeScopedPaths = .empty
        setState(.idle)
    }

    // MARK: - Spotlight tunnels

    /// Opens an SSH port-forward from `localPort` on localhost to `remotePort` on the SSH target.
    /// If a tunnel already exists for this localPort that is still running, it is reused.
    /// Waits up to `timeout` seconds for the local port to become connectable before returning.
    @discardableResult
    public func openSpotlightTunnel(localPort: Int, remotePort: Int, timeout: TimeInterval = 5) async throws -> Int {
        if let existing = spotlightTunnels[localPort] {
            if existing.process.isRunning {
                return localPort
            }
            // Stale — clean it up
            var stale = spotlightTunnels.removeValue(forKey: localPort)
            stale?.terminate()
        }

        guard let sshTarget = profile.sshTarget, !sshTarget.isEmpty else {
            throw TunnelError.noTarget
        }
        let resolvedPaths = resolveScopedPaths()
        let configPath = resolvedPaths.configPath ?? existingSSHConfigPath()
        guard let identityPath = resolvedPaths.identityPath,
              !identityPath.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            throw TunnelError.identityRequired
        }

        let tunnel = try launchSpotlightTunnel(
            localPort: localPort,
            remotePort: remotePort,
            sshTarget: sshTarget,
            configPath: configPath,
            identityPath: identityPath
        )
        spotlightTunnels[localPort] = tunnel
        AppLifecycleLog.info("ssh-tunnel", "spotlight opened localPort=\(localPort) remotePort=\(remotePort)")

        // Wait until the local port accepts TCP connections (phase 1: bind probe)
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if !tunnel.process.isRunning {
                let exitCode = tunnel.process.terminationStatus
                AppLifecycleLog.error("ssh-tunnel", "spotlight ssh died exitCode=\(exitCode)")
                throw TunnelError.processDied(exitCode: exitCode, stderr: "")
            }
            if let _ = try? await connectTCP(host: "127.0.0.1", port: localPort) {
                AppLifecycleLog.info("ssh-tunnel", "spotlight localPort=\(localPort) is ready")
                return localPort
            }
            try await Task.sleep(nanoseconds: 100_000_000) // 100ms
        }
        AppLifecycleLog.warn("ssh-tunnel", "spotlight localPort=\(localPort) readiness timeout — proceeding anyway")
        return localPort
    }

    /// Attempts a TCP connection to host:port and returns immediately.
    private func connectTCP(host: String, port: Int) async throws -> Bool {
        return try await withCheckedThrowingContinuation { continuation in
            let conn = NWConnection(host: NWEndpoint.Host(host), port: NWEndpoint.Port(integerLiteral: UInt16(port)), using: .tcp)
            conn.stateUpdateHandler = { state in
                switch state {
                case .ready:
                    conn.cancel()
                    continuation.resume(returning: true)
                case .failed(let error):
                    conn.cancel()
                    continuation.resume(throwing: error)
                case .waiting:
                    conn.cancel()
                    continuation.resume(throwing: TunnelError.noTarget) // port not yet open
                default:
                    break
                }
            }
            conn.start(queue: .global())
        }
    }

    /// Terminates the spotlight tunnel bound to `localPort`, if it exists.
    public func closeSpotlightTunnel(localPort: Int) async {
        guard var tunnel = spotlightTunnels.removeValue(forKey: localPort) else { return }
        tunnel.terminate()
        AppLifecycleLog.info("ssh-tunnel", "spotlight closed localPort=\(localPort)")
    }

    func stopAllSpotlightTunnels() {
        for (localPort, var tunnel) in spotlightTunnels {
            tunnel.terminate()
            AppLifecycleLog.info("ssh-tunnel", "spotlight stopped localPort=\(localPort)")
        }
        spotlightTunnels.removeAll()
    }

    // MARK: - Token fetch

    /// Fetches the daemon token from the remote host via SSH.
    public func fetchRemoteToken() async throws -> String {
        guard let sshTarget = profile.sshTarget, !sshTarget.isEmpty else {
            throw TunnelError.noTarget
        }

        let remoteBin = "~/.local/bin/nexus"
        let resolvedPaths = resolveScopedPaths()
        guard let identityPath = resolvedPaths.identityPath,
              !identityPath.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            throw TunnelError.identityRequired
        }
        let client = SSHClientArgs(profile: profile, scopedPaths: resolvedPaths)
        let token = try runSSH(client: client, command: [remoteBin, "daemon", "token"])
        if token.isEmpty { throw TunnelError.tokenFetchFailed }
        return token
    }

    // MARK: - Private helpers

    private func runSSH(client: SSHClientArgs, command: [String]) throws -> String {
        let args = client.commandArgs(remoteCommand: command)
        AppLifecycleLog.info("ssh-tunnel", "token-fetch \(client.logDescription)")
        AppLifecycleLog.info("ssh-tunnel", "token-fetch full-args: ssh \(args.joined(separator: " "))")

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

    private func launchControlTunnel(localPort: Int, reversePort: Int, configPath: String?, identityPath: String?) throws {
        guard let sshTarget = profile.sshTarget, !sshTarget.isEmpty else {
            throw TunnelError.noTarget
        }
        let remotePort = profile.port
        let client = SSHClientArgs(
            sshTarget: sshTarget,
            port: profile.sshPort,
            identityPath: identityPath,
            configPath: configPath
        )
        var args = client.tunnelArgs(localPort: localPort, remotePort: remotePort)
        // Reverse tunnel: allows the remote daemon to reach this Mac's SSH server
        if reversePort > 0 {
            // Insert -R before the sshTarget (last element)
            args.insert("-R", at: args.count - 1)
            args.insert("\(reversePort):127.0.0.1:22", at: args.count - 1)
        }
        AppLifecycleLog.info(
            "ssh-tunnel",
            "launch \(client.logDescription) localPort=\(localPort) remotePort=\(remotePort)"
        )
        AppLifecycleLog.info("ssh-tunnel", "launch full-args: ssh \(args.joined(separator: " "))")

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

        let errPipe = Pipe()
        proc.standardError = errPipe
        // No readabilityHandler — we drain stderr synchronously after the process
        // exits so drainStderr() gets the complete output without a race.

        try proc.run()
        controlTunnel = SSHTunnelProcess(process: proc, stdoutPipe: outPipe, stderrPipe: errPipe)
    }

    private func launchSpotlightTunnel(
        localPort: Int,
        remotePort: Int,
        sshTarget: String,
        configPath: String?,
        identityPath: String
    ) throws -> SSHTunnelProcess {
        let client = SSHClientArgs(
            sshTarget: sshTarget,
            port: profile.sshPort,
            identityPath: identityPath,
            configPath: configPath
        )
        let args = client.tunnelArgs(localPort: localPort, remotePort: remotePort)

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
                if !trimmed.isEmpty { logger.debug("spotlight ssh stdout: \(trimmed, privacy: .public)") }
            }
        }

        let errPipe = Pipe()
        proc.standardError = errPipe
        errPipe.fileHandleForReading.readabilityHandler = { [logger] handle in
            let data = handle.availableData
            guard !data.isEmpty, let str = String(data: data, encoding: .utf8) else { return }
            for line in str.components(separatedBy: .newlines) {
                let trimmed = line.trimmingCharacters(in: .whitespacesAndNewlines)
                if !trimmed.isEmpty { logger.warning("spotlight ssh stderr: \(trimmed, privacy: .public)") }
            }
        }

        try proc.run()
        return SSHTunnelProcess(process: proc, stdoutPipe: outPipe, stderrPipe: errPipe)
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
            if let tunnel = controlTunnel, !tunnel.process.isRunning {
                let exitCode = tunnel.process.terminationStatus
                let stderr = tunnel.drainStderr()
                logger.error("ssh process died on attempt \(attempt, privacy: .public) exitCode=\(exitCode, privacy: .public) stderr=\(stderr, privacy: .public)")
                AppLifecycleLog.error("ssh-tunnel", "ssh process died exitCode=\(exitCode) stderr=\(stderr)")
                throw TunnelError.processDied(exitCode: exitCode, stderr: stderr)
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
                let isRunning = await self.controlTunnel?.process.isRunning ?? false
                if isRunning {
                    try? await Task.sleep(nanoseconds: 2_000_000_000)
                    continue
                }
                await self.setState(.failed(TunnelError.processDied(exitCode: 0, stderr: "")))
                try? await Task.sleep(nanoseconds: backoff)
                backoff = min(backoff * 2, 30_000_000_000)
                if Task.isCancelled { return }
                do {
                    try await self.relaunchControlTunnel(localPort: localPort)
                } catch {
                    continue
                }
            }
        }
    }

    private func relaunchControlTunnel(localPort: Int) throws {
        controlTunnel?.terminate()
        controlTunnel = nil
        let resolvedPaths = resolveScopedPaths()
        let configPath = resolvedPaths.configPath ?? existingSSHConfigPath()
        try launchControlTunnel(localPort: localPort, reversePort: _reversePort, configPath: configPath, identityPath: resolvedPaths.identityPath)
        setState(.connected)
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
    case processDied(exitCode: Int32, stderr: String)
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
        case let .processDied(exitCode, stderr):
            return TunnelError.describeProcessDied(exitCode: exitCode, stderr: stderr)
        case .timeout: return "Tunnel did not become ready within 30 seconds"
        case .tokenFetchFailed: return "Could not fetch daemon token from remote host"
        }
    }

    private static func describeProcessDied(exitCode: Int32, stderr: String) -> String {
        let detail = stderr.isEmpty ? "exit \(exitCode)" : "exit \(exitCode): \(stderr)"
        return "SSH tunnel failed — \(detail)"
    }
}
