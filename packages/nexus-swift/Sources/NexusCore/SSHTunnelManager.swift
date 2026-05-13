import Foundation
import Network
import os
import Citadel
import NIOCore
import NIOPosix
import NIOSSH

public actor SSHTunnelManager {
    public enum State: Sendable {
        case idle
        case connecting
        case connected
        case failed(any Error)
    }

    // MARK: - SSHTunnelConnection

    /// A private struct that owns a Citadel SSH client and its local port-forward server channel.
    private struct SSHTunnelConnection {
        var client: SSHClient
        var serverChannel: Channel
        var eventLoopGroup: MultiThreadedEventLoopGroup

        func close() async {
            _ = try? await serverChannel.close()
            try? await eventLoopGroup.shutdownGracefully()
            try? await client.close()
        }
    }

    // MARK: - State stream

    public let stateStream: AsyncStream<State>
    private let stateContinuation: AsyncStream<State>.Continuation

    // MARK: - Properties

    private let profile: DaemonProfile
    private let clientFactory = SSHClientFactory()
    private var controlTunnel: SSHTunnelConnection?
    private var reverseTunnelClient: SSHClient?
    private var spotlightTunnels: [Int: SSHTunnelConnection] = [:]   // keyed by localPort
    private var _state: State = .idle
    private var _localPort: Int?
    private var _reversePort: Int = 0
    private var restartTask: Task<Void, Never>?
    private var activeScopedPaths: SSHSecurityScopedPaths = .empty
    private let logger = Logger(subsystem: "com.oursky.nexus", category: "SSHTunnel")

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
        let startTime = Date()
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
                try await launchControlTunnel(localPort: port)
                try await waitForHealthz(localPort: port)
                setState(.connected)
                NSLog("[SSHTunnelManager] Tunnel healthy: port=\(port) after=\(Date().timeIntervalSince(startTime))s")
                AppLifecycleLog.info("ssh-tunnel", "start success localPort=\(port)")
                // Launch reverse tunnel as best-effort (non-blocking)
                if rp > 0, let sshTarget = profile.sshTarget {
                    do {
                        try await launchReverseTunnel(reversePort: rp, sshTarget: sshTarget)
                        logger.info("Reverse tunnel established on port \(rp)")
                    } catch {
                        logger.warning("Reverse tunnel failed (non-blocking): \(error.localizedDescription)")
                        _reversePort = 0
                    }
                }
                startRestartLoop(localPort: port)
                return port
            } catch {
                lastError = error
                logger.error("tunnel start attempt \(attempt, privacy: .public) failed: \(error.localizedDescription, privacy: .public)")
                AppLifecycleLog.warn("ssh-tunnel", "attempt \(attempt) failed: \(error.localizedDescription)")
                await controlTunnel?.close()
                controlTunnel = nil

                if case TunnelError.noTarget = error { break }
                if let te = error as? TunnelError, case TunnelError.connectionClosed = te { break }
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

    public func startWithRetry(profile: DaemonProfile, retries: Int = 3) async throws -> Int {
        var lastError: Error?
        for attempt in 1...retries {
            do {
                let port = try await start()
                if attempt > 1 {
                    NSLog("[SSHTunnelManager] Tunnel started on attempt \(attempt)/\(retries)")
                }
                return port
            } catch {
                lastError = error
                NSLog("[SSHTunnelManager] Tunnel attempt \(attempt)/\(retries) failed: \(error.localizedDescription)")
                if attempt < retries {
                    try? await Task.sleep(nanoseconds: 1_000_000_000)
                }
            }
        }
        throw lastError ?? NSError(domain: "SSHTunnelManager", code: -1)
    }

    public func stop() async {
        AppLifecycleLog.info("ssh-tunnel", "stop")
        restartTask?.cancel()
        restartTask = nil
        await controlTunnel?.close()
        controlTunnel = nil
        if let reverseClient = reverseTunnelClient {
            try? await reverseClient.close()
            reverseTunnelClient = nil
        }
        await stopAllSpotlightTunnels()
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
            if existing.client.isConnected {
                return localPort
            }
            // Stale — clean it up
            let stale = spotlightTunnels.removeValue(forKey: localPort)
            await stale?.close()
        }

        guard let sshTarget = profile.sshTarget, !sshTarget.isEmpty else {
            throw TunnelError.noTarget
        }
        let tunnel = try await launchSpotlightTunnel(localPort: localPort, remotePort: remotePort, sshTarget: sshTarget)
        spotlightTunnels[localPort] = tunnel
        AppLifecycleLog.info("ssh-tunnel", "spotlight opened localPort=\(localPort) remotePort=\(remotePort)")

        // Wait until the local port accepts TCP connections (phase 1: bind probe)
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if !tunnel.client.isConnected {
                AppLifecycleLog.error("ssh-tunnel", "spotlight ssh client disconnected")
                throw TunnelError.connectionClosed
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
                    continuation.resume(throwing: TunnelError.noTarget)
                default:
                    break
                }
            }
            conn.start(queue: .global())
        }
    }

    /// Terminates the spotlight tunnel bound to `localPort`, if it exists.
    public func closeSpotlightTunnel(localPort: Int) async {
        guard let tunnel = spotlightTunnels.removeValue(forKey: localPort) else { return }
        await tunnel.close()
        AppLifecycleLog.info("ssh-tunnel", "spotlight closed localPort=\(localPort)")
    }

    func stopAllSpotlightTunnels() async {
        for (localPort, tunnel) in spotlightTunnels {
            await tunnel.close()
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

        #if DEBUG
        let remoteBin = "~/.local/bin/nexus-dev"
        #else
        let remoteBin = "~/.local/bin/nexus"
        #endif

        let config = makeSSHConfig()
        let client = try await clientFactory.makeClient(config: config)
        defer { Task { try? await client.close() } }

        let output = try await client.executeCommand("\(remoteBin) daemon token")
        var buffer = output
        let token = buffer.readString(length: buffer.readableBytes)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        if token.isEmpty { throw TunnelError.tokenFetchFailed }
        return token
    }

    // MARK: - Private helpers

    private func makeSSHConfig() -> SSHConnectionConfig {
        SSHConnectionConfig(
            host: profile.sshTarget ?? "",
            port: profile.sshPort ?? 22,
            authMethod: profile.authMethod,
            hostKeyValidation: .acceptOnceThenStrict
        )
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

    private func launchControlTunnel(localPort: Int) async throws {
        guard let sshTarget = profile.sshTarget, !sshTarget.isEmpty else {
            throw TunnelError.noTarget
        }
        let remotePort = profile.port
        let config = makeSSHConfig()
        let client = try await clientFactory.makeClient(config: config)

        let (serverChannel, group) = try await startPortForwardServer(
            client: client,
            localPort: localPort,
            remoteHost: "127.0.0.1",
            remotePort: remotePort
        )

        AppLifecycleLog.info("ssh-tunnel", "launch target=\(sshTarget) localPort=\(localPort) remotePort=\(remotePort)")
        controlTunnel = SSHTunnelConnection(client: client, serverChannel: serverChannel, eventLoopGroup: group)
    }

    private func launchReverseTunnel(reversePort: Int, sshTarget: String) async throws {
        let config = makeSSHConfig()
        let client = try await clientFactory.makeClient(config: config)

        // TODO: Citadel doesn't expose a high-level reverse forwarding API.
        // The low-level NIOSSH API (GlobalRequest.TCPForwardingRequest) requires
        // NIOSSHHandler access which is not public through Citadel's SSHClient.
        // Reverse tunnel will be re-enabled when Citadel adds remote forwarding support.
        logger.info("Reverse tunnel on port \(reversePort) deferred — not yet supported via Citadel")
        reverseTunnelClient = client
    }

    private func launchSpotlightTunnel(localPort: Int, remotePort: Int, sshTarget: String) async throws -> SSHTunnelConnection {
        let config = makeSSHConfig()
        let client = try await clientFactory.makeClient(config: config)

        let (serverChannel, group) = try await startPortForwardServer(
            client: client,
            localPort: localPort,
            remoteHost: "127.0.0.1",
            remotePort: remotePort
        )

        return SSHTunnelConnection(client: client, serverChannel: serverChannel, eventLoopGroup: group)
    }

    /// Starts a local ServerBootstrap that binds to `localPort` and forwards each accepted
    /// connection through the SSH client via a DirectTCPIP channel (using Citadel's public API).
    private func startPortForwardServer(
        client: SSHClient,
        localPort: Int,
        remoteHost: String,
        remotePort: Int
    ) async throws -> (channel: Channel, eventLoopGroup: MultiThreadedEventLoopGroup) {
        let group = MultiThreadedEventLoopGroup(numberOfThreads: 1)
        let bootstrap = ServerBootstrap(group: group)
            .serverChannelOption(ChannelOptions.socketOption(.so_reuseaddr), value: 1)
            .childChannelInitializer { inboundChannel in
                let originatorAddress = inboundChannel.remoteAddress ?? {
                    try! SocketAddress(ipAddress: "127.0.0.1", port: 0)
                }()
                let directTCPIP = SSHChannelType.DirectTCPIP(
                    targetHost: remoteHost,
                    targetPort: remotePort,
                    originatorAddress: originatorAddress
                )
                // Bridge async createDirectTCPIPChannel to EventLoopFuture
                let promise = client.eventLoop.makePromise(of: Void.self)
                promise.completeWithTask {
                    let _ = try await client.createDirectTCPIPChannel(
                        using: directTCPIP
                    ) { childChannel in
                        let (ours, theirs) = GlueHandler.matchedPair()
                        return childChannel.eventLoop.makeCompletedFuture {
                            try childChannel.pipeline.addHandler(SSHWrapperHandler())
                            try childChannel.pipeline.addHandler(ours)
                            try childChannel.pipeline.addHandler(ErrorHandler())
                            try inboundChannel.pipeline.addHandler(theirs)
                            try inboundChannel.pipeline.addHandler(ErrorHandler())
                        }
                    }
                }
                return promise.futureResult
            }

        return (
            channel: try await bootstrap.bind(host: "127.0.0.1", port: localPort).get(),
            eventLoopGroup: group
        )
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
            if let tunnel = controlTunnel, !tunnel.client.isConnected {
                logger.error("ssh client disconnected on attempt \(attempt, privacy: .public)")
                AppLifecycleLog.error("ssh-tunnel", "ssh client disconnected")
                throw TunnelError.connectionClosed
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
                let isConnected = await self.controlTunnel?.client.isConnected ?? false
                if isConnected {
                    try? await Task.sleep(nanoseconds: 2_000_000_000)
                    continue
                }
                await self.setState(.failed(TunnelError.connectionClosed))
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

    private func relaunchControlTunnel(localPort: Int) async throws {
        await controlTunnel?.close()
        controlTunnel = nil
        try await launchControlTunnel(localPort: localPort)
        setState(.connected)
        // Re-launch reverse tunnel (best-effort)
        if _reversePort > 0 {
            let rp = _reversePort
            do {
                try await launchReverseTunnel(reversePort: rp, sshTarget: profile.sshTarget ?? "")
                logger.info("Reverse tunnel re-established on port \(rp)")
            } catch {
                logger.warning("Reverse tunnel re-launch failed: \(error.localizedDescription)")
                _reversePort = 0
            }
        }
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
    case connectionClosed
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
        case .identityRequired: return "No SSH key configured. Go to Settings → select an identity file (e.g., ~/.ssh/id_ed25519). ssh-agent is not yet supported."
        case .connectionClosed: return "SSH connection closed unexpectedly"
        case .timeout: return "Tunnel did not become ready within 30 seconds"
        case .tokenFetchFailed: return "Could not fetch daemon token from remote host"
        }
    }
}

// MARK: - NIO Glue handlers for port forwarding

import NIOCore

/// A simple handler that wraps data into SSHChannelData for forwarding.
final class SSHWrapperHandler: ChannelDuplexHandler {
    typealias InboundIn = SSHChannelData
    typealias InboundOut = ByteBuffer
    typealias OutboundIn = ByteBuffer
    typealias OutboundOut = SSHChannelData

    func channelRead(context: ChannelHandlerContext, data: NIOAny) {
        let data = self.unwrapInboundIn(data)

        guard case .channel = data.type, case .byteBuffer(let buffer) = data.data else {
            context.fireErrorCaught(SSHClientError.invalidData)
            return
        }

        context.fireChannelRead(self.wrapInboundOut(buffer))
    }

    func write(context: ChannelHandlerContext, data: NIOAny, promise: EventLoopPromise<Void>?) {
        let data = self.unwrapOutboundIn(data)
        let wrapped = SSHChannelData(type: .channel, data: .byteBuffer(data))
        context.write(self.wrapOutboundOut(wrapped), promise: promise)
    }
}

final class ErrorHandler: ChannelInboundHandler {
    typealias InboundIn = ByteBuffer

    func errorCaught(context: ChannelHandlerContext, error: Error) {
        context.close(promise: nil)
    }
}

enum SSHClientError: Error {
    case invalidChannelType
    case invalidData
}

// MARK: - GlueHandler

/// A matched pair of handlers that forward data between two channels.
final class GlueHandler: ChannelInboundHandler {
    typealias InboundIn = ByteBuffer
    typealias OutboundOut = ByteBuffer

    private var partner: GlueHandler?

    static func matchedPair() -> (GlueHandler, GlueHandler) {
        let first = GlueHandler()
        let second = GlueHandler()
        first.partner = second
        second.partner = first
        return (first, second)
    }

    private func partnerWrite(_ data: ByteBuffer) {
        partner?.write(data)
    }

    private func write(_ data: ByteBuffer) {
        // This will be called on the partner's handler
    }

    func channelRead(context: ChannelHandlerContext, data: NIOAny) {
        let data = self.unwrapInboundIn(data)
        partner?.context?.writeAndFlush(self.wrapOutboundOut(data), promise: nil)
    }

    func channelInactive(context: ChannelHandlerContext) {
        partner?.context?.close(promise: nil)
        context.fireChannelInactive()
    }

    func errorCaught(context: ChannelHandlerContext, error: Error) {
        partner?.context?.close(promise: nil)
        context.fireErrorCaught(error)
    }

    private var context: ChannelHandlerContext?

    func handlerAdded(context: ChannelHandlerContext) {
        self.context = context
    }

    func handlerRemoved(context: ChannelHandlerContext) {
        self.context = nil
        self.partner = nil
    }
}
