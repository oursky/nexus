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

    private enum CodingKeys: String, CodingKey {
        case profileId, name, port, isDefault, lastKnownStatus, sshTarget, sshPort, sshIdentity
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        profileId = try container.decodeIfPresent(String.self, forKey: .profileId) ?? UUID().uuidString
        name = (try container.decodeIfPresent(String.self, forKey: .name) ?? "").trimmingCharacters(in: .whitespacesAndNewlines)

        let decodedPort = try container.decodeIfPresent(Int.self, forKey: .port) ?? 7777
        port = DaemonProfile.clampPort(decodedPort, fallback: 7777)
        isDefault = try container.decodeIfPresent(Bool.self, forKey: .isDefault) ?? false
        lastKnownStatus = try container.decodeIfPresent(ProfileStatus.self, forKey: .lastKnownStatus) ?? .unknown

        let target = (try container.decodeIfPresent(String.self, forKey: .sshTarget) ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        sshTarget = target.isEmpty ? nil : target

        if let p = try container.decodeIfPresent(Int.self, forKey: .sshPort) {
            sshPort = DaemonProfile.validPortOrNil(p)
        } else {
            sshPort = nil
        }
        let identity = (try container.decodeIfPresent(String.self, forKey: .sshIdentity) ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        sshIdentity = identity.isEmpty ? nil : identity
    }

    private static func clampPort(_ value: Int, fallback: Int) -> Int {
        if (1...65535).contains(value) { return value }
        return fallback
    }

    private static func validPortOrNil(_ value: Int) -> Int? {
        (1...65535).contains(value) ? value : nil
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
        do {
            let profiles = try decoder.decode([DaemonProfile].self, from: data)
            AppLifecycleLog.info("profile-store", "loaded profiles count=\(profiles.count)")
            return profiles
        } catch {
            AppLifecycleLog.error("profile-store", "decode failed: \(error.localizedDescription)")
            return []
        }
    }

    public func save(_ profiles: [DaemonProfile]) {
        guard let data = try? encoder.encode(profiles) else {
            AppLifecycleLog.error("profile-store", "encode failed count=\(profiles.count)")
            return
        }
        defaults.set(data, forKey: key)
        AppLifecycleLog.info("profile-store", "saved profiles count=\(profiles.count)")
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
