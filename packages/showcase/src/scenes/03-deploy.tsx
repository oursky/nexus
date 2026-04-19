import React from "react";
import { AbsoluteFill, useCurrentFrame } from "remotion";
import { TerminalWindow, TerminalLine } from "../components/TerminalWindow";


const LINES: TerminalLine[] = [
  { type: "command", text: "curl -fsSL https://get.nexus.sh | sh", startFrame: 0 },
  { type: "output", text: "✓ nexus 0.9.0 installed", startFrame: 60, color: "#a6e3a1" },
  { type: "blank", text: "", startFrame: 80 },
  { type: "command", text: "nexus daemon start --network --bind 0.0.0.0 --tls auto", startFrame: 90 },
  { type: "output", text: "[nexusd] transport: network listener on 0.0.0.0:7777", startFrame: 190 },
  { type: "output", text: "[nexusd] workspace daemon ready", startFrame: 230, color: "#a6e3a1" },
  { type: "blank", text: "", startFrame: 260 },
  { type: "command", text: "nexus daemon token", startFrame: 270 },
  { type: "output", text: "87f1a6fcd4be254de29134caf54a6045abe5ef40eacece99aa934c17e2cc2a20", startFrame: 330, color: "#f9e2af" },
  { type: "blank", text: "", startFrame: 380 },
  { type: "output", text: "# Copy token → paste into macOS app", startFrame: 390, color: "#6c7086" },
];

export const DeployScene: React.FC = () => {
  const frame = useCurrentFrame();
  const f = frame;

  return (
    <AbsoluteFill
      style={{
        background: "#11111b",
        display: "flex",
        flexDirection: "column",
        padding: 90,
        gap: 24,
      }}
    >
      <div
        style={{
          color: "#a6adc8",
          fontSize: 20,
          fontFamily: "sans-serif",
          letterSpacing: 2,
          textTransform: "uppercase",
          opacity: 0.6,
        }}
      >
        STEP 1 — DEPLOY THE DAEMON
      </div>
      <TerminalWindow title="deploy nexusd" lines={LINES} frame={f} />
    </AbsoluteFill>
  );
};
