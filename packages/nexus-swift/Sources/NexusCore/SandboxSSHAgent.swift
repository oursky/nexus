import Foundation

/// Manages an ssh-agent process with a unix socket in the app container,
/// allowing sandboxed child /usr/bin/ssh processes to authenticate via keys
/// loaded by the parent app (which has security-scoped access to identity files).
public actor SandboxSSHAgent {
    private var agentProcess: Process?
    private var socketPath: String?
    
    /// The SSH_AUTH_SOCK value for child processes to use, or nil if agent not running.
    public var authSocket: String? { socketPath }
    
    /// Start ssh-agent with a socket in the app container.
    /// Returns the socket path, or nil if start failed.
    public func start() -> String? {
        // Place the socket directly in the home directory (which is the sandbox container
        // under App Sandbox). Using Application Support subdirectories produces paths that
        // exceed the 104-char unix socket path limit on macOS.
        // e.g. ~/Library/Containers/com.oursky.nexus.local/Data/.nexus-agent.sock (78 chars)
        let homeURL = FileManager.default.homeDirectoryForCurrentUser
        let sockPath = homeURL.appendingPathComponent(".nexus-agent.sock").path
        // Remove stale socket
        try? FileManager.default.removeItem(atPath: sockPath)
        
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/ssh-agent")
        proc.arguments = ["-a", sockPath]
        proc.standardOutput = FileHandle.nullDevice
        proc.standardError = FileHandle.nullDevice
        
        do {
            try proc.run()
            // Give agent time to create socket
            Thread.sleep(forTimeInterval: 0.5)
            guard FileManager.default.fileExists(atPath: sockPath) else {
                proc.terminate()
                return nil
            }
            agentProcess = proc
            socketPath = sockPath
            return sockPath
        } catch {
            return nil
        }
    }
    
    /// Load an identity file into the agent.
    ///
    /// Strategy:
    /// 1. Read the key file directly (works when the entitlement grants access,
    ///    e.g. a properly signed TestFlight build with the .ssh/ temporary exception).
    /// 2. Fall back to `ssh-add --apple-load-keychain` which loads SSH keys that
    ///    the user stored in the macOS Keychain — requires no file access at all.
    ///
    /// Returns true if at least one key ends up in the agent.
    public func loadIdentity(_ path: String) -> Bool {
        guard let sock = socketPath else { return false }

        // Strategy 1: read key from file and pipe to ssh-add via stdin.
        if let keyData = try? Data(contentsOf: URL(fileURLWithPath: path)) {
            let proc = Process()
            proc.executableURL = URL(fileURLWithPath: "/usr/bin/ssh-add")
            proc.arguments = ["-"]
            proc.standardOutput = FileHandle.nullDevice
            let errPipe = Pipe()
            proc.standardError = errPipe
            let inPipe = Pipe()
            proc.standardInput = inPipe
            var env = ProcessInfo.processInfo.environment
            env["SSH_AUTH_SOCK"] = sock
            proc.environment = env
            do {
                try proc.run()
                inPipe.fileHandleForWriting.write(keyData)
                inPipe.fileHandleForWriting.closeFile()
                proc.waitUntilExit()
                let errData = errPipe.fileHandleForReading.readDataToEndOfFile()
                let errStr = String(data: errData, encoding: .utf8)?
                    .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                let status = proc.terminationStatus
                AppLifecycleLog.info("ssh-agent", "ssh-add (file) exit=\(status) stderr='\(errStr)'")
                if status == 0 { return true }
                if errStr.localizedCaseInsensitiveContains("already") { return true }
            } catch {
                AppLifecycleLog.warn("ssh-agent", "ssh-add (file) launch failed: \(error.localizedDescription)")
            }
        } else {
            AppLifecycleLog.info("ssh-agent",
                "cannot read \(path) directly (sandbox) — trying macOS Keychain fallback")
        }

        // Strategy 2: load from macOS Keychain (no file I/O required).
        // Works when the user ran `ssh-add --apple-use-keychain` to persist their key.
        return loadFromKeychain(sock: sock)
    }

    /// Load SSH keys stored in the macOS Keychain into this agent.
    /// Uses `ssh-add --apple-load-keychain` which reads from the Keychain without
    /// requiring any file-system access — safe under App Sandbox.
    @discardableResult
    public func loadFromKeychain(sock: String? = nil) -> Bool {
        guard let sock = sock ?? socketPath else { return false }
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/ssh-add")
        proc.arguments = ["--apple-load-keychain"]
        proc.standardOutput = FileHandle.nullDevice
        let errPipe = Pipe()
        proc.standardError = errPipe
        var env = ProcessInfo.processInfo.environment
        env["SSH_AUTH_SOCK"] = sock
        proc.environment = env
        do {
            try proc.run()
            proc.waitUntilExit()
            let errData = errPipe.fileHandleForReading.readDataToEndOfFile()
            let errStr = String(data: errData, encoding: .utf8)?
                .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            let status = proc.terminationStatus
            AppLifecycleLog.info("ssh-agent", "ssh-add --apple-load-keychain exit=\(status) stderr='\(errStr)'")
            // "No identity found in the keychain." with exit 0 means nothing was loaded.
            if errStr.localizedCaseInsensitiveContains("No identity found") { return false }
            return status == 0
        } catch {
            AppLifecycleLog.warn("ssh-agent", "ssh-add --apple-load-keychain failed: \(error.localizedDescription)")
            return false
        }
    }
    
    /// Stop the ssh-agent process.
    public func stop() {
        agentProcess?.terminate()
        agentProcess = nil
        if let sock = socketPath {
            try? FileManager.default.removeItem(atPath: sock)
        }
        socketPath = nil
    }
}
