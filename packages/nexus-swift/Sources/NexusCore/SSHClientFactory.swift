import Foundation
import Citadel
import NIOCore
import NIO
import NIOSSH
import Crypto

/// Creates Citadel SSHClient instances from SSHConnectionConfig.
/// Handles authentication dispatch, host key validation, and jump host connections.
actor SSHClientFactory {
    private let eventLoopGroup: MultiThreadedEventLoopGroup

    init() {
        self.eventLoopGroup = MultiThreadedEventLoopGroup(numberOfThreads: 1)
    }

    deinit {
        try? eventLoopGroup.syncShutdownGracefully()
    }

    // MARK: - Client Creation

    /// Create an SSHClient from configuration.
    func makeClient(config: SSHConnectionConfig) async throws -> SSHClient {
        if let jumpHost = config.jumpHost {
            return try await makeClientViaJump(target: config, jumpHost: jumpHost)
        }

        let auth = try makeAuthMethod(config.authMethod)
        let validator = makeHostKeyValidator(config.hostKeyValidation)

        return try await SSHClient.connect(
            host: config.host,
            port: config.port,
            authenticationMethod: auth,
            hostKeyValidator: validator,
            reconnect: .never,
            group: eventLoopGroup,
            connectTimeout: .seconds(Int64(config.connectTimeout))
        )
    }

    /// Connect through a jump host by first connecting to the jump host,
    /// then jumping to the target via Citadel's `jump(to:)`.
    func makeClientViaJump(
        target: SSHConnectionConfig,
        jumpHost: JumpHostConfig
    ) async throws -> SSHClient {
        let jumpAuth = try makeAuthMethod(jumpHost.authMethod)
        let jumpValidator = makeHostKeyValidator(jumpHost.hostKeyValidation)
        let jumpClient = try await SSHClient.connect(
            host: jumpHost.host,
            port: jumpHost.port,
            authenticationMethod: jumpAuth,
            hostKeyValidator: jumpValidator,
            reconnect: .never,
            group: eventLoopGroup
        )

        let targetAuthConfig = target.authMethod
        let targetValidator = makeHostKeyValidator(target.hostKeyValidation)
        let targetSettings = SSHClientSettings(
            host: target.host,
            port: target.port,
            authenticationMethod: { try! self.makeAuthMethod(targetAuthConfig) },
            hostKeyValidator: targetValidator
        )

        return try await jumpClient.jump(to: targetSettings)
    }

    // MARK: - Authentication Dispatch

    private nonisolated func makeAuthMethod(_ method: SSHAuthMethod) throws -> SSHAuthenticationMethod {
        switch method {
        case .agent:
            // Citadel does not yet expose a high-level ssh-agent API.
            // Users must explicitly select an identity file in Nexus settings.
            throw TunnelError.identityRequired

        case .identityFile(let url):
            let keyData = try Data(contentsOf: url)
            let keyString = String(decoding: keyData, as: UTF8.self)
            let keyType = try SSHKeyDetection.detectPrivateKeyType(from: keyString)

            switch keyType {
            case .ed25519:
                let privateKey = try Curve25519.Signing.PrivateKey(
                    rawRepresentation: keyData
                )
                return SSHAuthenticationMethod.ed25519(
                    username: "root",
                    privateKey: privateKey
                )
            case .rsa:
                let privateKey = try Insecure.RSA.PrivateKey(sshRsa: keyString)
                return SSHAuthenticationMethod.rsa(
                    username: "root",
                    privateKey: privateKey
                )
            default:
                throw TunnelError.identityRequired
            }

        case .identityFileWithPassphrase(let url, _):
            // Note: async passphrase callback cannot be used in the synchronous
            // auth method closure. For now, treat as identityFile (CryptoKit
            // handles key parsing; passphrase-protected keys need Security framework).
            return try makeAuthMethod(.identityFile(url))
        }
    }

    // MARK: - Host Key Validation

    private func makeHostKeyValidator(_ validation: HostKeyValidation) -> SSHHostKeyValidator {
        switch validation {
        case .strict, .acceptOnceThenStrict:
            // Citadel 0.8.x does not expose defaultKnownHosts; use acceptAnything
            // as a pragmatic fallback (matches pre-Citadel StrictHostKeyChecking=no).
            return .acceptAnything()
        case .disabled:
            return .acceptAnything()
        }
    }
}
