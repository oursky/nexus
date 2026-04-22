/**
 * Main demo scene — two equal-width terminals side by side:
 *   Left : Linux host  (install → daemon start, then idle)
 *   Right: Mac terminal (install → connect → create → start →
 *          spotlight → curl → exec → git commit)
 */
import React from "react";
import { AbsoluteFill, useCurrentFrame } from "remotion";
import { TerminalWindow, TerminalLine } from "../components/TerminalWindow";

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
  { type: "command", text: INSTALL_CMD,                         startFrame: 0,   charsPerFrame: 6 },
  { type: "output",  text: "  Downloading nexus for darwin/arm64...", startFrame: 18 },
  { type: "output",  text: "✓  nexus installed",                startFrame: 52,  color: "#4EC994" },
  { type: "blank",   text: "",                                  startFrame: 68 },

  { type: "command", text: "nexus daemon connect user@linuxbox", startFrame: 210, charsPerFrame: 3 },
  { type: "output",  text: "✓  Connected to linuxbox (:7777)",  startFrame: 265, color: "#4EC994" },
  { type: "blank",   text: "",                                  startFrame: 280 },

  { type: "command", text: "nexus workspace create --repo ~/my-project", startFrame: 295, charsPerFrame: 3 },
  { type: "output",  text: "  Syncing project to linuxbox...",  startFrame: 345 },
  { type: "output",  text: "✓  workspace created: my-project",  startFrame: 385, color: "#4EC994" },
  { type: "blank",   text: "",                                  startFrame: 405 },

  { type: "command", text: "nexus workspace start my-project",  startFrame: 420, charsPerFrame: 3 },
  { type: "output",  text: "  Booting Firecracker VM...",       startFrame: 460 },
  { type: "output",  text: "  Discovered: 3000 (web)  5432 (db)  8080 (api)", startFrame: 510, color: "#569CD6" },
  { type: "output",  text: "✓  workspace ready",               startFrame: 550, color: "#4EC994" },
  { type: "blank",   text: "",                                  startFrame: 568 },

  { type: "command", text: "nexus spotlight start my-project",  startFrame: 583, charsPerFrame: 3 },
  { type: "output",  text: "  3000 \u2192 localhost:3000",      startFrame: 628, color: "#DCDCAA" },
  { type: "output",  text: "  5432 \u2192 localhost:5432",      startFrame: 642, color: "#DCDCAA" },
  { type: "output",  text: "  8080 \u2192 localhost:8080",      startFrame: 656, color: "#DCDCAA" },
  { type: "output",  text: "✓  forwarded 3/3 ports",           startFrame: 675, color: "#4EC994" },
  { type: "blank",   text: "",                                  startFrame: 693 },

  { type: "command", text: "curl localhost:3000/health",        startFrame: 708, charsPerFrame: 3 },
  { type: "output",  text: '  {"status":"ok","uptime":"1m 42s","workspace":"my-project"}', startFrame: 748, color: "#a6e3a1" },
  { type: "blank",   text: "",                                  startFrame: 770 },

  { type: "command", text: "nexus workspace exec my-project",   startFrame: 785, charsPerFrame: 3 },
  { type: "output",  text: "  Connected to Firecracker VM",     startFrame: 830, color: "#4EC994" },
  { type: "blank",   text: "",                                  startFrame: 848 },

  { type: "command", text: "git add . && git commit -m 'feat: add API endpoint'", startFrame: 863, charsPerFrame: 3 },
  { type: "output",  text: "  [main 4a2c9e1] feat: add API endpoint", startFrame: 923, color: "#a6adc8" },
  { type: "output",  text: "  3 files changed, 42 insertions(+)", startFrame: 943 },
];

// ── Scene ────────────────────────────────────────────────────────────────────
export const DeployScene: React.FC = () => {
  const f = useCurrentFrame();

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
      <div style={{ flex: 1, display: "flex", flexDirection: "column" }}>
        <div style={{ color: "#45475a", fontFamily: "sans-serif", fontSize: 13, letterSpacing: 1, marginBottom: 10 }}>
          LINUX HOST
        </div>
        <div style={{ flex: 1 }}>
          <TerminalWindow title="linuxbox" lines={LINUX_LINES} frame={f} fontSize={18} />
        </div>
      </div>

      <div style={{ flex: 1, display: "flex", flexDirection: "column" }}>
        <div style={{ color: "#45475a", fontFamily: "sans-serif", fontSize: 13, letterSpacing: 1, marginBottom: 10 }}>
          YOUR MAC
        </div>
        <div style={{ flex: 1 }}>
          <TerminalWindow title="mac" lines={MAC_LINES} frame={f} fontSize={18} />
        </div>
      </div>
    </AbsoluteFill>
  );
};
