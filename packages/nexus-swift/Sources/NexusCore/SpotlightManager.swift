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

    /// Callback to open a port-forward tunnel via Citadel instead of spawning child ssh.
    /// Set by AppState when connecting — bridges to SSHTunnelManager.openSpotlightTunnel().
    var openTunnel: ((_ localPort: Int, _ remotePort: Int, _ timeout: TimeInterval) async throws -> Int)?

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

    /// Start daemon-side port forwarding tunnels via Citadel instead of child ssh.
    /// Returns the tunnel status and the list of (localPort, targetPort) pairs.
    /// NOTE: `identityFile` is accepted for API compatibility but IGNORED —
    /// authentication is handled by the tunnel manager (ssh-agent or Citadel in-process key).
    func startDaemonTunnels(workspaceID: String, host: String, sshPort: Int, identityFile: String) async throws {
        let (_, forwards) = try await client.startTunnels(workspaceId: workspaceID)

        guard let openTunnel = openTunnel else {
            throw TunnelError.noTarget
        }

        // Open a Citadel tunnel for each forward. The tunnel manager reuses SSH connections
        // via spotlightTunnels cache and validates each with a TCP readiness probe.
        var opened: [Int] = []
        for (localPort, targetPort) in forwards {
            do {
                let port = try await openTunnel(localPort, targetPort, 5)
                opened.append(port)
            } catch {
                // Clean up already-opened tunnels on failure
                for port in opened {
                    _ = try? await openTunnel(port, 0, 0) // force close via reuse detection will clean stale
                }
                throw error
            }
        }

        // Track workspace for cleanup (store local ports instead of Process handles)
        sshProcesses[workspaceID] = Process() // placeholder — actual lifecycle managed by SSHTunnelManager

        AppLifecycleLog.info("spotlight", "started \(opened.count) Citadel tunnels for \(workspaceID): \(forwards.map { "\($0.0)→\($0.1)" }.joined(separator: ", "))")
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
