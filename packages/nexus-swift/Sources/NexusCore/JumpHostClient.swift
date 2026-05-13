import Foundation
import Citadel

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
    func connect(
        target: SSHConnectionConfig,
        via jumpConfig: SSHConnectionConfig
    ) async throws -> SSHClient {
        try await factory.makeClientViaJump(target: target, jumpHost: JumpHostConfig(jumpConfig))
    }

    /// Execute a command on the target host via a jump host and return stdout.
    func execute(
        target: SSHConnectionConfig,
        via jumpConfig: SSHConnectionConfig,
        command: String
    ) async throws -> String {
        let client = try await factory.makeClientViaJump(target: target, jumpHost: JumpHostConfig(jumpConfig))
        defer { Task { try? await client.close() } }
        let buffer = try await client.executeCommand(command)
        return String(buffer: buffer)
    }
}
