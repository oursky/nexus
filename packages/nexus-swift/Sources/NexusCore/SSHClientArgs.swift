import Foundation

/// Builds SSH argument lists with consistent, correct option handling.
///
/// All SSH invocations that talk to the remote daemon host must go through
/// this builder so that key-selection enforcement, config-file handling, and
/// common safety options are applied identically everywhere.
///
/// ## Key-selection enforcement
/// When an explicit identity file is provided the builder adds:
///   -F /dev/null        — skip ~/.ssh/config so it cannot inject extra keys
///   -o IdentitiesOnly=yes   — only the explicitly specified key is offered
///   -o IdentityAgent=none   — SSH agent is not consulted
///
/// This means a wrong key fails immediately with "Permission denied" (exit 255)
/// instead of silently succeeding because the agent or config supplied the real key.
///
/// When no identity is given, the SSH config file (-F <path>) is used as
/// normal so that ProxyJump or custom Hostname entries continue to work.
public struct SSHClientArgs {

    // MARK: - Inputs

    /// SSH target in `[user@]host` form.
    public let sshTarget: String
    /// SSH port (nil → 22).
    public let port: Int?
    /// Resolved identity file path (security-scoped if running sandboxed).
    public let identityPath: String?
    /// Resolved SSH config path (security-scoped if running sandboxed).
    /// Ignored when `identityPath` is non-empty — strict-key mode uses `-F /dev/null`.
    public let configPath: String?

    // MARK: - Init

    public init(
        sshTarget: String,
        port: Int? = nil,
        identityPath: String? = nil,
        configPath: String? = nil
    ) {
        self.sshTarget = sshTarget
        self.port = port
        self.identityPath = identityPath?.trimmingCharacters(in: .whitespacesAndNewlines).nonEmpty
        self.configPath = configPath?.trimmingCharacters(in: .whitespacesAndNewlines).nonEmpty
    }

    /// Convenience: pull values directly from a `DaemonProfile` and resolved security-scoped paths.
    public init(profile: DaemonProfile, scopedPaths: SSHSecurityScopedPaths) {
        self.init(
            sshTarget: profile.sshTarget ?? "",
            port: profile.sshPort,
            identityPath: scopedPaths.identityPath ?? profile.sshIdentity,
            configPath: scopedPaths.configPath
        )
    }

    // MARK: - Builders

    /// Base connection options shared by all modes (no `-N`, no `-L`, no target appended).
    ///
    /// Callers append mode-specific flags then the target and remote command.
    public var baseArgs: [String] {
        var args: [String] = []

        // Strict-key mode: bypass ~/.ssh/config entirely.
        if let identity = identityPath {
            args += ["-F", "/dev/null"]
            args += ["-i", identity, "-o", "IdentitiesOnly=yes", "-o", "IdentityAgent=none"]
        } else if let config = configPath {
            args += ["-F", config]
        }

        args += ["-p", "\(port ?? 22)"]
        args += ["-o", "BatchMode=yes"]
        args += ["-o", "StrictHostKeyChecking=no"]
        args += ["-o", "UserKnownHostsFile=/dev/null"]
        args += ["-o", "GlobalKnownHostsFile=/dev/null"]
        args += ["-o", "ConnectTimeout=10"]

        return args
    }

    /// Args for a one-shot remote command: `ssh <base> <target> <command...>`
    public func commandArgs(remoteCommand: [String]) -> [String] {
        baseArgs + [sshTarget] + remoteCommand
    }

    /// Args for a background port-forward tunnel: `ssh -N -o ExitOnForwardFailure=yes -o ServerAliveInterval=10 -L ...`
    public func tunnelArgs(localPort: Int, remotePort: Int) -> [String] {
        baseArgs + [
            "-N",
            "-o", "ExitOnForwardFailure=yes",
            "-o", "ServerAliveInterval=10",
            "-L", "\(localPort):127.0.0.1:\(remotePort)",
            sshTarget,
        ]
    }

    // MARK: - Diagnostics

    public var logDescription: String {
        "target=\(sshTarget) port=\(port ?? 22) identity=\(identityPath != nil ? "set (strict)" : "unset") config=\(identityPath != nil ? "/dev/null" : configPath ?? "<default>")"
    }
}

// MARK: - Helpers

private extension String {
    var nonEmpty: String? { isEmpty ? nil : self }
}
