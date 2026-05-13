import Foundation
import Citadel
import NIOCore
import NIO

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

    /// Create an SSHClient from configuration. Handles agent, identity file,
    /// and passphrase scenarios.
    func makeClient(config: SSHConnectionConfig) async throws -> SSHClient {
        let auth = try authenticationMethod(for: config.authMethod)
        let validator = hostKeyValidator(for: config.hostKeyValidation)

        if config.jumpHost != nil {
            // Jump host handled by makeClientViaJump
            return try await makeClientViaJump(
                target: config,
                jumpHost: config.jumpHost!
            )
        }

        return try await SSHClient.connect(
            host: config.host,
            port: config.port,
            authenticationMethod: auth,
            hostKeyValidator: validator,
            eventLoopGroup: eventLoopGroup
        )
    }

    /// Create a client that connects through a jump host by first establishing
    /// a connection to the jump host, then using port forwarding to reach the target.
    func makeClientViaJump(
        target: SSHConnectionConfig,
        jumpHost: SSHConnectionConfig
    ) async throws -> SSHClient {
        let jumpAuth = try authenticationMethod(for: jumpHost.authMethod)
        let jumpValidator = hostKeyValidator(for: jumpHost.hostKeyValidation)

        let jumpClient = try await SSHClient.connect(
            host: jumpHost.host,
            port: jumpHost.port,
            authenticationMethod: jumpAuth,
            hostKeyValidator: jumpValidator,
            eventLoopGroup: eventLoopGroup
        )

        let targetAuth = try authenticationMethod(for: target.authMethod)
        let targetValidator = hostKeyValidator(for: target.hostKeyValidation)

        return try await SSHClient.connect(
            host: target.host,
            port: target.port,
            authenticationMethod: targetAuth,
            hostKeyValidator: targetValidator,
            eventLoopGroup: eventLoopGroup,
            proxy: jumpClient
        )
    }

    // MARK: - Authentication Dispatch

    private func authenticationMethod(for authMethod: SSHAuthMethod) throws -> SSHMethodBasedAuthenticationMethod {
        switch authMethod {
        case .agent:
            return SSHAgent()
        case .identityFile(let url):
            let keyData = try Data(contentsOf: url)
            let keyString = String(decoding: keyData, as: UTF8.self)
            return try SSHPrivateKey(pemString: keyString)
        case .identityFileWithPassphrase(let url, let passphraseCallback):
            let keyData = try Data(contentsOf: url)
            let keyString = String(decoding: keyData, as: UTF8.self)
            return try SSHPrivateKey(pemString: keyString, passphrase: passphraseCallback)
        }
    }

    // MARK: - Host Key Validation

    private func hostKeyValidator(for validation: HostKeyValidation) -> SSHHostKeyValidator {
        switch validation {
        case .strict:
            // Accept only keys in known_hosts
            return .defaultKnownHosts()
        case .acceptOnce(let then):
            // Accept any key the first time, then enforce the captured key
            var acceptedKey: NIOSSHPublicKey?
            return .custom { key in
                if let stored = acceptedKey {
                    return stored == key
                }
                acceptedKey = key
                return true
            }
        case .disabled:
            // No validation (equivalent to -o StrictHostKeyChecking=no)
            return .acceptAnything()
        }
    }
}
