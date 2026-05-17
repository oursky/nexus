# Nexus System Specification — Chapter 09: Terminal User Interface (TUI)

> **Status**: Normative  
> **Module**: `github.com/oursky/nexus/packages/nexus`

---

## Overview — `TUI-001`–`TUI-005`

**`TUI-001`** — The TUI is a Bubble Tea (`charmbracelet/bubbletea`) application providing an
interactive terminal interface for workspace management, spotlight control, and PTY sessions.

**`TUI-002`** — The TUI follows the Model-Update-View (MUV) pattern: each screen has a `Model`
that holds state, an `Update(msg tea.Msg)` function that handles messages, and a `View()`
function that returns a string rendered by the Bubble Tea runtime.

**`TUI-003`** — The TUI communicates with the daemon via the same RPC transport as the CLI:
Unix socket for local connections, WebSocket mux for remote connections requiring push
notifications (PTY data, server events).

**`TUI-004`** — The TUI is launched via `nexus tui` (or implicitly when `nexus` is run without
subcommands, if configured). It runs fullscreen, intercepting all keyboard input.

**`TUI-005`** — The TUI MUST gracefully handle daemon disconnection: display an offline indicator,
queue user actions where possible, and attempt reconnection with exponential backoff.

---

## View Types — `TUI-010`–`TUI-018`

**`TUI-010`** — **Dashboard** — The primary view displaying a list of all workspaces with
columns: name, state, project, backend, and age. This is the default view on TUI startup.

**`TUI-011`** — **Onramp** — A guided creation flow for new workspaces. Collects repository URL,
name, ref, and backend selection through a step-by-step form.

**`TUI-012`** — **Detail** — A workspace detail view showing full metadata, state history,
active ports, PTY sessions, and available actions (start, stop, fork, remove, shell).

**`TUI-013`** — **Spotlight** — Port forwarding management view. Lists active forwards per
workspace with local port, remote port, protocol, and state. Supports adding and removing forwards.

**`TUI-014`** — **Sync** — A progress view for long-running operations (workspace create, start,
stop, fork) showing real-time RPC progress notifications and log output.

**`TUI-015`** — **Create** — A quick-create modal/form for workspace creation with minimal fields
(name, repo, ref). Faster than Onramp for experienced users.

**`TUI-016`** — **Help** — Context-sensitive help showing available keybindings for the current
view. Triggered by `?` or `h`.

**`TUI-017`** — **PTY** — An embedded terminal view for interactive shell sessions. Renders
terminal output using a virtual terminal emulator. Supports multiple tabs for concurrent sessions.

**`TUI-018`** — **Fullscreen** — A layout mode where the current view occupies the entire terminal,
hiding the shell chrome (header, footer, sidebar).

---

## Component Primitives — `TUI-020`–`TUI-029`

### Custom Components

**`TUI-020`** — **Button** — A focusable action trigger with active/hover/disabled states.
Renders with rounded corners and theme-aware background. Supports keyboard activation (Enter/Space)
and mouse click.

**`TUI-021`** — **Toast** — A transient notification banner appearing at the bottom of the screen.
Types: info (blue), success (green), warning (yellow), error (red). Auto-dismisses after 5 seconds
or on any keypress.

**`TUI-022`** — **Badge** — A small pill-shaped label for status indicators. Used for workspace
state (`running`, `stopped`, `starting`), backend type (`libkrun`, `process`), and protocol
(`tcp`, `udp`, `http`).

**`TUI-023`** — **Modal** — A centered overlay dialog for confirmations and critical actions.
Dims the background. Supports OK/Cancel, Yes/No, and custom button configurations. Dismissible via
Escape or clicking outside.

**`TUI-024`** — **TabBar** — A horizontal tab switcher for views with multiple panes
(Detail view: Info, Ports, Sessions, Logs). Shows active tab with underline indicator.

### Bubbles Library Components

**`TUI-025`** — **List** (`bubbles/list`) — Used for workspace lists, forward lists, and menu
selection. Custom item delegate for theme-aware rendering. Supports filtering, pagination, and
keyboard navigation.

**`TUI-026`** — **Spinner** (`bubbles/spinner`) — Used in Sync view and during async operations.
Dot-style spinner with theme color.

**`TUI-027`** — **TextInput** (`bubbles/textinput`) — Used in forms (Onramp, Create, port add).
Supports placeholder text, character limits, validation feedback, and password masking.

**`TUI-028`** — **TextArea** (`bubbles/textarea`) — Used for multi-line input and log display.
Supports scrolling, line numbers (optional), and syntax highlighting (for code snippets).

**`TUI-029`** — **Viewport** (`bubbles/viewport`) — Used for scrollable content areas (logs,
help text, long detail views). Supports mouse wheel and Page Up/Down.

---

## Design System — `TUI-030`–`TUI-039`

### Tokens

**`TUI-030`** — The design system uses semantic color tokens mapped to Lipgloss styles:

| Token | Light Theme | Tokyo Night Theme | Usage |
|-------|-------------|-------------------|-------|
| `primary` | `#2563eb` | `#7aa2f7` | Active elements, focus |
| `success` | `#16a34a` | `#9ece6a` | Running state, success toast |
| `warning` | `#ca8a04` | `#e0af68` | Starting state, warning toast |
| `error` | `#dc2626` | `#f7768e` | Stopped/error state, error toast |
| `muted` | `#6b7280` | `#565f89` | Secondary text, borders |
| `surface` | `#f3f4f6` | `#1a1b26` | Panel backgrounds |
| `background` | `#ffffff` | `#24283b` | App background |
| `text` | `#111827` | `#a9b1d6` | Primary text |
| `textInverse` | `#ffffff` | `#1a1b26` | Text on primary background |

### Themes

**`TUI-031`** — **Tokyo Night** (default) — A dark theme based on the Tokyo Night color palette.
Optimized for long terminal sessions with low eye strain. Blue-tinted grays, muted secondary
colors, vibrant accent colors for status.

**`TUI-032`** — **Light** — A clean light theme with high contrast. Uses standard Tailwind-like
color values. Suitable for bright environments and presentations.

**`TUI-033`** — Theme selection is persisted to `~/.config/nexus/tui-theme` (or equivalent
XDG config path). Defaults to Tokyo Night if no preference is set.

### Styles

**`TUI-034`** — **Base styles** are defined as Lipgloss style objects, not raw ANSI sequences:

```go
var (
    BaseStyle = lipgloss.NewStyle().
        Foreground(ColorText).
        Background(ColorBackground)

    HeaderStyle = lipgloss.NewStyle().
        Bold(true).
        Foreground(ColorTextInverse).
        Background(ColorPrimary).
        Padding(0, 1)

    PanelStyle = lipgloss.NewStyle().
        Border(lipgloss.RoundedBorder()).
        BorderForeground(ColorMuted).
        Padding(1)

    FocusedPanelStyle = PanelStyle.
        BorderForeground(ColorPrimary)
)
```

**`TUI-035`** — **Responsive sizing** — Components MUST adapt to terminal dimensions. Minimum
supported terminal size: 80×24. Below this, the TUI shows a "terminal too small" message and
refuses to render.

**`TUI-036`** — **Spacing scale** — Uses a 4-pixel base unit: `xs=1`, `sm=2`, `md=4`, `lg=8`.
All padding and margins use these units.

**`TUI-037`** — **Typography** — No custom fonts (terminal limitations). Emphasis via bold,
dim, underline, and color only. Headers are bold; secondary text is dim.

---

## Navigation — `TUI-040`–`TUI-049`

**`TUI-040`** — **Arrow keys** — Universal navigation: Up/Down for list/item selection,
Left/Right for tab switching and horizontal movement.

**`TUI-041`** — **Vim bindings** — `h`/`j`/`k`/`l` mirror Left/Down/Up/Right in all views
where text input is not focused. `gg` goes to top, `G` goes to bottom in lists.

**`TUI-042`** — **Mouse** — Enabled by default (Bubble Tea `tea.WithMouseCellMotion()`).
Click to select list items, activate buttons, switch tabs, and scroll viewports. Right-click
context menus are NOT supported.

**`TUI-043`** — **Global shortcuts** — Available from any view:

| Key | Action |
|-----|--------|
| `q` / `Ctrl+c` | Quit TUI |
| `?` / `h` | Toggle help |
| `d` | Go to Dashboard |
| `n` | Open Create modal |
| `1`–`9` | Switch to tab N (in tabbed views) |
| `Esc` | Cancel current action / close modal |
| `Ctrl+l` | Refresh / redraw screen |

**`TUI-044`** — **View-specific shortcuts** — Each view defines its own keybindings displayed
in the footer help bar. Dashboard: `Enter` (detail), `s` (start), `S` (stop), `x` (remove).
Detail: `s` (shell), `f` (fork), `p` (spotlight).

**`TUI-045`** — **Modal shortcuts** — `Enter` confirms, `Esc` cancels, `Tab` cycles between
buttons.

**`TUI-046`** — **Search/Filter** — `/` activates filter mode in list views. Type to filter
items in real-time. `Esc` clears filter. `n`/`N` navigate between matches (if applicable).

---

## Layouts — `TUI-050`–`TUI-055`

**`TUI-050`** — **Shell Layout** — The default layout with three regions:
- **Header** (top, 1 line): App name, connection status, current view title
- **Body** (middle, flexible): The active view content
- **Footer** (bottom, 1–2 lines): Contextual keybindings and status messages

**`TUI-051`** — **Split Layout** — Used in Detail and PTY views. Vertical split with a
sidebar (left, 30% width) and main content area (right, 70% width). Sidebar shows navigation
or session list; main area shows detail content or terminal output.

**`TUI-052`** — **Centered Layout** — Used for modals, confirmations, and the Create view.
Content is centered both horizontally and vertically with a maximum width of 80 characters.
Background is dimmed.

**`TUI-053`** — **Fullscreen Layout** — Hides header and footer. Used for PTY sessions when
`F11` is pressed. The terminal output occupies the entire screen.

**`TUI-054`** — **Layout transitions** — Smooth transitions between layouts are NOT required.
Instant switch is acceptable. The footer MUST update immediately to show the new view's shortcuts.

**`TUI-055`** — **Responsive breakpoints** — At terminal width < 100, Split Layout collapses
to stacked (sidebar above content). At width < 60, sidebar is hidden and accessible via a toggle
key (`b`).

---

## Messages Type System — `TUI-060`–`TUI-068`

**`TUI-060`** — All TUI messages implement the empty `tea.Msg` interface. Messages are typed
using Go struct types, not string constants.

**`TUI-061`** — **Core message categories**:

```go
// Daemon connectivity
type DaemonConnectedMsg struct{}
type DaemonDisconnectedMsg struct{ Error error }
type DaemonStateMsg struct{ Workspaces []WorkspaceSummary }

// Workspace lifecycle
type WorkspaceCreatedMsg struct{ Workspace Workspace }
type WorkspaceUpdatedMsg struct{ ID string; State WorkspaceState }
type WorkspaceRemovedMsg struct{ ID string }

// Spotlight
type ForwardCreatedMsg struct{ Forward Forward }
type ForwardUpdatedMsg struct{ Forward Forward }
type ForwardRemovedMsg struct{ ID string }

// PTY
type PTYDataMsg struct{ SessionID string; Data []byte }
type PTYExitMsg struct{ SessionID string; ExitCode int }
type PTYResizeMsg struct{ SessionID string; Cols, Rows int }

// UI
type ToastMsg struct{ Type ToastType; Text string }
type ViewChangeMsg struct{ View ViewType }
type ModalOpenMsg struct{ Modal ModalType }
type ModalCloseMsg struct{}
```

**`TUI-062`** — **RPC response messages** — Each RPC call returns a typed result message or
error message:

```go
type RPCResultMsg struct {
    CallID string
    Result interface{}
}

type RPCErrorMsg struct {
    CallID string
    Error  error
}
```

**`TUI-063`** — **Async operation messages** — Long-running operations emit progress:

```go
type OperationStartedMsg struct{ CallID string; Description string }
type OperationProgressMsg struct{ CallID string; Percent int; Detail string }
type OperationCompletedMsg struct{ CallID string }
type OperationFailedMsg struct{ CallID string; Error error }
```

**`TUI-064`** — **Message routing** — The root model's `Update` function acts as a router:
dispatches messages to the active sub-model based on message type and current view. Sub-models
return `(tea.Model, tea.Cmd)` and the root model handles the result.

**`TUI-065`** — **Message broadcasting** — Some messages (DaemonStateMsg, ToastMsg) are broadcast
to all sub-models. Each sub-model decides whether to handle or ignore.

**`TUI-066`** — **Command chaining** — `tea.Cmd` functions are composed using `tea.Batch` for
parallel execution and `tea.Sequence` for sequential execution.

**`TUI-067`** — **Tickers and timers** — Use `tea.Every` for periodic refresh (daemon state
poll every 5 seconds). Use `tea.Tick` for one-shot delays (toast auto-dismiss).

**`TUI-068`** — **External input** — Keyboard and mouse events from Bubble Tea are passed
through unmodified to the active sub-model, except for global shortcuts which are intercepted
by the root model.

---

## PTY Session Persistence — `TUI-070`–`TUI-078`

**`TUI-070`** — PTY sessions are managed per-tab in the PTY view. Each tab represents one
active `pty.create` session with a unique session ID.

**`TUI-071`** — **Tab state** includes:
- `sessionID` — The daemon's PTY session identifier
- `workspaceID` — The associated workspace
- `title` — Display name (defaults to workspace name, user-editable)
- `terminal` — Virtual terminal emulator state (screen buffer, cursor position, scrollback)
- `connected` — Whether the session is actively receiving data
- `exitCode` — Set when session ends (nil while running)

**`TUI-072`** — **Session creation** — When a new PTY tab is opened:
1. Call `pty.create {"workspaceId", "shell", "args", "workDir"}`
2. On success, store sessionID and start rendering terminal output
3. On failure, show error toast and close tab

**`TUI-073`** — **Data flow** — The TUI subscribes to `pty.data` push notifications via the
mux connection. Incoming data is written to the virtual terminal emulator, which updates the
screen buffer. The View() function renders the screen buffer to strings.

**`TUI-074`** — **Input forwarding** — Keyboard input in PTY view is forwarded directly to the
daemon via `pty.write` RPC, bypassing normal TUI key handling (except global shortcuts and
`Ctrl+a` prefix for TUI commands).

**`TUI-075`** — **Session persistence across view switches** — PTY sessions survive switching
to other TUI views (Dashboard, Detail, etc.). The terminal state is maintained in memory and
resumed when returning to the PTY view.

**`TUI-076`** — **Session persistence across disconnections** — If the daemon connection drops,
PTY tabs show a disconnected indicator. On reconnection, the TUI attempts to reattach to existing
sessions by session ID. If the session no longer exists (daemon restarted), the tab shows an
error and offers to restart.

**`TUI-077`** — **Tab management** — Users can:
- Open new tab: `Ctrl+t` (prompts for workspace)
- Close tab: `Ctrl+w` or click × button (sends `pty.close`, cleans up state)
- Rename tab: `Ctrl+r` (inline edit)
- Switch tabs: `Alt+1`–`Alt+9` or click tab

**`TUI-078`** — **Scrollback buffer** — Each PTY tab maintains a scrollback buffer of 10,000
lines. `Shift+PageUp/PageDown` scrolls through history. `Shift+Esc` exits scrollback mode and
returns to live output.
