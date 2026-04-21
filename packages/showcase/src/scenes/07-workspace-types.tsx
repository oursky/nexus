import React from "react";
import { AbsoluteFill, interpolate, spring, useCurrentFrame, useVideoConfig } from "remotion";
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
  { local: 3000, remote: 3000, process: "compose: web",  tunneled: true },
  { local: 5432, remote: 5432, process: "compose: db",   tunneled: true },
  { local: 8080, remote: 8080, process: "compose: api",  tunneled: true },
];

export const WorkspaceTypesScene: React.FC = () => {
  const frame = useCurrentFrame();
  const { fps } = useVideoConfig();
  const f = frame;

  const sceneIn = spring({ frame: f, fps, config: { damping: 22, stiffness: 70 } });
  const sceneY = interpolate(sceneIn, [0, 1], [30, 0]);

  // Port rows animate in after tunnels become active

  const row0Opacity = interpolate(f, [120, 160], [0, 1], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });
  const row1Opacity = interpolate(f, [150, 190], [0, 1], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });
  const row2Opacity = interpolate(f, [180, 220], [0, 1], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });
  const rowOpacities = [row0Opacity, row1Opacity, row2Opacity];

  const isTunneledActive = f >= 100;

  // Animate tunnel dots on (tunneled = true once active)
  const portRowsAnimated = PORT_ROWS.map((row, i) => ({
    ...row,
    tunneled: isTunneledActive,
    opacity: rowOpacities[i],
  }));

  const Breadcrumb = () => (
    <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
      <span style={{ fontFamily: "-apple-system, sans-serif", fontSize: 13, fontWeight: 600, color: C.label }}>
        nexus-example
      </span>
      <span style={{ color: C.labelTertiary, fontSize: 11 }}>›</span>
      <span style={{ fontFamily: "monospace", fontSize: 13, color: C.labelSecondary }}>main</span>
      {/* firecracker badge */}
      <div
        style={{
          marginLeft: 4,
          padding: "2px 8px",
          borderRadius: 4,
          background: "rgba(0,0,0,0.05)",
          fontFamily: "-apple-system, sans-serif",
          fontSize: 11,
          color: C.labelSecondary,
        }}
      >
        firecracker
      </div>
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
        opacity: sceneIn,
        transform: `translateY(${sceneY}px)`,
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
        macOS companion app
      </div>

      <div style={{ flex: 1 }}>
        <NexusAppWindow breadcrumb={<Breadcrumb />} width="100%" height="100%">
          {/* Sidebar */}
          <AppSidebar>
            <SidebarHeader />
            <ProjectSection name="nexus-example">
              <WorkspaceRow
                name="Base"
                badge="root  VM"
                isRoot
                isSelected
                running
                hasTunnels={isTunneledActive}
              />
            </ProjectSection>
            <div style={{ padding: "4px 0" }}>
              <ProjectSection name="staging">
                <WorkspaceRow name="Base" isRoot running={false} />
              </ProjectSection>
            </div>
            <SidebarFooter connected label="Connected" />
          </AppSidebar>

          {/* Main */}
          <div style={{ flex: 1, display: "flex", flexDirection: "column" }}>
            <SessionInfoStrip
              branch="main"
              runtime="firecracker"
              path="/workspace"
              ports={isTunneledActive ? [3000, 5432, 8080] : []}
            />
            <AppTerminal />
          </div>

          {/* Inspector with animated rows */}
          <PortsInspector
            title={isTunneledActive ? "Tunnels Active" : "Tunnels Inactive"}
            rows={portRowsAnimated.map((r, i) => ({
              ...r,
              tunneled: isTunneledActive && f > 120 + i * 30,
            }))}
            showStart={!isTunneledActive}
          />
        </NexusAppWindow>
      </div>
    </AbsoluteFill>
  );
};
