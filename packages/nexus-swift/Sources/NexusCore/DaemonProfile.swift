import Foundation

public enum ProfileStatus: String, Codable, Equatable {
    case unknown
    case connected
    case unreachable
    case authFailed
    case tlsError
    case protocolMismatch
}

public struct DaemonProfile: Codable, Equatable, Identifiable, Sendable {
    public var id: String { profileId }
    public var profileId: String
    public var name: String
    public var port: Int
    public var isDefault: Bool
    public var lastKnownStatus: ProfileStatus
    public var sshTarget: String?
    public var sshPort: Int?
    public var sshIdentity: String?

    public init(
        profileId: String = UUID().uuidString,
        name: String,
        port: Int = 7777,
        isDefault: Bool = false,
        lastKnownStatus: ProfileStatus = .unknown,
        sshTarget: String? = nil,
        sshPort: Int? = nil,
        sshIdentity: String? = nil
    ) {
        self.profileId = profileId
        self.name = name
        self.port = port
        self.isDefault = isDefault
        self.lastKnownStatus = lastKnownStatus
        self.sshTarget = sshTarget
        self.sshPort = sshPort
        self.sshIdentity = sshIdentity
    }
}

public final class DaemonProfileStore {
    private let defaults: UserDefaults
    private let key = "nexus.daemonProfiles"
    private let encoder = JSONEncoder()
    private let decoder = JSONDecoder()

    public init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
    }

    public func load() -> [DaemonProfile] {
        guard let data = defaults.data(forKey: key) else { return [] }
        return (try? decoder.decode([DaemonProfile].self, from: data)) ?? []
    }

    public func save(_ profiles: [DaemonProfile]) {
        guard let data = try? encoder.encode(profiles) else { return }
        defaults.set(data, forKey: key)
    }

    public func defaultProfile() -> DaemonProfile? {
        load().first { $0.isDefault }
    }

    public static func remoteDefault() -> DaemonProfile {
        DaemonProfile(
            profileId: "remote-default",
            name: "Remote",
            port: 7777,
            isDefault: true,
            lastKnownStatus: .unknown
        )
    }
}
