import Foundation

// Response types for RPC calls (NOT in NexusRPC.swift)
struct DiscoveredPort: Codable {
    let localPort: Int
    let remotePort: Int
    let service: String?
    let `protocol`: String?
    let source: String?
}

struct DiscoverPortsResponse: Codable {
    let ports: [DiscoveredPort]
}

struct SpotlightStartResponse: Codable {
    let forward: NexusForward
}

struct SpotlightStopResponse: Codable {
    let closed: Bool
}

actor SpotlightManager {
    private let client: any DaemonClient
    private var sshProcesses: [String: Process] = [:]  // key: workspaceID

    init(client: any DaemonClient) {
        self.client = client
    }

    /// Discover forwarded ports for a workspace (same as `workspace.discover-ports` RPC)
    func discoverPorts(workspaceID: String) async throws -> [DiscoveredPort] {
        let result = try await client.call(NexusMethod.workspaceDiscoverPorts, params: ["id": workspaceID])
        guard let dict = result as? [String: Any],
              let portsArr = dict["ports"] as? [Any] else {
            return []
        }
        let data = try JSONSerialization.data(withJSONObject: portsArr)
        let response = try JSONDecoder().decode(DiscoverPortsResponse.self, from: data)
        return response.ports
    }

    /// Start spotlight forwards on the daemon side
    func startSpotlight(workspaceID: String, ports: [DiscoveredPort]) async throws -> [NexusForward] {
        var forwards: [NexusForward] = []
        for port in ports {
            let specDict: [String: Any] = [
                "workspaceId": workspaceID,
                "localPort": port.localPort,
                "remotePort": port.remotePort,
                "protocol": port.protocol as Any,
                "source": port.source as Any,
            ]
            let result = try await client.call(NexusMethod.spotlightStart, params: [
                "workspaceId": workspaceID,
                "spec": specDict,
            ])
            guard let dict = result as? [String: Any],
                  let fwd = dict["forward"] else {
                continue
            }
            let data = try JSONSerialization.data(withJSONObject: fwd)
            let response = try JSONDecoder().decode(SpotlightStartResponse.self, from: data)
            forwards.append(response.forward)
        }
        return forwards
    }

    /// Stop spotlight on daemon side
    func stopSpotlight(workspaceID: String) async throws {
        _ = try await client.call(NexusMethod.spotlightStop, params: ["workspaceId": workspaceID])
    }

    /// Start SSH port forwarding tunnels using system `ssh` command
    /// - Parameters:
    ///   - host: SSH target host
    ///   - sshPort: SSH port (default 22)
    ///   - identityFile: path to SSH identity file (e.g. ~/.ssh/id_ed25519)
    ///   - forwards: list of NexusForward from daemon, each with localPort/remotePort
    func startSSHTunnels(workspaceID: String, host: String, sshPort: Int, identityFile: String, forwards: [NexusForward]) async throws {
        var args = ["-N", "-o", "StrictHostKeyChecking=accept-new", "-o", "ExitOnForwardFailure=yes"]
        args += ["-p", String(sshPort)]
        args += ["-i", identityFile]
        for f in forwards {
            args += ["-L", "\(f.localPort):127.0.0.1:\(f.remotePort)"]
        }
        args.append(host)

        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/ssh")
        proc.arguments = args

        // Redirect stderr to a pipe for error logging
        let errPipe = Pipe()
        proc.standardError = errPipe
        proc.standardOutput = FileHandle.nullDevice
        proc.standardInput = FileHandle.nullDevice

        try proc.run()

        // Store process so we can kill it later
        sshProcesses[workspaceID] = proc

        // Log
        AppLifecycleLog.info("spotlight", "started SSH tunnels for \(workspaceID): \(forwards.map { "\($0.localPort)→\($0.remotePort)" }.joined(separator: ", "))")
    }

    /// Kill SSH tunnels for a workspace
    func stopSSHTunnels(workspaceID: String) {
        if let proc = sshProcesses.removeValue(forKey: workspaceID) {
            proc.terminate()
            AppLifecycleLog.info("spotlight", "stopped SSH tunnels for \(workspaceID)")
        }
    }

    /// Stop all (for app cleanup)
    func stopAll() {
        for (wsID, proc) in sshProcesses {
            proc.terminate()
            AppLifecycleLog.info("spotlight", "stopped SSH tunnels for \(wsID)")
        }
        sshProcesses.removeAll()
    }
}
