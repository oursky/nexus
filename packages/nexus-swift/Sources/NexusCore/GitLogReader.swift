import Foundation

/// Runs `git log` locally (Mac-side repo). Used by the sidebar log pane; the daemon has no `exec` RPC.
public enum GitLogReader {
    public static func recentLogLines(repoDirectory: String, limit: Int = 25) throws -> String {
        let trimmed = repoDirectory.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            throw NSError(domain: "GitLogReader", code: 1, userInfo: [NSLocalizedDescriptionKey: "Empty repo path"])
        }
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/git")
        proc.arguments = ["-C", trimmed, "log", "--format=%h\t%s\t%an\t%ar", "-\(limit)"]
        let out = Pipe()
        let err = Pipe()
        proc.standardOutput = out
        proc.standardError = err
        try proc.run()
        proc.waitUntilExit()
        let errData = err.fileHandleForReading.readDataToEndOfFile()
        if proc.terminationStatus != 0 {
            let msg = String(data: errData, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? "git failed"
            throw NSError(domain: "GitLogReader", code: Int(proc.terminationStatus), userInfo: [NSLocalizedDescriptionKey: msg])
        }
        let data = out.fileHandleForReading.readDataToEndOfFile()
        return String(data: data, encoding: .utf8) ?? ""
    }
}
