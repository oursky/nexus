# nexus-swift

Native macOS client for Nexus — SwiftUI, macOS 14+.

See `[ROADMAP.md](../../ROADMAP.md#native-macos-client-swiftui)` for milestones and architecture.

## Quick start

**Full contributor loop (remote Linux daemon + Xcode app)** — from the **repository root**, with `REMOTE_HOST` in `.env.local`:

```bash
task dev:swift   # deploy linux/amd64 + regenerate Swift RPC + xcodebuild + open NexusApp
```

Use `nexus daemon connect <user@host>` on the Mac so the app has a profile with `sshTarget` and token; the app does not read daemon connection settings from environment variables.

**SwiftPM only** (no Xcode deploy step) — useful for quick UI iteration when the daemon side is unchanged:

```bash
cd packages/nexus-swift
swift run   # default daemon profile — SSH tunnel + remote WebSocket
```

If the Xcode build warns that `Resources/nexus-daemon` is missing, stage a macOS build of the CLI for the embedder (see [Contributing](../../CONTRIBUTING.md), macOS app section).

## Crash debugging in Xcode (required for app-start crashes)

When the app crashes before headless RPC is reachable, debug in Xcode with breakpoints first.

1. Open `packages/nexus-swift/NexusApp.xcodeproj` and run the `NexusApp` scheme in `Debug`.
2. Scheme → Run → Arguments → Environment:
   - `NEXUS_HEADLESS_RPC=1`
   - `NEXUS_APP_FILE_LOG=1`
3. Load the shared LLDB breakpoint preset:
   - Xcode console: `command source packages/nexus-swift/scripts/lldb/crash-breakpoints.lldb`
4. Scheme → Run → Diagnostics:
   - Enable `Address Sanitizer`
   - Enable `Zombie Objects`
5. Reproduce the crash using the same flow (e.g. profile save / reconnect / test connection).
6. Capture:
   - Stopped thread backtrace in Xcode (`bt`)
   - `~/.config/nexus/run/nexusapp.log`
   - `~/.config/nexus/run/nexusapp-crash-probe.log`

Notes:
- `nexusapp.log` is best-effort app lifecycle logging; hard traps can bypass normal log flushing.
- `nexusapp-crash-probe.log` is low-level crash-probe output and should be inspected alongside Xcode backtraces.

## Structure

```
Sources/NexusApp/
├── NexusApp.swift           # @main entry, window + commands
├── Theme.swift              # Design tokens (colors, fonts, spacing)
├── Models/                  # App-local models if any
└── Views/
    ├── ContentView.swift        # NavigationSplitView root
    ├── SidebarView.swift        # Repo › Workspace list, ⌘N button
    └── …
Sources/NexusCore/           # Shared app logic, `AppState`, daemon client, models
```

## Design principles

- **Remote daemon only**: profile store + SSH tunnel to the Linux host; use `MockDaemonClient` / `AppState(client:)` in unit tests
- **Theme parity**: `Theme.swift` tokens match the Tauri experiment's CSS variables
- **Two build paths**: SwiftPM (`swift build` / `swift run`) for fast iteration; the checked-in Xcode project is the canonical app bundle path for contributor workflows (`task dev:swift`)
- **MVVM**: Views read from `AppState`; mutations go through `AppState` APIs
