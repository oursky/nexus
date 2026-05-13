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
/// Key authentication is handled through `ssh-agent`. When `agentSocket` is set,
/// `-o IdentityAgent=<sock>` is added to `baseArgs` so that child SSH processes
/// use the app-owned agent explicitly (overriding SSH_AUTH_SOCK and the default
/// agent path). This is the preferred approach under App Sandbox.
///
/// No `-i <key>` flags are ever passed to child processes.
/// The `identityPath` and `configPath` fields on this struct are retained for
/// caller API compatibility but have no effect on the generated arguments.
/// The caller is responsible for ensuring a suitable key is loaded into
/// the agent before invoking any SSH operation.
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
    /// App-owned ssh-agent socket. When non-nil, `-o IdentityAgent=<sock>` is added
    /// to `baseArgs` so child SSH processes use our sandboxed agent explicitly.
    public let agentSocket: String?

    // MARK: - Init

    public init(
        sshTarget: String,
        port: Int? = nil,
        identityPath: String? = nil,
        configPath: String? = nil,
        agentSocket: String? = nil
    ) {
        self.sshTarget = sshTarget
        self.port = port
        self.identityPath = identityPath?.trimmingCharacters(in: .whitespacesAndNewlines).nonEmpty
        self.configPath = configPath?.trimmingCharacters(in: .whitespacesAndNewlines).nonEmpty
        self.agentSocket = agentSocket?.trimmingCharacters(in: .whitespacesAndNewlines).nonEmpty
    }

    /// Convenience: pull values directly from a `DaemonProfile` and resolved security-scoped paths.
    public init(profile: DaemonProfile, scopedPaths: SSHSecurityScopedPaths, agentSocket: String? = nil) {
        self.init(
            sshTarget: profile.sshTarget ?? "",
            port: profile.sshPort,
            identityPath: scopedPaths.identityPath ?? profile.resolvedIdentity(),
            configPath: scopedPaths.configPath,
            agentSocket: agentSocket
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

        // Explicitly wire child SSH processes to the app-owned agent so they are
        // not affected by sandbox restrictions on the system launchd agent socket.
        if let sock = agentSocket {
            args += ["-o", "IdentityAgent=\(sock)"]
        }

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

    /// Args for a background multi-port-forward tunnel (e.g. Spotlight's dynamic port list).
    ///
    /// Produces: `ssh <baseArgs> -N -o ExitOnForwardFailure=yes -L <p1> [-L <p2> ...] <target>`
    ///
    /// Use this instead of `tunnelArgs` when you need to forward several ports in a single
    /// SSH connection (fewer processes, fewer auth round-trips).
    ///
    /// Each element is `(localPort, remotePort)` — labels are ignored so callers can pass
    /// tuples with any label names (e.g. `targetPort` from `DaemonClient.startTunnels`).
    public func multiTunnelArgs(forwards: [(Int, Int)]) -> [String] {
        var args = baseArgs + ["-N", "-o", "ExitOnForwardFailure=yes"]
        for (local, remote) in forwards {
            args += ["-L", "\(local):127.0.0.1:\(remote)"]
        }
        args.append(sshTarget)
        return args
    }

    // MARK: - Process factory

    /// Returns a `Process` wired to `/usr/bin/ssh` with `arguments` set to `args` and
    /// the environment configured for sandbox-safe operation:
    ///   - `SHELL=/bin/sh` — prevents ssh from picking up a non-POSIX login shell
    ///   - `SSH_AUTH_SOCK=<sock>` (belt-and-suspenders, when agentSocket is set) — ensures
    ///     any further child processes spawned by ssh also find the agent
    ///
    /// The caller is responsible for setting `standardOutput`, `standardError`, and
    /// `standardInput` before calling `proc.run()`.
    public func makeProcess(args: [String]) -> Process {
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/ssh")
        proc.arguments = args
        var env = ProcessInfo.processInfo.environment
        env["SHELL"] = "/bin/sh"
        if let sock = agentSocket {
            env["SSH_AUTH_SOCK"] = sock
        }
        proc.environment = env
        return proc
    }

    // MARK: - Diagnostics

    public var logDescription: String {
        let agentDesc = agentSocket != nil ? "app-agent" : "inherited"
        return "target=\(sshTarget) port=\(port ?? 22) identity=\(identityPath != nil ? "set" : "unset") agent=\(agentDesc)"
    }
}

// MARK: - Helpers

private extension String {
    var nonEmpty: String? { isEmpty ? nil : self }
}
