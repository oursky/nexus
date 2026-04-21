import React from "react";
import { AbsoluteFill, interpolate, useCurrentFrame } from "remotion";
import { TerminalWindow, TerminalLine } from "../components/TerminalWindow";

const LINES: TerminalLine[] = [
  { type: "command", text: "nexus workspace create --repo ~/my-project", startFrame: 0 },
  { type: "output", text: "  Syncing project to linuxbox...", startFrame: 50 },
  { type: "output", text: "✓  workspace created: my-project", startFrame: 110, color: "#4EC994" },
  { type: "blank", text: "", startFrame: 130 },
  { type: "command", text: "nexus workspace start my-project", startFrame: 150 },
  { type: "output", text: "  Booting Firecracker VM...", startFrame: 200 },
  { type: "output", text: "  Discovered ports: 3000 (web)  5432 (db)  8080 (api)", startFrame: 280, color: "#569CD6" },
  { type: "output", text: "✓  workspace ready", startFrame: 340, color: "#4EC994" },
  { type: "blank", text: "", startFrame: 360 },
  { type: "command", text: "nexus spotlight start my-project", startFrame: 390 },
  { type: "output", text: "  3000 → localhost:3000  (web)", startFrame: 450, color: "#DCDCAA" },
  { type: "output", text: "  5432 → localhost:5432  (db)", startFrame: 470, color: "#DCDCAA" },
  { type: "output", text: "  8080 → localhost:8080  (api)", startFrame: 490, color: "#DCDCAA" },
  { type: "output", text: "✓  forwarded 3/3 ports", startFrame: 520, color: "#4EC994" },
];

const PORTS = [
  { label: "web",  port: "localhost:3000", color: "#569CD6",  startF: 460 },
  { label: "db",   port: "localhost:5432", color: "#4EC994",  startF: 480 },
  { label: "api",  port: "localhost:8080", color: "#DCDCAA",  startF: 500 },
];

export const CreateWorkspaceScene: React.FC = () => {
  const frame = useCurrentFrame();
  const f = frame;

  const cardOpacity = interpolate(f, [420, 460], [0, 1], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
  });
  const cardY = interpolate(f, [420, 460], [30, 0], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
  });

  const portOpacity = (startF: number) =>
    interpolate(f, [startF, startF + 30], [0, 1], {
      extrapolateLeft: "clamp",
      extrapolateRight: "clamp",
    });

  return (
    <AbsoluteFill
      style={{
        background: "#11111b",
        display: "flex",
        flexDirection: "column",
        padding: 90,
        gap: 28,
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
        Step 2 — Create, start, forward
      </div>

      <div style={{ display: "flex", flex: 1, gap: 48 }}>
        {/* Terminal */}
        <div style={{ flex: 6 }}>
          <TerminalWindow title="mac" lines={LINES} frame={f} />
        </div>

        {/* Port card */}
        <div
          style={{
            flex: 4,
            display: "flex",
            flexDirection: "column",
            justifyContent: "center",
            gap: 16,
            opacity: cardOpacity,
            transform: `translateY(${cardY}px)`,
          }}
        >
          <div
            style={{
              background: "#1e1e2e",
              border: "1px solid rgba(86,156,214,0.25)",
              borderRadius: 14,
              padding: 32,
            }}
          >
            <div
              style={{
                color: "#6c7086",
                fontSize: 13,
                fontFamily: "sans-serif",
                letterSpacing: 2,
                textTransform: "uppercase",
                marginBottom: 20,
              }}
            >
              Available on localhost
            </div>

            {PORTS.map((p) => (
              <div
                key={p.label}
                style={{
                  display: "flex",
                  justifyContent: "space-between",
                  alignItems: "center",
                  marginBottom: 18,
                  opacity: portOpacity(p.startF),
                }}
              >
                <div
                  style={{
                    background: `${p.color}18`,
                    border: `1px solid ${p.color}44`,
                    borderRadius: 6,
                    padding: "4px 12px",
                    color: p.color,
                    fontFamily: "'JetBrains Mono', monospace",
                    fontSize: 18,
                    fontWeight: 600,
                    minWidth: 52,
                    textAlign: "center",
                  }}
                >
                  {p.label}
                </div>
                <div
                  style={{
                    color: "#a6adc8",
                    fontFamily: "'JetBrains Mono', monospace",
                    fontSize: 17,
                  }}
                >
                  {p.port}
                </div>
              </div>
            ))}

            <div
              style={{
                marginTop: 20,
                borderTop: "1px solid rgba(255,255,255,0.06)",
                paddingTop: 16,
                color: "#6c7086",
                fontFamily: "sans-serif",
                fontSize: 14,
              }}
            >
              curl, browser, psql — any tool works
            </div>
          </div>
        </div>
      </div>
    </AbsoluteFill>
  );
};
