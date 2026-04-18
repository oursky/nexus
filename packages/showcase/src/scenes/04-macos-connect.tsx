import React from "react";
import { AbsoluteFill, interpolate, useCurrentFrame } from "remotion";
import { MacOSWindow } from "../components/MacOSWindow";


const TOKEN_DISPLAY = "87f1a6fc••••••••";

const WORKSPACES = [
  { name: "webapp-dev", status: "Running", runtime: "Next.js 14" },
  { name: "api-server", status: "Running", runtime: "Node 20" },
];

export const MacOSConnectScene: React.FC = () => {
  const frame = useCurrentFrame();
  const f = frame;

  // Beat 1 (0–200): settings panel, profile fades in
  const profileRowOpacity = interpolate(f, [60, 100], [0, 1], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });
  const profileRowX = interpolate(f, [60, 100], [20, 0], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });

  // Beat 2 (200–400): connect button → status transition
  const connectBtnGlow = interpolate(f, [200, 230], [0, 1], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });
  const spinnerOpacity = interpolate(f, [240, 260, 320, 340], [0, 1, 1, 0], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });
  const connectedOpacity = interpolate(f, [330, 360], [0, 1], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });
  const statusDotColor = f >= 330 ? "#a6e3a1" : "#585b70";
  const statusDotScale = interpolate(f, [330, 370], [0.5, 1], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });

  // Beat 3 (400–900): workspaces list
  const sidebarWorkspacesOpacity = interpolate(f, [400, 440], [0, 1], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });
  const ws0Opacity = interpolate(f, [440, 480], [0, 1], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });
  const ws0Y = interpolate(f, [440, 480], [20, 0], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });
  const ws1Opacity = interpolate(f, [500, 540], [0, 1], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });
  const ws1Y = interpolate(f, [500, 540], [20, 0], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });
  const wsOpacities = [ws0Opacity, ws1Opacity];
  const wsYs = [ws0Y, ws1Y];

  return (
    <AbsoluteFill style={{ background: "#11111b", padding: 48 }}>
      <MacOSWindow title="Nexus" width="100%" height="100%">
        <div style={{ display: "flex", height: "100%" }}>
          {/* Sidebar */}
          <div style={{ width: 260, background: "#181825", borderRight: "1px solid rgba(255,255,255,0.06)", padding: 24, flexShrink: 0, display: "flex", flexDirection: "column", gap: 24 }}>
            <div style={{ color: "#6c7086", fontSize: 16, fontFamily: "sans-serif", letterSpacing: 2, textTransform: "uppercase" }}>
              Remote Profiles
            </div>
            <div
              style={{
                opacity: profileRowOpacity,
                transform: `translateX(${profileRowX}px)`,
                background: "rgba(137,180,250,0.1)",
                border: "1px solid rgba(137,180,250,0.3)",
                borderRadius: 8,
                padding: "10px 14px",
              }}
            >
              <div style={{ color: "#89b4fa", fontFamily: "sans-serif", fontSize: 22, fontWeight: 600 }}>linuxbox</div>
              <div style={{ color: "#6c7086", fontFamily: "'JetBrains Mono', monospace", fontSize: 16, marginTop: 4 }}>linuxbox:7777</div>
            </div>

            <div style={{ opacity: sidebarWorkspacesOpacity }}>
              <div style={{ color: "#6c7086", fontSize: 16, fontFamily: "sans-serif", letterSpacing: 2, textTransform: "uppercase", marginBottom: 12 }}>
                Workspaces
              </div>
              {WORKSPACES.map((ws, i) => (
                <div
                  key={ws.name}
                  style={{
                    opacity: wsOpacities[i],
                    transform: `translateY(${wsYs[i]}px)`,
                    padding: "8px 12px",
                    borderRadius: 8,
                    marginBottom: 6,
                    display: "flex",
                    alignItems: "center",
                    gap: 8,
                  }}
                >
                  <div style={{ width: 8, height: 8, borderRadius: "50%", background: "#a6e3a1", flexShrink: 0 }} />
                  <div>
                    <div style={{ color: "#cdd6f4", fontFamily: "sans-serif", fontSize: 20, fontWeight: 500 }}>{ws.name}</div>
                    <div style={{ color: "#6c7086", fontFamily: "sans-serif", fontSize: 16 }}>{ws.runtime}</div>
                  </div>
                </div>
              ))}
            </div>
          </div>

          {/* Main content */}
          <div style={{ flex: 1, padding: 48, display: "flex", flexDirection: "column", gap: 32 }}>
            <div style={{ color: "#cdd6f4", fontFamily: "sans-serif", fontSize: 32, fontWeight: 700 }}>
              Remote Profiles
            </div>

            {/* Profile card */}
            <div
              style={{
                background: "#1e1e2e",
                border: "1px solid rgba(255,255,255,0.08)",
                borderRadius: 12,
                padding: 28,
                display: "flex",
                flexDirection: "column",
                gap: 14,
              }}
            >
              <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                <span style={{ color: "#a6adc8", fontFamily: "sans-serif", fontSize: 20 }}>Host</span>
                <span style={{ color: "#cdd6f4", fontFamily: "'JetBrains Mono', monospace", fontSize: 20 }}>linuxbox</span>
              </div>
              <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                <span style={{ color: "#a6adc8", fontFamily: "sans-serif", fontSize: 20 }}>Port</span>
                <span style={{ color: "#cdd6f4", fontFamily: "'JetBrains Mono', monospace", fontSize: 20 }}>7777</span>
              </div>
              <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                <span style={{ color: "#a6adc8", fontFamily: "sans-serif", fontSize: 20 }}>Token</span>
                <span style={{ color: "#f9e2af", fontFamily: "'JetBrains Mono', monospace", fontSize: 20 }}>{TOKEN_DISPLAY}</span>
              </div>

              <div style={{ borderTop: "1px solid rgba(255,255,255,0.06)", paddingTop: 16, display: "flex", alignItems: "center", justifyContent: "space-between" }}>
                {/* Status */}
                <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                  <div
                    style={{
                      width: 10,
                      height: 10,
                      borderRadius: "50%",
                      background: statusDotColor,
                      transform: `scale(${statusDotScale})`,
                      transition: "none",
                    }}
                  />
                  {spinnerOpacity > 0 && (
                    <span style={{ color: "#6c7086", fontFamily: "sans-serif", fontSize: 20, opacity: spinnerOpacity }}>
                      Connecting…
                    </span>
                  )}
                  <span style={{ color: "#a6e3a1", fontFamily: "sans-serif", fontSize: 20, fontWeight: 600, opacity: connectedOpacity }}>
                    Connected
                  </span>
                </div>

                {/* Connect button */}
                <div
                  style={{
                    background: connectBtnGlow > 0.5 ? "rgba(137,180,250,0.25)" : "rgba(137,180,250,0.1)",
                    border: `1px solid rgba(137,180,250,${0.3 + connectBtnGlow * 0.4})`,
                    borderRadius: 8,
                    padding: "10px 28px",
                    color: "#89b4fa",
                    fontFamily: "sans-serif",
                    fontSize: 22,
                    fontWeight: 600,
                    boxShadow: connectBtnGlow > 0 ? `0 0 16px rgba(137,180,250,${connectBtnGlow * 0.3})` : "none",
                  }}
                >
                  Connect
                </div>
              </div>
            </div>

            {/* Workspace rows */}
            <div style={{ opacity: sidebarWorkspacesOpacity }}>
              <div style={{ color: "#6c7086", fontSize: 16, fontFamily: "sans-serif", letterSpacing: 2, textTransform: "uppercase", marginBottom: 14 }}>
                Workspaces
              </div>
              <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
                {WORKSPACES.map((ws, i) => (
                  <div
                    key={ws.name}
                    style={{
                      opacity: wsOpacities[i],
                      transform: `translateY(${wsYs[i]}px)`,
                      background: "#313244",
                      border: "1px solid rgba(255,255,255,0.06)",
                      borderRadius: 10,
                      padding: "14px 20px",
                      display: "flex",
                      alignItems: "center",
                      gap: 14,
                    }}
                  >
                    <div style={{ width: 10, height: 10, borderRadius: "50%", background: "#a6e3a1", flexShrink: 0 }} />
                    <div style={{ flex: 1 }}>
                      <span style={{ color: "#cdd6f4", fontFamily: "sans-serif", fontSize: 24, fontWeight: 600 }}>{ws.name}</span>
                      <span style={{ color: "#6c7086", fontFamily: "sans-serif", fontSize: 20, marginLeft: 16 }}>● Running</span>
                      <span style={{ color: "#a6adc8", fontFamily: "'JetBrains Mono', monospace", fontSize: 18, marginLeft: 16 }}>{ws.runtime}</span>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          </div>
        </div>
      </MacOSWindow>
    </AbsoluteFill>
  );
};
