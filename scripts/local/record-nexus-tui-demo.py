#!/usr/bin/env python3
# Drive the Nexus TUI for an asciinema recording (bubbletea / lipgloss).
#
# Run from the repo root inside asciinema, with truecolor TERM and geometry.
# Uses uv so pexpect does not need system/site install:
#
#   export COLORTERM=truecolor TERM=xterm-256color COLUMNS=220 LINES=50
#   cd /path/to/nexus && asciinema rec --cols 220 --rows 50 /tmp/nexus-demo-v2.cast -c \
#     'uv run --with pexpect python3 scripts/local/record-nexus-tui-demo.py'
#
# Optional: NEXUS_RECORD_DRIVER=libkrun with NEXUS_VM_KERNEL + NEXUS_VM_ROOTFS.

from __future__ import annotations

import atexit
import http.client
import os
import random
import signal
import socket
import subprocess
import sys
import tempfile
import time
from pathlib import Path

import pexpect

REPO_ROOT = Path(__file__).resolve().parents[2]
NEXUS_PKG = REPO_ROOT / "packages" / "nexus"


def free_tcp_port() -> int:
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


def wait_healthz(host: str, port: int, timeout_sec: float = 90) -> None:
    deadline = time.time() + timeout_sec
    while time.time() < deadline:
        try:
            c = http.client.HTTPConnection(host, port, timeout=2)
            c.request("GET", "/healthz")
            r = c.getresponse()
            if r.status == 200:
                return
        except OSError:
            pass
        time.sleep(0.1)
    raise TimeoutError(f"daemon /healthz not ready on {host}:{port}")


def build_binaries(bindir: Path) -> Path:
    bindir.mkdir(parents=True, exist_ok=True)
    nexus = bindir / "nexus"
    ptyhost = bindir / "pty-host"
    gen = subprocess.run(
        ["go", "generate", "./cmd/nexus/"],
        cwd=NEXUS_PKG,
        capture_output=True,
        text=True,
    )
    if gen.returncode != 0:
        print(gen.stderr, file=sys.stderr)
    b1 = subprocess.run(
        ["go", "build", "-tags", "dev", "-o", str(nexus), "./cmd/nexus"],
        cwd=NEXUS_PKG,
    )
    if b1.returncode != 0:
        sys.exit(1)
    b2 = subprocess.run(
        ["go", "build", "-tags", "dev", "-o", str(ptyhost), "./cmd/pty-host"],
        cwd=NEXUS_PKG,
    )
    if b2.returncode != 0:
        sys.exit(1)
    return nexus


def make_git_repo(path: Path) -> None:
    subprocess.run(["git", "init"], cwd=path, check=True, capture_output=True)
    subprocess.run(
        ["git", "config", "user.email", "demo@local"],
        cwd=path,
        check=True,
        capture_output=True,
    )
    subprocess.run(
        ["git", "config", "user.name", "Demo"],
        cwd=path,
        check=True,
        capture_output=True,
    )
    (path / "README.md").write_text("# myproject demo\n", encoding="utf-8")
    subprocess.run(["git", "add", "-A"], cwd=path, check=True, capture_output=True)
    subprocess.run(["git", "commit", "-m", "init"], cwd=path, check=True, capture_output=True)
    subprocess.run(["git", "branch", "-M", "main"], cwd=path, check=True, capture_output=True)


def daemon_cmd(
    nexus: Path,
    db: Path,
    sock: Path,
    work_root: Path,
    port: int,
    token: str,
) -> list[str]:
    driver = os.environ.get("NEXUS_RECORD_DRIVER", "sandbox").strip() or "sandbox"
    args: list[str] = [
        str(nexus),
        "daemon",
        "start",
        "--db",
        str(db),
        "--socket",
        str(sock),
        "--workdir-root",
        str(work_root),
    ]
    if driver == "libkrun":
        k = os.environ.get("NEXUS_VM_KERNEL", "").strip()
        r = os.environ.get("NEXUS_VM_ROOTFS", "").strip()
        if not k or not r:
            print("libkrun requires NEXUS_VM_KERNEL and NEXUS_VM_ROOTFS", file=sys.stderr)
            sys.exit(1)
        args += ["--kernel", k, "--rootfs", r]
    args += [
        "--driver",
        driver,
        "--network=true",
        "--bind",
        "127.0.0.1",
        "--port",
        str(port),
        "--token",
        token,
        "--foreground",
    ]
    return args


def main() -> None:
    tmp = Path(tempfile.mkdtemp(prefix="nexus-tui-demo-"))
    bindir = tmp / "bin"
    nexus = build_binaries(bindir)

    repo = tmp / "myproject-repo"
    repo.mkdir()
    make_git_repo(repo)
    repo_abs = str(repo.resolve())

    port = free_tcp_port()
    token = "".join(f"{random.randrange(256):02x}" for _ in range(24))
    db = tmp / "nexus.db"
    sock = tmp / "nexusd.sock"
    work_root = tmp / "work"

    env = os.environ.copy()
    env["PATH"] = str(bindir) + os.pathsep + env.get("PATH", "")
    plog = open(tmp / "daemon.log", "w", encoding="utf-8")  # noqa: SIM115
    dproc = subprocess.Popen(daemon_cmd(nexus, db, sock, work_root, port, token), env=env, stdout=plog, stderr=subprocess.STDOUT)

    def cleanup_daemon() -> None:
        if dproc.poll() is None:
            dproc.send_signal(signal.SIGTERM)
            try:
                dproc.wait(timeout=20)
            except subprocess.TimeoutExpired:
                dproc.kill()
        plog.close()

    atexit.register(cleanup_daemon)

    wait_healthz("127.0.0.1", port)
    ws_url = f"ws://127.0.0.1:{port}/"

    state_home = tmp / "xdg-state"
    (state_home / "nexus").mkdir(parents=True, exist_ok=True)

    cli_env = env.copy()
    cli_env["NEXUS_E2E_DAEMON_WEBSOCKET"] = ws_url
    cli_env["NEXUS_DAEMON_TOKEN"] = token
    cli_env["XDG_STATE_HOME"] = str(state_home)

    tui_env = cli_env.copy()
    tui_env.setdefault("COLORTERM", "truecolor")
    tui_env.setdefault("TERM", "xterm-256color")

    rows, cols = 50, 220
    if "LINES" in os.environ:
        rows = int(os.environ["LINES"])
    if "COLUMNS" in os.environ:
        cols = int(os.environ["COLUMNS"])

    child = pexpect.spawn(
        str(nexus),
        ["tui"],
        env=tui_env,
        encoding="utf-8",
        timeout=240,
        maxread=65536,
    )
    child.delaybeforesend = 0.1
    child.setwinsize(rows, cols)
    child.logfile = sys.stdout

    child.expect([r"[Cc]onnected", r"[Nn]exus", r"[Ww]orkspace"], timeout=60)
    time.sleep(0.7)

    # Create workspace (bubbletea expects \\r for Enter, not sendline's \\n).
    child.send("n")
    child.expect(r"tab next field", timeout=30)
    child.send("myproject\r")
    time.sleep(0.35)
    # Enter advanced to repo; do not Tab here — Tab cycles fields and would skip repo.
    child.send(repo_abs + "\r")
    time.sleep(0.35)
    child.send("main\r")
    child.expect(r"workspace created", timeout=90)
    time.sleep(0.7)

    # Detail
    child.send("\r")
    time.sleep(0.9)

    # Start — center pane attaches a PTY; three-pane layout already shows SPOTLIGHT sidebar.
    child.send("s")
    time.sleep(2.0)

    # Full-screen shell via workspace shell (avoid opening the spotlight overlay here —
    # while that panel is open, keys like "t" are not routed to workspace actions).
    child.send("t")
    time.sleep(2.8)
    child.send("pwd\r")
    time.sleep(0.35)
    child.send("uname -a\r")
    time.sleep(0.35)
    child.send("echo 'Nexus: host-side PTY (sandbox) — libkrun guests get Docker inside the VM.'\r")
    time.sleep(0.35)
    child.send("exit\r")
    time.sleep(2.5)

    child.expect([r"[Cc]onnected", r"[Nn]exus", r"myproject", r"[Qq]uit"], timeout=90)
    time.sleep(0.5)
    # Clear any overlay / focus quirks before stop.
    child.send("\x1b")
    time.sleep(0.25)

    child.send("x")
    time.sleep(1.0)
    child.send("q")
    child.expect(pexpect.EOF, timeout=45)
    child.close()

    print("\n\033[1;33m$ nexus workspace list\033[0m\n", flush=True)
    subprocess.run([str(nexus), "workspace", "list"], env=cli_env, check=False)
    print(flush=True)


if __name__ == "__main__":
    main()
