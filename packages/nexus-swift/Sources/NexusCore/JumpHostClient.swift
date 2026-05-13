import Foundation
import Citadel
import NIOCore

/// Wraps Citadel SSH jump host connections.
///
/// Connects to a target host through an intermediate jump/bastion host using
/// Citadel's native `SSHClient.jump(to:)` API.
actor JumpHostClient {
    let factory: SSHClientFactory

    init(factory: SSHClientFactory) {
        self.factory = factory
    }

    /// Connect to a target host through a jump host.
    ///
    /// 1. Connects to the jump host via `factory.makeClient(config:)`.
    /// 2. Uses Citadel's `jump(to:)` to establish a nested SSH session
    ///    from the jump host to the target.
    func connect(
        target: SSHConnectionConfig,
        via jumpConfig: SSHConnectionConfig
    ) async throws -> SSHClient {
        let jumpClient = try await factory.makeClient(config: jumpConfig)
        return try await jumpClient.jump(to: target.citadelSettings)
    }

    /// Execute a command on the target host via a jump host and return stdout.
    func execute(
        target: SSHConnectionConfig,
        via jumpConfig: SSHConnectionConfig,
        command: String
    ) async throws -> String {
        let client = try await connect(target: target, via: jumpConfig)
        defer { Task { try? await client.close() } }
        return try await client.executeCommand(command)
    }
}

// MARK: - Helpers

private extension SSHConnectionConfig {
    /// Convert NexusCore config to Citadel's `SSHClientSettings`.
    var citadelSettings: SSHClientSettings {
        get throws {
            try SSHClientSettings(
                host: host,
                port: port,
                authenticationMethod: authMethod.citadelMethod,
                hostKeyValidator: hostKeyValidation.citadelValidator
            )
        }
    }
}

private extension SSHAuthMethod {
    var citadelMethod: SSHMethodBasedAuthenticationMethod {
        get throws {
            switch self {
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
    }
}

private extension HostKeyValidation {
    var citadelValidator: SSHHostKeyValidator {
        switch self {
        case .strict:
            return .defaultKnownHosts()
        case .acceptOnceThenStrict:
            return .acceptAnything()
        case .disabled:
            return .acceptAnything()
        }
    }
}
