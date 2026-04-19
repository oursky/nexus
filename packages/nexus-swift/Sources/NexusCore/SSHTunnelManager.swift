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
    private let logger = Logger(subsystem: "com.nexus.NexusApp", category: "SSHTunnel")

    public init(profile: DaemonProfile) {
        self.profile = profile
    }

    public var state: State { _state }
    public var localPort: Int? { _localPort }

    public func start() async throws -> Int {
        _state = .connecting
        let port = try allocateLocalPort()
        _localPort = port
        try launchSSH(localPort: port)
        try await waitForHealthz(localPort: port)
        _state = .connected
        startRestartLoop(localPort: port)
        return port
    }

    /// Fetches the daemon token from the remote host via SSH.
    /// Calls `<remoteBin> daemon token` on the remote — the running daemon is the
    /// single source of truth for the auth token.
    public func fetchRemoteToken() async throws -> String {
        guard let sshTarget = profile.sshTarget, !sshTarget.isEmpty else {
            throw TunnelError.noTarget
        }

        let remoteBin = "/home/newman/magic/bin/nexus"
        let token = try runSSH(sshTarget: sshTarget, command: [remoteBin, "daemon", "token"])
        if token.isEmpty { throw TunnelError.tokenFetchFailed }
        return token
    }

    private func runSSH(sshTarget: String, command: [String]) throws -> String {
        let sshPort = profile.sshPort ?? 22
        var args = ["-p", "\(sshPort)", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=no"]
        if let identity = profile.sshIdentity, !identity.isEmpty {
            args += ["-i", identity]
        }
        args += [sshTarget] + command

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
        restartTask?.cancel()
        restartTask = nil
        process?.terminate()
        process = nil
        stderrPipe = nil
        _state = .idle
    }

    private func allocateLocalPort() throws -> Int {
        let sock = socket(AF_INET, SOCK_STREAM, 0)
        guard sock >= 0 else { throw TunnelError.portAllocation }
        defer { close(sock) }
        var addr = sockaddr_in()
        addr.sin_family = sa_family_t(AF_INET)
        addr.sin_port = 0
        addr.sin_addr.s_addr = INADDR_ANY
        let bindResult = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                bind(sock, $0, socklen_t(MemoryLayout<sockaddr_in>.size))
            }
        }
        guard bindResult == 0 else { throw TunnelError.portAllocation }
        var len = socklen_t(MemoryLayout<sockaddr_in>.size)
        let nameResult = withUnsafeMutablePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                getsockname(sock, $0, &len)
            }
        }
        guard nameResult == 0 else { throw TunnelError.portAllocation }
        return Int(addr.sin_port.bigEndian)
    }

    private func launchSSH(localPort: Int) throws {
        guard let sshTarget = profile.sshTarget, !sshTarget.isEmpty else {
            throw TunnelError.noTarget
        }
        let remotePort = profile.port
        let sshPort = profile.sshPort ?? 22

        var args = [
            "-N",
            "-o", "ExitOnForwardFailure=yes",
            "-o", "ServerAliveInterval=10",
            "-L", "\(localPort):127.0.0.1:\(remotePort)",
            "-p", "\(sshPort)"
        ]
        if let identity = profile.sshIdentity, !identity.isEmpty {
            args += ["-i", identity]
        }
        args.append(sshTarget)

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
        stderrPipe = nil
        try launchSSH(localPort: localPort)
        _state = .connected
    }
}

public enum TunnelError: Error, LocalizedError {
    case portAllocation
    case noTarget
    case processDied
    case timeout
    case tokenFetchFailed

    public var errorDescription: String? {
        switch self {
        case .portAllocation: return "Failed to allocate local port"
        case .noTarget: return "No SSH target configured"
        case .processDied: return "SSH process exited unexpectedly"
        case .timeout: return "Tunnel did not become ready within 15 seconds"
        case .tokenFetchFailed: return "Could not fetch daemon token from remote host"
        }
    }
}
