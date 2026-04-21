Output docs/assets/demo.gif

Require nexus
Require task
Require python3

Set Shell "bash"
Set FontSize 13
Set Width 1440
Set Height 560
Set Theme "Catppuccin Mocha"
Set Padding 0
Set WindowBar Colorful
Set BorderRadius 8
Set TypingSpeed 52ms
Set PlaybackSpeed 1

Sleep 400ms

# ── Launch tmux: left = workspace shell, right = Mac CLI ─────────────────
Type "tmux kill-session -t demo 2>/dev/null; tmux new-session -s demo '/bin/bash --norc' \; set -g status off \; split-window -h -p 50 '/bin/bash --norc' \; select-pane -t 0"
Enter
Sleep 800ms

# Left pane: set PATH + prompt
Type "export PATH=$HOME/.local/bin:$PATH && PS1='> '"
Enter
Sleep 200ms

# ── RIGHT PANE: Mac CLI ───────────────────────────────────────────────────
Ctrl+b
Right
Sleep 200ms

Type "export PATH=$HOME/.local/bin:$PATH && PS1=$'\\[\\e[38;5;147m\\]mac\\[\\e[0m\\]:\\[\\e[38;5;245m\\]~\\[\\e[0m\\]$ '"
Enter
Sleep 200ms

# One-liner: build for linux/amd64, deploy, restart daemon
Type "task dev:remote"
Enter
Sleep 12000ms

# Connect CLI to the remote daemon
Type "nexus daemon connect newman@linuxbox"
Enter
Sleep 2200ms

# Start workspace — Firecracker VM boots
Type "nexus workspace start ws-1776759737416718098"
Enter
Sleep 8000ms

# ── LEFT PANE: shell inside the Firecracker VM ────────────────────────────
Ctrl+b
Left
Sleep 200ms

# Open an interactive shell in the workspace (runs inside the Firecracker VM)
Type "nexus workspace shell ws-1776759737416718098"
Enter
Sleep 2000ms

# Start a lightweight HTTP server on port 8080 in the background
Type "python3 -m http.server 8080 &>/dev/null &"
Enter
Sleep 600ms

# Verify it's up from inside the VM
Type "curl -s localhost:8080 | head -3"
Enter
Sleep 900ms

# Init git, add all project files, first commit
Type "git init -q && git config user.email dev@example.com && git config user.name Dev"
Enter
Sleep 700ms

Type "git add . && git commit -q -m 'feat: initial project setup'"
Enter
Sleep 1500ms

# Make a code change
Type "echo '# v1.0.0' >> README.md"
Enter
Sleep 500ms

# Commit the change
Type "git add README.md && git commit -q -m 'docs: release v1.0.0'"
Enter
Sleep 1200ms

# Show git log
Type "git log --oneline"
Enter
Sleep 800ms

# ── RIGHT PANE: forward ports to localhost via spotlight ──────────────────
Ctrl+b
Right
Sleep 200ms

# Forward workspace ports to Mac localhost
Type "nexus spotlight start ws-1776759737416718098"
Enter
Sleep 1400ms

# Hit the API through the spotlight tunnel (same server visible from inside VM)
Type "curl -s localhost:8080 | head -3"
Enter
Sleep 1000ms

# Show all workspace ports
Type "nexus workspace list"
Enter
Sleep 800ms

Sleep 4s
