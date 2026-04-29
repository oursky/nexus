import Foundation
import os

public enum AppLifecycleLogLevel: String {
    case info = "INFO"
    case warn = "WARN"
    case error = "ERROR"
}

/// Durable app lifecycle log to support post-crash diagnosis from disk.
/// File location: ~/.config/nexus/run/nexusapp.log
public enum AppLifecycleLog {
    private static let logger = Logger(subsystem: "com.nexus.NexusApp", category: "lifecycle")
    private static let lock = NSLock()
    private static var enabled = true
    private static var logPath = ""

    public static func configure() {
        lock.lock()
        defer { lock.unlock() }
        enabled = ProcessInfo.processInfo.environment["NEXUS_APP_FILE_LOG"] != "0"
        guard enabled else {
            logPath = ""
            return
        }

        let dir = resolveRunDir()
        logPath = "\(dir)/nexusapp.log"
        try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        if !FileManager.default.fileExists(atPath: logPath) {
            _ = FileManager.default.createFile(atPath: logPath, contents: nil)
        }
        let wall = ISO8601DateFormatter().string(from: Date())
        let pid = ProcessInfo.processInfo.processIdentifier
        appendNoLock("--- session wall=\(wall) pid=\(pid) ---\n")
    }

    public static func info(_ category: String, _ message: String) {
        write(.info, category, message)
    }

    public static func warn(_ category: String, _ message: String) {
        write(.warn, category, message)
    }

    public static func error(_ category: String, _ message: String) {
        write(.error, category, message)
    }

    private static func write(_ level: AppLifecycleLogLevel, _ category: String, _ message: String) {
        lock.lock()
        defer { lock.unlock() }
        let sanitized = message.replacingOccurrences(of: "\n", with: "\\n")
        let ts = String(format: "%.3f", Date().timeIntervalSince1970)
        let line = "[\(level.rawValue)] \(ts) [\(category)] \(sanitized)"
        switch level {
        case .info:
            logger.info("\(line, privacy: .public)")
        case .warn:
            logger.warning("\(line, privacy: .public)")
        case .error:
            logger.error("\(line, privacy: .public)")
        }
        guard enabled, !logPath.isEmpty else { return }
        appendNoLock(line + "\n")
    }

    private static func appendNoLock(_ text: String) {
        guard let data = text.data(using: .utf8),
              let handle = FileHandle(forWritingAtPath: logPath) else { return }
        defer { try? handle.close() }
        do {
            try handle.seekToEnd()
            try handle.write(contentsOf: data)
            try handle.synchronize()
        } catch {
            // Avoid cascading failures from logging.
        }
    }

    private static func resolveRunDir() -> String {
        let configHome = ProcessInfo.processInfo.environment["XDG_CONFIG_HOME"]
            ?? "\(ProcessInfo.processInfo.environment["HOME"] ?? NSHomeDirectory())/.config"
        return "\(configHome)/nexus/run"
    }
}
