import Foundation

/// Minimal port model returned from discoverPorts
struct DiscoveredPort: Codable, Sendable {
    let localPort: Int
    let remotePort: Int
    let service: String?
    let `protocol`: String?
    let source: String?
}

actor SpotlightManager {
    private let client: any DaemonClient
    private var sshProcesses: [String: Process] = [:]  // key: workspaceID

    init(client: any DaemonClient) {
        self.client = client
    }

    /// Discover forwarded ports for a workspace using the DaemonClient protocol method.
    func discoverPorts(workspaceID: String) async throws -> [DiscoveredPort] {
        let raw = try await client.discoverPorts(workspaceID: workspaceID)
        guard let ports = raw as? [[String: Any]] else { return [] }
        return try ports.compactMap { dict in
            // Codable expects camelCase, daemon sends snake_case — map manually.
            guard let localPort = dict["localPort"] as? Int,
                  let remotePort = dict["remotePort"] as? Int else {
                return nil
            }
            return DiscoveredPort(
                localPort: localPort,
                remotePort: remotePort,
                service: dict["service"] as? String,
                protocol: dict["protocol"] as? String,
                source: dict["source"] as? String
            )
        }
    }

    /// Start spotlight forwards on the daemon side. Returns the list of created forwards.
    func startSpotlight(workspaceID: String, ports: [DiscoveredPort]) async throws {
        for port in ports {
            let _ = try await client.spotlightStart(
                workspaceId: workspaceID,
                localPort: port.localPort,
                remotePort: port.remotePort,
                protocolText: port.protocol
            )
        }
    }

    /// Stop spotlight on daemon side for the given workspace.
    func stopSpotlight(workspaceID: String) async throws {
        try await client.spotlightStopWorkspace(workspaceId: workspaceID)
    }

    /// Start daemon-side port forwarding tunnels (replaces Go's `sshtunnel.MultiWithOptions`).
    /// Returns the tunnel status and the list of (localPort, targetPort) pairs.
    func startDaemonTunnels(workspaceID: String, host: String, sshPort: Int, identityFile: String) async throws {
        let (_, forwards) = try await client.startTunnels(workspaceId: workspaceID)

        // Build SSH command args
        var args = ["-N", "-o", "StrictHostKeyChecking=accept-new", "-o", "ExitOnForwardFailure=yes"]
        args += ["-p", String(sshPort)]
        args += ["-i", identityFile]
        for (localPort, targetPort) in forwards {
            args += ["-L", "\(localPort):127.0.0.1:\(targetPort)"]
        }
        args.append(host)

        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/ssh")
        proc.arguments = args

        let errPipe = Pipe()
        proc.standardError = errPipe
        proc.standardOutput = FileHandle.nullDevice
        proc.standardInput = FileHandle.nullDevice

        try proc.run()
        sshProcesses[workspaceID] = proc

        AppLifecycleLog.info("spotlight", "started SSH tunnels for \(workspaceID): \(forwards.map { "\($0.0)→\($0.1)" }.joined(separator: ", "))")
    }

    /// Kill SSH tunnel process for a workspace.
    func stopSSHTunnels(workspaceID: String) {
        if let proc = sshProcesses.removeValue(forKey: workspaceID) {
            proc.terminate()
            AppLifecycleLog.info("spotlight", "stopped SSH tunnels for \(workspaceID)")
        }
    }

    /// Kill all SSH tunnels (app cleanup).
    func stopAll() {
        for (wsID, proc) in sshProcesses {
            proc.terminate()
            AppLifecycleLog.info("spotlight", "stopped SSH tunnels for \(wsID)")
        }
        sshProcesses.removeAll()
    }
}
