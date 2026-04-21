import React from "react";
import { AbsoluteFill, interpolate, useCurrentFrame } from "remotion";
import {
  NexusAppWindow,
  AppSidebar,
  SidebarHeader,
  ProjectSection,
  WorkspaceRow,
  SidebarFooter,
  SessionInfoStrip,
  AppTerminal,
  PortsInspector,
  C,
} from "../components/NexusAppWindow";

const PORT_ROWS = [
  { local: 3000, remote: 3000, process: "compose: web" },
  { local: 5432, remote: 5432, process: "compose: db" },
  { local: 8080, remote: 8080, process: "compose: api" },
];

export const MacOSConnectScene: React.FC = () => {
  const frame = useCurrentFrame();
  const f = frame;

  // Beat 1 (0–120): app fades in, sidebar shows connecting state
  const appOpacity = interpolate(f, [0, 40], [0, 1], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });
  const appY = interpolate(f, [0, 40], [30, 0], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });

  // Beat 2 (100–220): "Connecting…" → "Connected"
  const connectingOpacity = interpolate(f, [80, 120, 200, 220], [0, 1, 1, 0], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });
  // connectedOpacity drives the footer label via the isConnected flag below

  // Beat 3 (240–450): project + workspace appears in sidebar
  const sidebarProjectOpacity = interpolate(f, [240, 280], [0, 1], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });
  const sidebarProjectY = interpolate(f, [240, 280], [12, 0], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });
  const wsOpacity = interpolate(f, [300, 340], [0, 1], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });
  const wsY = interpolate(f, [300, 340], [8, 0], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });

  // Beat 4 (400+): main content area fades in (selected workspace)
  const detailOpacity = interpolate(f, [380, 430], [0, 1], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });

  // Inspector slides in at 480
  const inspectorOpacity = interpolate(f, [480, 530], [0, 1], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });
  const inspectorX = interpolate(f, [480, 530], [40, 0], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });

  const isConnected = f >= 220;

  const Breadcrumb = () => (
    <div style={{ display: "flex", alignItems: "center", gap: 6, opacity: detailOpacity }}>
      <span style={{ fontFamily: "-apple-system, sans-serif", fontSize: 13, fontWeight: 600, color: C.label }}>
        nexus-example
      </span>
      <span style={{ color: C.labelTertiary, fontSize: 11 }}>›</span>
      <span style={{ fontFamily: "monospace", fontSize: 13, color: C.labelSecondary }}>main</span>
    </div>
  );

  return (
    <AbsoluteFill
      style={{
        background: "#11111b",
        display: "flex",
        flexDirection: "column",
        padding: 56,
        gap: 20,
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
        macOS app — connect
      </div>

      <div style={{ flex: 1, opacity: appOpacity, transform: `translateY(${appY}px)` }}>
        <NexusAppWindow breadcrumb={<Breadcrumb />} width="100%" height="100%">
          {/* Sidebar */}
          <AppSidebar>
            <SidebarHeader />

            {/* Project section */}
            <div
              style={{
                opacity: sidebarProjectOpacity,
                transform: `translateY(${sidebarProjectY}px)`,
                flex: 1,
              }}
            >
              <ProjectSection name="nexus-example">
                <div style={{ opacity: wsOpacity, transform: `translateY(${wsY}px)` }}>
                  <WorkspaceRow
                    name="Base"
                    badge="root  VM"
                    isRoot
                    isSelected
                    running
                  />
                </div>
              </ProjectSection>
            </div>

            <SidebarFooter
              connected={isConnected}
              label={
                connectingOpacity > 0.1 && !isConnected
                  ? "Connecting…"
                  : isConnected
                  ? "Connected"
                  : "Offline"
              }
            />
          </AppSidebar>

          {/* Main area */}
          <div style={{ flex: 1, display: "flex", flexDirection: "column", opacity: detailOpacity }}>
            <SessionInfoStrip branch="main" runtime="firecracker" path="/workspace" />
            <AppTerminal />
          </div>

          {/* Inspector */}
          <div
            style={{
              opacity: inspectorOpacity,
              transform: `translateX(${inspectorX}px)`,
              display: "flex",
            }}
          >
            <PortsInspector title="Tunnels Inactive" rows={PORT_ROWS} showStart />
          </div>
        </NexusAppWindow>
      </div>
    </AbsoluteFill>
  );
};
