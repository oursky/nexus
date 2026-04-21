import Foundation

/// Mac-side git worktree + exclude file updates matching `nexus workspace fork` CLI behavior.
public enum ForkLocalMacSetup {
    public static func ensureGitExclude(gitRoot: String, entry: String) throws {
        let root = gitRoot.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !root.isEmpty else { return }
        let exclude = URL(fileURLWithPath: root).appendingPathComponent(".git/info/exclude").path
        try FileManager.default.createDirectory(
            atPath: (exclude as NSString).deletingLastPathComponent,
            withIntermediateDirectories: true
        )
        var existing = (try? String(contentsOfFile: exclude, encoding: .utf8)) ?? ""
        for line in existing.split(separator: "\n") {
            if line.trimmingCharacters(in: .whitespaces) == entry { return }
        }
        if !existing.isEmpty && !existing.hasSuffix("\n") { existing += "\n" }
        existing += "\n# nexus fork worktrees\n\(entry)\n"
        try existing.write(toFile: exclude, atomically: true, encoding: .utf8)
    }

    /// Runs `git worktree add` from `gitRoot`. Removes stale non-worktree directories at `worktreePath`.
    public static func gitWorktreeAdd(gitRoot: String, worktreePath: String, ref: String) throws {
        let gr = gitRoot.trimmingCharacters(in: .whitespacesAndNewlines)
        let wt = worktreePath.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !gr.isEmpty, !wt.isEmpty else {
            throw NSError(domain: "ForkLocalMacSetup", code: 1, userInfo: [NSLocalizedDescriptionKey: "empty path"])
        }
        var isDir: ObjCBool = false
        if FileManager.default.fileExists(atPath: wt, isDirectory: &isDir), isDir.boolValue {
            let check = Process()
            check.executableURL = URL(fileURLWithPath: "/usr/bin/git")
            check.arguments = ["rev-parse", "--is-inside-work-tree"]
            check.currentDirectoryURL = URL(fileURLWithPath: wt)
            check.standardOutput = Pipe()
            check.standardError = Pipe()
            try? check.run()
            check.waitUntilExit()
            if check.terminationStatus == 0 { return }
            try FileManager.default.removeItem(atPath: wt)
        }
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/git")
        // `git worktree add <path> <ref>` requires an existing ref. For a new branch name, use `-b <name> <path> <start>`.
        if Self.gitRefResolves(gitRoot: gr, ref: ref) {
            proc.arguments = ["-C", gr, "worktree", "add", wt, ref]
        } else {
            proc.arguments = ["-C", gr, "worktree", "add", "-b", ref, wt, "HEAD"]
        }
        let err = Pipe()
        proc.standardError = err
        proc.standardOutput = Pipe()
        try proc.run()
        proc.waitUntilExit()
        if proc.terminationStatus != 0 {
            let msg = String(data: err.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? "git worktree failed"
            throw NSError(domain: "ForkLocalMacSetup", code: 2, userInfo: [NSLocalizedDescriptionKey: msg])
        }
    }

    private static func gitRefResolves(gitRoot: String, ref: String) -> Bool {
        let r = ref.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !r.isEmpty else { return false }
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/git")
        proc.arguments = ["-C", gitRoot, "rev-parse", "--verify", r]
        proc.standardOutput = Pipe()
        proc.standardError = Pipe()
        guard (try? proc.run()) != nil else { return false }
        proc.waitUntilExit()
        return proc.terminationStatus == 0
    }
}
