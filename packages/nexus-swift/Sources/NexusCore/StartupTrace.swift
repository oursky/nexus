import Foundation
import os

/// Writes a line-oriented trace to `~/.config/nexus/run/app-startup-trace.log`
/// and mirrors checkpoints to the unified log (Console.app: subsystem `com.nexus.NexusApp`, category `startup-trace`).
///
/// **Durability:** Lines are written **synchronously** (no `DispatchQueue.async`) and `FileHandle.synchronize()` runs
/// after each append so a force-quit or SIGKILL often still leaves the **last checkpoint** on disk. Async logging
/// would buffer in memory and produce **no useful file** after kill.
///
/// **How to use after a hang:** `tail -100 ~/.config/nexus/run/app-startup-trace.log` — the last line is the last
/// completed step; the next step in code is where execution stuck.
///
/// Disable file output: `NEXUS_STARTUP_TRACE=0`.
/// Per-RPC lines (noisy; off by default): `NEXUS_STARTUP_TRACE_RPC=1`.
public enum StartupTrace {
    private static let logger = Logger(subsystem: "com.nexus.NexusApp", category: "startup-trace")
    private static let lock = NSLock()

    private static var fileEnabled = true
    /// When false, `rpc()` returns immediately with no lock (hot path stays allocation-free).
    private static var logRPC = false
    private static var logPath: String = ""

    /// Truncate the trace file and write a session header. Call once per process from `AppState.init`.
    public static func beginSession() {
        lock.lock()
        defer { lock.unlock() }
        fileEnabled = ProcessInfo.processInfo.environment["NEXUS_STARTUP_TRACE"] != "0"
        logRPC = ProcessInfo.processInfo.environment["NEXUS_STARTUP_TRACE_RPC"] == "1"
        guard fileEnabled else {
            logPath = ""
            return
        }

        let dir = Self.resolveRunDir()
        logPath = "\(dir)/app-startup-trace.log"
        try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)

        let pid = ProcessInfo.processInfo.processIdentifier
        let wall = ISO8601DateFormatter().string(from: Date())
        let header = "--- NexusApp startup trace session wall=\(wall) pid=\(pid) ---\nfile=\(logPath)\n"
        FileManager.default.createFile(atPath: logPath, contents: Data(header.utf8), attributes: nil)
        if let h = FileHandle(forWritingAtPath: logPath) {
            defer { try? h.close() }
            try? h.synchronize()
        }
    }

    /// Record a checkpoint. `code` should be stable (grep-friendly); `detail` is optional context.
    public static func checkpoint(_ code: String, _ detail: String = "") {
        lock.lock()
        defer { lock.unlock() }
        guard fileEnabled, !logPath.isEmpty else { return }
        let line = formatLine(prefix: "CHK", code: code, detail: detail)
        logger.info("\(line, privacy: .public)")
        appendToFileSynchronouslyNoLock(line + "\n")
    }

    /// Per-RPC trace line. **Off by default** — enable `NEXUS_STARTUP_TRACE_RPC=1` to avoid lock/file I/O on every RPC.
    public static func rpc(method: String) {
        guard logRPC else { return }
        lock.lock()
        defer { lock.unlock() }
        guard fileEnabled, !logPath.isEmpty else { return }
        let line = formatLine(prefix: "RPC", code: method, detail: "")
        logger.info("\(line, privacy: .public)")
        appendToFileSynchronouslyNoLock(line + "\n")
    }

    private static func formatLine(prefix: String, code: String, detail: String) -> String {
        let t = String(format: "%.3f", Date().timeIntervalSince1970)
        if detail.isEmpty {
            return "[\(prefix)] \(t) \(code)"
        }
        return "[\(prefix)] \(t) \(code) — \(detail)"
    }

    private static func resolveRunDir() -> String {
        let configHome = ProcessInfo.processInfo.environment["XDG_CONFIG_HOME"]
            ?? "\(ProcessInfo.processInfo.environment["HOME"] ?? NSHomeDirectory())/.config"
        return "\(configHome)/nexus/run"
    }

    private static func appendToFileSynchronouslyNoLock(_ utf8: String) {
        guard let data = utf8.data(using: .utf8) else { return }
        guard let handle = FileHandle(forWritingAtPath: logPath) else { return }
        defer { try? handle.close() }
        do {
            try handle.seekToEnd()
            try handle.write(contentsOf: data)
            try handle.synchronize()
        } catch {
            // Best-effort tracing; avoid throwing into callers.
        }
    }
}
