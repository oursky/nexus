import Foundation

/// Builds SSH argument lists for child `/usr/bin/ssh` processes running under
/// macOS app sandbox. All SSH invocations that talk to the remote daemon host
/// must go through this builder so that sandbox-safe arguments are applied
/// consistently everywhere.
///
/// ## Sandbox compliance
/// Under app-sandbox, child processes CANNOT access `~/.ssh/` files
/// (Operation not permitted). This builder enforces:
///   -F /dev/null                    — bypass ~/.ssh/config entirely
///   -o BatchMode=yes                — never prompt interactively
///   -o StrictHostKeyChecking=no     — host key validation is the caller's job
///   -o UserKnownHostsFile=/dev/null — never write to ~/.ssh/known_hosts
///   -o GlobalKnownHostsFile=/dev/null
///
/// ## Authentication
/// All key authentication is handled exclusively through `ssh-agent`
/// (SSH_AUTH_SOCK). No `-i <key>` flags are ever passed to child processes.
/// The `identityPath` and `configPath` fields on this struct are retained for
/// caller API compatibility but have no effect on the generated arguments.
/// The caller is responsible for ensuring a suitable key is loaded into
/// ssh-agent before invoking any SSH operation.
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
            identityPath: scopedPaths.identityPath ?? profile.resolvedIdentity(),
            configPath: scopedPaths.configPath
        )
    }

    // MARK: - Builders

    /// Base connection options shared by all modes (no `-N`, no `-L`, no target appended).
    ///
    /// Callers append mode-specific flags then the target and remote command.
    public var baseArgs: [String] {
        var args: [String] = []

        // NEVER use ~/.ssh/config under sandbox — the child /usr/bin/ssh
        // process cannot open() files in ~/.ssh/ (Operation not permitted).
        // Instead rely on the SSH agent for key authentication.
        args += ["-F", "/dev/null"]

        args += ["-p", "\(port ?? 22)"]
        args += ["-o", "BatchMode=yes"]
        args += ["-o", "StrictHostKeyChecking=no"]
        args += ["-o", "UserKnownHostsFile=/dev/null"]
        args += ["-o", "GlobalKnownHostsFile=/dev/null"]
        args += ["-o", "ConnectTimeout=10"]

        return args
    }

    /// Args for a one-shot remote argv: `ssh <base> <target> <command...>`
    ///
    /// Use this when passing a pre-tokenised command (e.g. `["nexus", "daemon", "token"]`).
    /// SSH passes each element as a separate argument to the remote login shell's exec,
    /// so no shell quoting is needed and the result is not affected by the remote login shell.
    public func commandArgs(remoteCommand: [String]) -> [String] {
        baseArgs + [sshTarget] + remoteCommand
    }

    /// Args for a one-shot remote shell script: `ssh <base> <target> '/bin/bash -c '\''<script>'\'''`
    ///
    /// Uses the absolute path `/bin/bash` so the script runs under bash regardless of
    /// the user's login shell (fish, zsh, etc.) and regardless of whether `bash` is on
    /// PATH in the remote non-interactive SSH environment.
    ///
    /// The entire `/bin/bash -c '<script>'` command is passed as a **single** SSH argument
    /// so that spaces, redirections, and other shell metacharacters in `script` are
    /// preserved exactly. Without this, SSH concatenates separate arguments with spaces
    /// and the remote shell receives a broken command (e.g. `>/dev/null` becomes a
    /// separate argument instead of a redirection).
    public func shellArgs(script: String) -> [String] {
        // Escape single quotes in the script: ' -> '\''
        let escaped = script.replacingOccurrences(of: "'", with: "'\\''")
        let remoteCommand = "/bin/bash -c '\(escaped)'"
        return baseArgs + [sshTarget, remoteCommand]
    }

    /// Args for a background port-forward tunnel: `ssh -v -N -o ExitOnForwardFailure=yes -o ServerAliveInterval=10 -L ...`
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
