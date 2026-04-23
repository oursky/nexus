import Foundation

/// One entry in a remote directory listing (SSH to the engine host).
public struct RemoteListingEntry: Identifiable, Hashable, Sendable {
    public let name: String
    public let isDirectory: Bool
    public var id: String { name }

    public init(name: String, isDirectory: Bool) {
        self.name = name
        self.isDirectory = isDirectory
    }
}

/// Lists directories on the Linux daemon over SSH (same transport as the app's remote profile).
public enum EngineRemotePathBrowser {
    private static let markerPrefix = "__NEXUS_PATH__:"

    public enum BrowserError: LocalizedError {
        case noSSHTarget
        case remoteCommandFailed(String)

        public var errorDescription: String? {
            switch self {
            case .noSSHTarget:
                return "SSH target is not configured for this profile."
            case .remoteCommandFailed(let msg):
                return msg
            }
        }
    }

    /// Remote user home directory (`$HOME` on the engine).
    public static func remoteHome(profile: DaemonProfile) throws -> String {
        // echo doesn't have the quoting issues printf had.
        let script = "echo '\(markerPrefix)'\"$HOME\""
        let out = try runRemoteBash(profile: profile, script: script)
        guard let h = firstMarkedAbsoluteLine(out) else {
            throw BrowserError.remoteCommandFailed("Could not resolve home directory on the engine.")
        }
        return h
    }

    /// Lists immediate children of `path` on the engine.
    public static func listDirectory(path: String, profile: DaemonProfile) throws -> [RemoteListingEntry] {
        // Build a python3 script as a plain Swift string (no multiline literal — Python
        // indentation must be preserved at column 0, which conflicts with Swift multiline rules).
        let escapedPath = path.replacingOccurrences(of: "\\", with: "\\\\")
                             .replacingOccurrences(of: "'", with: "\\'")
        let pyLines = [
            "import os, sys",
            "p = '\(escapedPath)'",
            "if not os.path.isdir(p):",
            "    print('Not a directory: ' + p, file=sys.stderr)",
            "    sys.exit(2)",
            "names = sorted(os.listdir(p))",
            "for name in names:",
            // Skip hidden entries (matches plain `ls` behaviour — no -a flag).
            "    if name.startswith('.'): continue",
            // Skip non-UTF-8 names (os.listdir uses surrogate-escape on Linux).
            "    try: name.encode('utf-8')",
            "    except (UnicodeEncodeError, UnicodeDecodeError): continue",
            "    try:",
            "        full = os.path.join(p, name)",
            // Only emit directories — this is a project-directory picker, not a
            // general file browser. Filtering out files eliminates garbled binary
            // filenames left behind by archives / packages in the home directory.
            "        if not os.path.isdir(full): continue",
            "        sys.stdout.buffer.write(('d\\t' + name + '\\n').encode('utf-8'))",
            "    except OSError: continue",
        ]
        let pyScript = pyLines.joined(separator: "\n")

        // Base64-encode the python script so it's safe to embed in a shell command.
        let pyB64 = Data(pyScript.utf8).base64EncodedString()

        // Shell script: prefer python3, fall back to shell globbing.
        let qpath = "'" + path.replacingOccurrences(of: "'", with: "'\\''") + "'"
        let script = [
            "if command -v python3 >/dev/null 2>&1; then",
            "  python3 -c \"$(echo \(pyB64) | base64 -d)\"",
            "else",
            "  cd \(qpath) 2>/dev/null || { echo 'Not a directory: \(path)' >&2; exit 2; }",
            "  for fp in .* *; do",
            "    [ \"$fp\" = \".\" ] || [ \"$fp\" = \"..\" ] && continue",
            "    [ \"$fp\" = \".*\" ] || [ \"$fp\" = \"*\" ] && continue",
            "    if [ -d \"$fp\" ]; then printf 'd\\t%s\\n' \"$fp\"",
            "    else printf 'f\\t%s\\n' \"$fp\"; fi",
            "  done",
            "fi",
        ].joined(separator: "\n")

        let raw = try runRemoteBash(profile: profile, script: script)
        var entries: [RemoteListingEntry] = []
        for line in raw.split(whereSeparator: \.isNewline) {
            let s = String(line).trimmingCharacters(in: .whitespacesAndNewlines)
            guard !s.isEmpty else { continue }
            let parts = s.split(separator: "\t", maxSplits: 1, omittingEmptySubsequences: false)
            guard parts.count == 2 else { continue }
            let name = String(parts[1])
            guard !name.isEmpty, name != ".", name != ".." else { continue }
            entries.append(RemoteListingEntry(name: name, isDirectory: String(parts[0]) == "d"))
        }
        return entries
    }

    // MARK: - Helpers

    /// Returns the first marker-prefixed absolute path line, ignoring MOTD/login noise.
    private static func firstMarkedAbsoluteLine(_ out: String) -> String? {
        out.split(whereSeparator: \.isNewline)
            .compactMap { line -> String? in
                let s = String(line).trimmingCharacters(in: .whitespacesAndNewlines)
                guard let r = s.range(of: markerPrefix) else { return nil }
                let p = String(s[r.upperBound...]).trimmingCharacters(in: .whitespacesAndNewlines)
                return p.hasPrefix("/") ? p : nil
            }
            .first
    }

    /// Runs `script` on the remote engine via SSH.
    ///
    /// **Why base64:** When `ssh` is given multiple arguments after the hostname it joins
    /// them with spaces and the remote shell tokenises the result — so a multi-word script
    /// passed as a single Swift string argument still gets split by the remote shell, causing
    /// bash's `-c` option to receive only the first word.  Base64-encoding the script and
    /// piping it to `bash -l` completely sidesteps all quoting/tokenisation issues.
    private static func runRemoteBash(profile: DaemonProfile, script: String) throws -> String {
        guard let target = profile.sshTarget?.trimmingCharacters(in: .whitespacesAndNewlines), !target.isEmpty else {
            throw BrowserError.noSSHTarget
        }
        guard let scriptData = script.data(using: .utf8) else {
            throw BrowserError.remoteCommandFailed("Script encoding failed.")
        }
        let b64 = scriptData.base64EncodedString()
        // Pass as a single SSH argument so the remote shell receives it as one command.
        let remoteCmd = "echo \(b64) | base64 -d | bash -l"

        var args: [String] = []
        let cfg = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".nexus/ssh/nexus.ssh.config", isDirectory: false).path
        if FileManager.default.fileExists(atPath: cfg) {
            args += ["-F", cfg]
        }
        args += ["-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=no"]
        if let port = profile.sshPort, port != 22 {
            args.insert(contentsOf: ["-p", "\(port)"], at: 0)
        }
        if let identity = profile.sshIdentity, !identity.isEmpty {
            args += ["-i", identity]
        }
        args += [target, remoteCmd]

        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/ssh")
        proc.arguments = args
        let outPipe = Pipe()
        let errPipe = Pipe()
        proc.standardOutput = outPipe
        proc.standardError = errPipe
        try proc.run()
        proc.waitUntilExit()
        let outStr = String(data: outPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
        let errStr = (String(data: errPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? "")
            .trimmingCharacters(in: .whitespacesAndNewlines)
        guard proc.terminationStatus == 0 else {
            let msg = errStr.isEmpty ? outStr.trimmingCharacters(in: .whitespacesAndNewlines) : errStr
            throw BrowserError.remoteCommandFailed(msg.isEmpty ? "SSH command failed (exit \(proc.terminationStatus))." : msg)
        }
        return outStr
    }
}
