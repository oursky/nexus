import React from "react";
import { AbsoluteFill, interpolate, useCurrentFrame } from "remotion";
import { TerminalWindow, TerminalLine } from "../components/TerminalWindow";
import { BrowserWindow } from "../components/BrowserWindow";

const TERMINAL_LINES: TerminalLine[] = [
  { type: "command", text: "nexus exec webapp-dev", startFrame: 0 },
  { type: "output", text: "[webapp-dev] $ npm run dev", startFrame: 40 },
  { type: "output", text: "  ▲ Next.js 14.2.0", startFrame: 70 },
  { type: "output", text: "  - Local:        http://localhost:3000", startFrame: 90 },
  { type: "output", text: "  ✓ Ready in 2.1s", startFrame: 120, color: "#a6e3a1" },
  { type: "blank", text: "", startFrame: 145 },
  { type: "output", text: "# editing src/app/page.tsx", startFrame: 420, color: "#6c7086" },
  { type: "output", text: "event - compiled successfully", startFrame: 500, color: "#89b4fa" },
];

const MockPage: React.FC<{ version: number; reloadFlash: number }> = ({ version, reloadFlash }) => {
  const flashOpacity = interpolate(reloadFlash, [0, 0.15, 0.3, 1], [1, 0.3, 0.8, 1]);
  return (
    <div
      style={{
        background: "#0a0a0a",
        width: "100%",
        height: "100%",
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        gap: 16,
        opacity: flashOpacity,
      }}
    >
      {version === 0 ? (
        <>
          <div style={{ fontSize: 32, fontWeight: 700, color: "#ffffff", fontFamily: "sans-serif" }}>
            Welcome to Next.js
          </div>
          <div style={{ fontSize: 18, color: "#888", fontFamily: "monospace" }}>
            Edit <code style={{ color: "#a6e3a1" }}>src/app/page.tsx</code> to get started
          </div>
        </>
      ) : (
        <>
          <div style={{ fontSize: 40, fontWeight: 700, color: "#89b4fa", fontFamily: "sans-serif" }}>
            Hello from Nexus 🚀
          </div>
          <div style={{ fontSize: 18, color: "#a6adc8", fontFamily: "monospace" }}>
            Running in Firecracker VM on linuxbox
          </div>
        </>
      )}
    </div>
  );
};

export const LivePreviewScene: React.FC = () => {
  const frame = useCurrentFrame();
  const f = frame;

  // Browser slides in from right at f=150
  const browserX = interpolate(f, [150, 280], [1920, 0], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });
  const browserOpacity = interpolate(f, [150, 200], [0, 1], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });

  // Hot reload flash at f=490
  const reloadProgress = interpolate(f, [490, 530], [0, 1], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });
  const pageVersion = f >= 520 ? 1 : 0;

  const terminalWidth = interpolate(f, [150, 270], [1760, 840], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });

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
        STEP 3 — LIVE PREVIEW
      </div>

      <div style={{ position: "relative", flex: 1 }}>
        {/* Terminal — shrinks right when browser appears */}
        <div
          style={{
            position: "absolute",
            left: 0,
            top: 0,
            bottom: 0,
            width: terminalWidth,
            transition: "none",
          }}
        >
          <TerminalWindow title="webapp-dev" lines={TERMINAL_LINES} frame={f} />
        </div>

        {/* Browser — slides in from right */}
        <div
          style={{
            position: "absolute",
            right: 0,
            top: 0,
            bottom: 0,
            width: 880,
            opacity: browserOpacity,
            transform: `translateX(${browserX - 0}px)`,
          }}
        >
          <BrowserWindow url="localhost:3000">
            <MockPage version={pageVersion} reloadFlash={reloadProgress} />
          </BrowserWindow>
        </div>
      </div>
    </AbsoluteFill>
  );
};
