# Nexus TUI — formal specification (implemented)

This document is the reference for TUI behavior verified by `test/e2e/tui/tui_spec_test.go`.

## B.1 — Daemon connection

- **B.1.1** When the CLI connects to the daemon WebSocket successfully, the header shows `connected`.
- **B.1.2** When connection fails (invalid URL, refused, auth), the header shows `disconnected` or `degraded` and an error line is visible.

## B.2 — Workspace list and lifecycle

- **B.2.1** After `workspace.create`, the main list shows the workspace name or id.
- **B.2.2** From the detail view, `s` issues `workspace.start` for that workspace.
- **B.2.3** From the detail view, `x` issues `workspace.stop`.
- **B.2.4** Delete: `d` then `Y` issues `workspace.remove`.
- **B.2.5** Delete cancelled: `d` then `n` or `Esc` leaves the workspace intact.

## B.4 — Spotlight panel

- **B.4.1** From detail view, `l` opens the spotlight panel listing `spotlight.list` forwards.

## B.5 — Sync panel

- **B.5.1** From detail view, `y` opens the sync panel listing `workspace.sync-list` sessions.

## C.1 — Main list chrome

- **C.1.1** The workspace list shows a title/header row (`Workspaces`).

## D.1 — Global keys

- **D.1.1** `q` exits with code 0.
- **D.1.2** `?` toggles or shows a help overlay.
- **D.1.3** `/` activates filter mode on the list (filtering enabled).

## D.3 — Fork

- **D.3.1** From detail view, `f` shows a fork prompt (child workspace name).
