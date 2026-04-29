import NexusCore
import SwiftUI
import Foundation
import Darwin

private func nexusCrashSignalHandler(_ sig: Int32) {
    CrashProbe.handleSignal(sig)
}

private func nexusUncaughtExceptionHandler(_ exception: NSException) {
    CrashProbe.handleException(exception)
}

private enum CrashProbe {
    private static var logFD: Int32 = -1
    private static let lock = NSLock()

    static func install() {
        lock.lock()
        defer { lock.unlock() }
        if logFD >= 0 { return }

        let path = logPath()
        let dir = (path as NSString).deletingLastPathComponent
        try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        logFD = open(path, O_CREAT | O_APPEND | O_WRONLY, 0o644)
        if logFD < 0 {
            logFD = open("/tmp/nexusapp-crash-probe.log", O_CREAT | O_APPEND | O_WRONLY, 0o644)
        }
        writeLine("[BOOT] crash-probe installed pid=\(getpid())")

        NSSetUncaughtExceptionHandler(nexusUncaughtExceptionHandler)

        // Do not trap SIGPIPE as fatal: macOS frameworks can legitimately emit it.
        for sig in [SIGABRT, SIGILL, SIGSEGV, SIGFPE, SIGBUS, SIGTRAP] {
            signal(sig, nexusCrashSignalHandler)
        }
    }

    static func handleSignal(_ sig: Int32) {
        writeSignal(sig)
        signal(sig, SIG_DFL)
        raise(sig)
    }

    static func handleException(_ exception: NSException) {
        writeRaw("[EXC] uncaught exception: \(exception.name.rawValue) reason=\(exception.reason ?? "")")
    }

    static func checkpoint(_ message: String) {
        writeLine("[CHK] \(message)")
    }

    private static func writeSignal(_ sig: Int32) {
        writeRaw("[SIG] pid=\(getpid()) signal=\(sig)\n")
    }

    private static func writeLine(_ line: String) {
        writeRaw(line + "\n")
    }

    private static func writeRaw(_ text: String) {
        guard logFD >= 0, let data = text.data(using: .utf8) else { return }
        data.withUnsafeBytes { ptr in
            _ = Darwin.write(logFD, ptr.baseAddress, ptr.count)
        }
        _ = fsync(logFD)
    }

    private static func logPath() -> String {
        let configHome = ProcessInfo.processInfo.environment["XDG_CONFIG_HOME"]
            ?? "\(ProcessInfo.processInfo.environment["HOME"] ?? NSHomeDirectory())/.config"
        return "\(configHome)/nexus/run/nexusapp-crash-probe.log"
    }
}

@main
struct NexusApp: App {
    @StateObject private var appState: AppState

    init() {
        CrashProbe.install()
        CrashProbe.checkpoint("NexusApp init begin")
        _appState = StateObject(wrappedValue: AppState())
        CrashProbe.checkpoint("NexusApp init AppState ready")
    }

    var body: some Scene {
        WindowGroup {
            ContentView()
                .environmentObject(appState)
                .preferredColorScheme(.light)
                .onAppear {
                    CrashProbe.checkpoint("NexusApp window appeared")
                }
        }
        .windowToolbarStyle(.unified(showsTitle: false))
        .commands {
            CommandGroup(replacing: .newItem) {
                Button("New Project") {
                    appState.createIntent = .newProject
                    appState.showNewWorkspace = true
                }
                    .keyboardShortcut("n", modifiers: .command)
            }
        }
    }
}
