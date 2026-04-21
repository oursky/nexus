import React from "react";
import { AbsoluteFill, interpolate, useCurrentFrame } from "remotion";
import { TerminalWindow, TerminalLine } from "../components/TerminalWindow";
import { BrowserWindow } from "../components/BrowserWindow";

// Mac terminal — curl through spotlight tunnel
const MAC_LINES: TerminalLine[] = [
  { type: "command", text: "curl localhost:8080/health", startFrame: 0 },
  { type: "output", text: '  {"status":"ok","uptime":"3m12s"}', startFrame: 50, color: "#a6e3a1" },
  { type: "blank", text: "", startFrame: 80 },
  { type: "command", text: "curl localhost:3000", startFrame: 110 },
  { type: "output", text: "  <!DOCTYPE html>", startFrame: 160, color: "#89b4fa" },
  { type: "output", text: "  <html>... my-project running</html>", startFrame: 180, color: "#89b4fa" },
];

// VM shell — git workflow inside the workspace
const VM_LINES: TerminalLine[] = [
  { type: "command", text: "nexus workspace shell my-project", startFrame: 60 },
  { type: "output", text: "  Connected to Firecracker VM", startFrame: 110, color: "#a6e3a1" },
  { type: "blank", text: "", startFrame: 130 },
  { type: "command", text: "echo 'fix: typo' >> api/handler.go", startFrame: 160 },
  { type: "command", text: "git add . && git commit -m 'fix: typo'", startFrame: 230 },
  { type: "output", text: "  [main a3f29b1] fix: typo", startFrame: 290, color: "#a6e3a1" },
  { type: "output", text: "  1 file changed, 1 insertion(+)", startFrame: 310 },
];

const MockPage: React.FC<{ frame: number }> = ({ frame }) => {
  const labelOpacity = interpolate(frame, [160, 200], [0, 1], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
  });

  return (
    <div
      style={{
        background: "#0f0f17",
        width: "100%",
        height: "100%",
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        gap: 12,
        fontFamily: "sans-serif",
      }}
    >
      <div style={{ fontSize: 13, color: "#6c7086", letterSpacing: 2, textTransform: "uppercase" }}>
        running on firecracker via spotlight
      </div>
      <div style={{ fontSize: 36, fontWeight: 700, color: "#cdd6f4" }}>my-project</div>
      <div
        style={{
          marginTop: 16,
          background: "rgba(166,227,161,0.08)",
          border: "1px solid rgba(166,227,161,0.3)",
          borderRadius: 8,
          padding: "8px 20px",
          color: "#a6e3a1",
          fontSize: 16,
          opacity: labelOpacity,
        }}
      >
        localhost:3000
      </div>
    </div>
  );
};

export const LivePreviewScene: React.FC = () => {
  const frame = useCurrentFrame();
  const f = frame;

  const browserOpacity = interpolate(f, [40, 90], [0, 1], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
  });
  const browserX = interpolate(f, [40, 90], [60, 0], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
  });

  const vmOpacity = interpolate(f, [20, 60], [0, 1], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
  });

  return (
    <AbsoluteFill
      style={{
        background: "#11111b",
        display: "flex",
        flexDirection: "column",
        padding: 72,
        gap: 20,
      }}
    >
      {/* Mac curl + VM shell + browser */}
      <div style={{ display: "flex", flex: 1, gap: 36 }}>
        {/* Left column: Mac curl + VM shell */}
        <div style={{ flex: 5, display: "flex", flexDirection: "column", gap: 24 }}>
          <div style={{ flex: 1 }}>
            <div style={{ color: "#6c7086", fontFamily: "sans-serif", fontSize: 16, marginBottom: 10, letterSpacing: 1 }}>
              Mac — curl through spotlight
            </div>
            <TerminalWindow title="mac" lines={MAC_LINES} frame={f} />
          </div>
          <div style={{ flex: 1, opacity: vmOpacity }}>
            <div style={{ color: "#6c7086", fontFamily: "sans-serif", fontSize: 16, marginBottom: 10, letterSpacing: 1 }}>
              VM — git inside workspace
            </div>
            <TerminalWindow title="my-project (firecracker)" lines={VM_LINES} frame={f} />
          </div>
        </div>

        {/* Right: browser window */}
        <div
          style={{
            flex: 4,
            opacity: browserOpacity,
            transform: `translateX(${browserX}px)`,
          }}
        >
          <BrowserWindow url="localhost:3000">
            <MockPage frame={f} />
          </BrowserWindow>
        </div>
      </div>
    </AbsoluteFill>
  );
};
