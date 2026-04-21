/**
 * Main demo scene — three panels:
 *   Left  (~26%): Linux host terminal (install → daemon start, then idle)
 *   Center(~38%): Mac terminal — install runs in parallel, then waits for
 *                 daemon ready before connecting. Full flow: connect →
 *                 create → start → spotlight → curl → exec → git commit
 *   Right (~36%): Browser (fades in once spotlight is active)
 *
 * Key constraint: `nexus daemon connect` only fires after Linux daemon
 * prints "ready on :7777" (frame 200).
 */
import React from "react";
import { AbsoluteFill, interpolate, useCurrentFrame } from "remotion";
import { TerminalWindow, TerminalLine } from "../components/TerminalWindow";
import { BrowserWindow } from "../components/BrowserWindow";

const INSTALL_CMD = "curl -fsSL https://raw.githubusercontent.com/IniZio/nexus/main/install.sh | sh";

// ── Linux host ───────────────────────────────────────────────────────────────
const LINUX_LINES: TerminalLine[] = [
  { type: "command", text: INSTALL_CMD,                         startFrame: 0,   charsPerFrame: 6 },
  { type: "output",  text: "  Downloading nexus for linux/amd64...", startFrame: 18 },
  { type: "output",  text: "✓  nexus installed",                startFrame: 52,  color: "#4EC994" },
  { type: "blank",   text: "",                                  startFrame: 68 },
  { type: "command", text: "nexus daemon start",                startFrame: 80 },
  { type: "output",  text: "  Downloading Firecracker...",      startFrame: 115 },
  { type: "output",  text: "  Configuring network bridge...",   startFrame: 155 },
  { type: "output",  text: "  daemon ready on :7777",           startFrame: 200, color: "#4EC994" },
];

// ── Mac CLI ──────────────────────────────────────────────────────────────────
// Install runs in parallel with Linux. Everything else waits until
// Linux daemon is ready (frame 200).
const MAC_LINES: TerminalLine[] = [
  // ── install (parallel with linux) ──────────────────────────────
  { type: "command", text: INSTALL_CMD,                         startFrame: 0,   charsPerFrame: 6 },
  { type: "output",  text: "  Downloading nexus for darwin/arm64...", startFrame: 18 },
  { type: "output",  text: "✓  nexus installed",                startFrame: 52,  color: "#4EC994" },
  { type: "blank",   text: "",                                  startFrame: 68 },

  // ── connect — only after daemon ready (f200) ────────────────────
  { type: "command", text: "nexus daemon connect user@linuxbox", startFrame: 210, charsPerFrame: 3 },
  { type: "output",  text: "✓  Connected to linuxbox (:7777)",  startFrame: 265, color: "#4EC994" },
  { type: "blank",   text: "",                                  startFrame: 280 },

  // ── workspace create ────────────────────────────────────────────
  { type: "command", text: "nexus workspace create --repo ~/my-project", startFrame: 295, charsPerFrame: 3 },
  { type: "output",  text: "  Syncing project to linuxbox...", startFrame: 345 },
  { type: "output",  text: "✓  workspace created: my-project", startFrame: 385, color: "#4EC994" },
  { type: "blank",   text: "",                                  startFrame: 405 },

  // ── workspace start ─────────────────────────────────────────────
  { type: "command", text: "nexus workspace start my-project",  startFrame: 420, charsPerFrame: 3 },
  { type: "output",  text: "  Booting Firecracker VM...",       startFrame: 460 },
  { type: "output",  text: "  Discovered: 3000 (web)  5432 (db)  8080 (api)", startFrame: 510, color: "#569CD6" },
  { type: "output",  text: "✓  workspace ready",               startFrame: 550, color: "#4EC994" },
  { type: "blank",   text: "",                                  startFrame: 568 },

  // ── spotlight ───────────────────────────────────────────────────
  { type: "command", text: "nexus spotlight start my-project",  startFrame: 583, charsPerFrame: 3 },
  { type: "output",  text: "  3000 \u2192 localhost:3000",      startFrame: 628, color: "#DCDCAA" },
  { type: "output",  text: "  5432 \u2192 localhost:5432",      startFrame: 642, color: "#DCDCAA" },
  { type: "output",  text: "  8080 \u2192 localhost:8080",      startFrame: 656, color: "#DCDCAA" },
  { type: "output",  text: "✓  forwarded 3/3 ports",           startFrame: 675, color: "#4EC994" },
  { type: "blank",   text: "",                                  startFrame: 693 },

  // ── curl from Mac to forwarded port ────────────────────────────
  { type: "command", text: "curl localhost:3000/health",        startFrame: 708, charsPerFrame: 3 },
  { type: "output",  text: '  {"status":"ok","uptime":"1m 42s","workspace":"my-project"}', startFrame: 748, color: "#a6e3a1" },
  { type: "blank",   text: "",                                  startFrame: 770 },

  // ── exec into VM ────────────────────────────────────────────────
  { type: "command", text: "nexus workspace exec my-project",   startFrame: 785, charsPerFrame: 3 },
  { type: "output",  text: "  Connected to Firecracker VM",     startFrame: 830, color: "#4EC994" },
  { type: "blank",   text: "",                                  startFrame: 848 },

  // ── git commit inside VM ────────────────────────────────────────
  { type: "command", text: "git add . && git commit -m 'feat: add API endpoint'", startFrame: 863, charsPerFrame: 3 },
  { type: "output",  text: "  [main 4a2c9e1] feat: add API endpoint", startFrame: 923, color: "#a6adc8" },
  { type: "output",  text: "  3 files changed, 42 insertions(+)", startFrame: 943 },
];

// ── Browser mock page ────────────────────────────────────────────────────────
const MockWebPage: React.FC<{ frame: number }> = ({ frame }) => {
  const bodyIn = interpolate(frame, [0, 30], [0, 1], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });
  const rowIn = (delay: number) =>
    interpolate(frame, [delay, delay + 20], [0, 1], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });

  return (
    <div
      style={{
        background: "#0f1117",
        width: "100%",
        height: "100%",
        fontFamily: "'JetBrains Mono', monospace",
        display: "flex",
        flexDirection: "column",
        opacity: bodyIn,
      }}
    >
      {/* Nav bar */}
      <div
        style={{
          borderBottom: "1px solid rgba(255,255,255,0.07)",
          padding: "14px 24px",
          display: "flex",
          alignItems: "center",
          gap: 12,
        }}
      >
        <div style={{ width: 8, height: 8, borderRadius: "50%", background: "#4EC994" }} />
        <span style={{ color: "rgba(255,255,255,0.5)", fontSize: 12 }}>my-project API</span>
        <span style={{ marginLeft: "auto", color: "rgba(255,255,255,0.25)", fontSize: 11 }}>v1.0.0</span>
      </div>

      {/* JSON body */}
      <div style={{ flex: 1, padding: "20px 24px" }}>
        <div style={{ color: "rgba(255,255,255,0.3)", fontSize: 11, marginBottom: 12, letterSpacing: 1 }}>
          GET /health → 200 OK
        </div>
        {(
          [
            ["{",                              "rgba(255,255,255,0.45)", 0],
            ['  "status": "ok",',              "#4EC994",               12],
            ['  "uptime": "1m 42s",',          "#DCDCAA",               24],
            ['  "workspace": "my-project",',   "#569CD6",               36],
            ['  "ports": [3000, 5432, 8080]',  "#cba6f7",               48],
            ["}",                              "rgba(255,255,255,0.45)", 58],
          ] as [string, string, number][]
        ).map(([text, color, delay]) => (
          <div key={text} style={{ color, fontSize: 13, lineHeight: 1.9, opacity: rowIn(delay) }}>
            {text}
          </div>
        ))}
      </div>
    </div>
  );
};

// ── Scene ────────────────────────────────────────────────────────────────────
export const DeployScene: React.FC = () => {
  const f = useCurrentFrame();

  // Browser fades in once "forwarded 3/3 ports" appears
  const BROWSER_START = 680;
  const browserOpacity = interpolate(f, [BROWSER_START, BROWSER_START + 40], [0, 1], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
  });
  const browserX = interpolate(f, [BROWSER_START, BROWSER_START + 40], [30, 0], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
  });

  return (
    <AbsoluteFill
      style={{
        background: "#11111b",
        display: "flex",
        flexDirection: "row",
        alignItems: "stretch",
        padding: 60,
        gap: 28,
      }}
    >
      {/* Linux host — narrow, static after daemon start */}
      <div style={{ flex: 26 }}>
        <div style={{ color: "#45475a", fontFamily: "sans-serif", fontSize: 13, letterSpacing: 1, marginBottom: 10 }}>
          LINUX HOST
        </div>
        <TerminalWindow title="linuxbox" lines={LINUX_LINES} frame={f} fontSize={16} />
      </div>

      {/* Mac CLI — center */}
      <div style={{ flex: 38 }}>
        <div style={{ color: "#45475a", fontFamily: "sans-serif", fontSize: 13, letterSpacing: 1, marginBottom: 10 }}>
          YOUR MAC
        </div>
        <TerminalWindow title="mac" lines={MAC_LINES} frame={f} fontSize={16} />
      </div>

      {/* Browser — slides in after spotlight */}
      <div
        style={{
          flex: 36,
          opacity: browserOpacity,
          transform: `translateX(${browserX}px)`,
          display: "flex",
          flexDirection: "column",
        }}
      >
        <div style={{ color: "#45475a", fontFamily: "sans-serif", fontSize: 13, letterSpacing: 1, marginBottom: 10 }}>
          BROWSER — localhost:3000
        </div>
        <div style={{ flex: 1 }}>
          <BrowserWindow url="localhost:3000/health" width="100%" height="100%">
            <MockWebPage frame={Math.max(0, f - BROWSER_START)} />
          </BrowserWindow>
        </div>
      </div>
    </AbsoluteFill>
  );
};
