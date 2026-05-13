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
    /// Optional explicit SSH identity path. If nil, auto-detected from ~/.ssh/.
    public var sshIdentity: String?
    /// Security-scoped bookmark for the SSH identity file. Created via NSOpenPanel so
    /// the app can re-open the file after relaunches even under full App Sandbox.
    public var sshIdentityBookmark: Data?
    /// Security-scoped bookmark for ~/.ssh/config. Created via NSOpenPanel so the app
    /// can write the Include directive even under full App Sandbox (TestFlight).
    public var sshConfigBookmark: Data?
    /// Always ~/.ssh/config (app has read-write entitlement for ~/.ssh/).
    public var sshConfigPath: String { sshDir + "/config" }
    /// When true, connect directly to a local WebSocket (no SSH tunnel).
    public var isLocal: Bool
    /// SSH directory (always ~/.ssh/ — entitlement grants read-write access).
    public var sshDir: String { Self.sshDir() }

    private static func sshDir() -> String {
        // Use getpwuid to reliably get the real home directory under sandbox.
        // NSHomeDirectoryForUser may return nil or the container home.
        let home: String = {
            let uid = getuid()
            if let pw = getpwuid(uid), let dir = pw.pointee.pw_dir {
                return String(cString: dir)
            }
            return NSHomeDirectory()
        }()
        return (home as NSString).appendingPathComponent(".ssh")
    }

    /// Auto-detect identity from ~/.ssh/ if not explicitly set.
    public func resolvedIdentity() -> String? {
        if let id = sshIdentity?.trimmingCharacters(in: .whitespacesAndNewlines), !id.isEmpty {
            return id
        }
        let dir = sshDir
        for name in ["id_ed25519", "id_rsa"] {
            let path = (dir as NSString).appendingPathComponent(name)
            if FileManager.default.fileExists(atPath: path) { return path }
        }
        return nil
    }

    public init(
        profileId: String = UUID().uuidString,
        name: String,
        port: Int = 7777,
        isDefault: Bool = false,
        lastKnownStatus: ProfileStatus = .unknown,
        sshTarget: String? = nil,
        sshPort: Int? = nil,
        sshIdentity: String? = nil,
        sshIdentityBookmark: Data? = nil,
        sshConfigBookmark: Data? = nil,
        isLocal: Bool = false
    ) {
        self.profileId = profileId
        self.name = name
        self.port = port
        self.isDefault = isDefault
        self.lastKnownStatus = lastKnownStatus
        self.sshTarget = sshTarget
        self.sshPort = sshPort
        self.sshIdentity = sshIdentity
        self.sshIdentityBookmark = sshIdentityBookmark
        self.sshConfigBookmark = sshConfigBookmark
        self.isLocal = isLocal
    }

    private enum CodingKeys: String, CodingKey {
        case profileId, name, port, isDefault, lastKnownStatus, sshTarget, sshPort, isLocal
        case sshIdentity, sshIdentityBookmark, sshConfigBookmark
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

        sshIdentityBookmark = try container.decodeIfPresent(Data.self, forKey: .sshIdentityBookmark)
        sshConfigBookmark = try container.decodeIfPresent(Data.self, forKey: .sshConfigBookmark)
        isLocal = try container.decodeIfPresent(Bool.self, forKey: .isLocal) ?? false
    }

    private static func clampPort(_ value: Int, fallback: Int) -> Int {
        if (1...65535).contains(value) { return value }
        return fallback
    }

    private static func validPortOrNil(_ value: Int) -> Int? {
        (1...65535).contains(value) ? value : nil
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

    /// Preset for the macOS app's bundled / loopback Nexus daemon.
    public static func localDefault() -> DaemonProfile {
        DaemonProfile(
            profileId: "local-default",
            name: "Local",
            port: 63987,
            isDefault: false,
            lastKnownStatus: .unknown,
            isLocal: true
        )
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
}
