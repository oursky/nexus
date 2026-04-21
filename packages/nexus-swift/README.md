# nexus-swift

Native macOS client for Nexus — SwiftUI, macOS 14+.

See `[ROADMAP.md](../../ROADMAP.md#native-macos-client-swiftui)` for milestones and architecture.

## Quick start

```bash
# Build / run (connects using the default daemon profile — SSH tunnel + remote WebSocket)
swift run
```

Use `nexus daemon connect <user@host>` on the Mac so the app has a profile with `sshTarget` and token; the app does not read daemon connection settings from environment variables.

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
- **No Xcode required**: SPM-only; Xcode is optional for debugging
- **MVVM**: Views read from `AppState`; mutations go through `AppState` APIs

