import Foundation

// MARK: - SSH Connection Configuration

/// Configuration for an SSH connection.
/// Replaces the ad-hoc ["-F", "/dev/null", "-o", ...] argument arrays
/// used with Process("/usr/bin/ssh").
struct SSHConnectionConfig {
    let host: String
    let port: Int
    let authMethod: SSHAuthMethod
    let hostKeyValidation: HostKeyValidation
    let connectTimeout: TimeInterval
    let jumpHost: JumpHostConfig?

    init(
        host: String,
        port: Int = 22,
        authMethod: SSHAuthMethod,
        hostKeyValidation: HostKeyValidation = .acceptOnceThenStrict,
        connectTimeout: TimeInterval = 15,
        jumpHost: JumpHostConfig? = nil
    ) {
        self.host = host
        self.port = port
        self.authMethod = authMethod
        self.hostKeyValidation = hostKeyValidation
        self.connectTimeout = connectTimeout
        self.jumpHost = jumpHost
    }
}

// MARK: - Jump Host

/// Configuration for an intermediate jump/bastion host.
struct JumpHostConfig {
    let host: String
    let port: Int
    let authMethod: SSHAuthMethod

    init(
        host: String,
        port: Int = 22,
        authMethod: SSHAuthMethod
    ) {
        self.host = host
        self.port = port
        self.authMethod = authMethod
    }
}

// MARK: - Authentication Method

/// How the SSH client authenticates.
enum SSHAuthMethod {
    /// Authenticate via ssh-agent (SSH_AUTH_SOCK). Default for sandbox compatibility.
    case agent
    /// Read an identity file in-process and authenticate with it.
    case identityFile(URL)
    /// Read a passphrase-protected identity file in-process.
    case identityFileWithPassphrase(URL, () async -> String?)
}

// MARK: - Host Key Validation

/// How the SSH client validates the remote host key.
enum HostKeyValidation {
    /// Validate against known_hosts. Fails if key is unknown or mismatched.
    case strict
    /// Accept on first connection, add to known_hosts, then switch to strict.
    case acceptOnceThenStrict
    /// No validation. Only for limited scenarios like cleanroom provisioning.
    case disabled
}
