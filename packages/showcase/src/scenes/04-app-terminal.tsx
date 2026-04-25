/**
 * The core demo scene: terminal (left) driving the Mac app (right) live.
 *
 * Timeline (all frames relative to scene start):
 *  0–180   nexus workspace create  → workspace appears in app sidebar
 *  180–360 nexus workspace start   → "Base" row goes green in sidebar
 *  360–540 nexus spotlight start   → port rows appear in inspector, ports light up in toolbar
 *  540–720 idle — full picture holds
 */
import React from "react";
import { AbsoluteFill, interpolate, useCurrentFrame } from "remotion";
import { TerminalWindow, TerminalLine } from "../components/TerminalWindow";
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

// ── Terminal lines ──────────────────────────────────────────────────────────

const LINES: TerminalLine[] = [
  // create
  { type: "command", text: "nexus workspace create --repo ~/my-project", startFrame: 10, charsPerFrame: 3 },
  { type: "output",  text: "  Syncing project to linuxbox...", startFrame: 60 },
  { type: "output",  text: "✓  workspace created: my-project", startFrame: 110, color: "#4EC994" },
  { type: "blank",   text: "", startFrame: 130 },
  // start
  { type: "command", text: "nexus workspace start my-project", startFrame: 190, charsPerFrame: 3 },
  { type: "output",  text: "  Booting libkrun VM...", startFrame: 240 },
  { type: "output",  text: "  Discovered: 3000 (web)  5432 (db)  8080 (api)", startFrame: 310, color: "#569CD6" },
  { type: "output",  text: "✓  workspace ready", startFrame: 360, color: "#4EC994" },
  { type: "blank",   text: "", startFrame: 380 },
  // spotlight
  { type: "command", text: "nexus spotlight start my-project", startFrame: 400, charsPerFrame: 3 },
  { type: "output",  text: "  3000 \u2192 localhost:3000", startFrame: 450, color: "#DCDCAA" },
  { type: "output",  text: "  5432 \u2192 localhost:5432", startFrame: 470, color: "#DCDCAA" },
  { type: "output",  text: "  8080 \u2192 localhost:8080", startFrame: 490, color: "#DCDCAA" },
  { type: "output",  text: "✓  forwarded 3/3 ports", startFrame: 520, color: "#4EC994" },
];

const PORT_ROWS = [
  { local: 3000, remote: 3000, process: "compose: web" },
  { local: 5432, remote: 5432, process: "compose: db" },
  { local: 8080, remote: 8080, process: "compose: api" },
];

// ── Scene ───────────────────────────────────────────────────────────────────

export const AppTerminalScene: React.FC = () => {
  const f = useCurrentFrame();

  // App panel fades in a beat after the terminal starts
  const appOpacity = interpolate(f, [20, 60], [0, 1], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });

  // Sidebar: project section appears once "workspace created" line shows
  const projectOpacity = interpolate(f, [115, 150], [0, 1], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });
  const projectY = interpolate(f, [115, 150], [10, 0], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });

  // Workspace row (Base) goes green once workspace starts
  const wsRunning = f >= 360;
  const wsOpacity = interpolate(f, [120, 155], [0, 1], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });

  // Main content area appears once workspace is running
  const detailOpacity = interpolate(f, [365, 400], [0, 1], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });

  // Inspector appears once spotlight starts
  const inspectorOpacity = interpolate(f, [405, 440], [0, 1], { extrapolateLeft: "clamp", extrapolateRight: "clamp" });

  // Port rows tunnel on one by one
  const tunneled = (rowIdx: number) => f >= 455 + rowIdx * 20;
  const activeTunnelCount = PORT_ROWS.filter((_, i) => tunneled(i)).length;
  const showActivePorts = activeTunnelCount > 0;

  const Breadcrumb = () => (
    <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
      <span style={{ fontFamily: "-apple-system, sans-serif", fontSize: 13, fontWeight: 600, color: C.label }}>
        my-project
      </span>
      <span style={{ color: C.labelTertiary, fontSize: 11 }}>›</span>
      <span style={{ fontFamily: "monospace", fontSize: 13, color: C.labelSecondary }}>main</span>
    </div>
  );

  return (
    <AbsoluteFill style={{ background: "#11111b", display: "flex", flexDirection: "row", alignItems: "stretch", padding: 60, gap: 36 }}>

      {/* ── Left: terminal ─────────────────────────────────────────────── */}
      <div style={{ flex: 11 }}>
        <TerminalWindow title="mac" lines={LINES} frame={f} fontSize={20} />
      </div>

      {/* ── Right: Mac app mockup ───────────────────────────────────────── */}
      <div style={{ flex: 13, opacity: appOpacity }}>
        <NexusAppWindow breadcrumb={<Breadcrumb />} width="100%" height="100%">

          {/* Sidebar */}
          <AppSidebar>
            <SidebarHeader />

            <div
              style={{
                opacity: projectOpacity,
                transform: `translateY(${projectY}px)`,
                flex: 1,
              }}
            >
              <ProjectSection name="my-project">
                <div style={{ opacity: wsOpacity }}>
                  <WorkspaceRow
                    name="Base"
                    badge="root  VM"
                    isRoot
                    isSelected
                    running={wsRunning}
                    hasTunnels={showActivePorts}
                  />
                </div>
              </ProjectSection>
            </div>

            <SidebarFooter connected label="Connected" />
          </AppSidebar>

          {/* Main */}
          <div style={{ flex: 1, display: "flex", flexDirection: "column", opacity: detailOpacity }}>
            <SessionInfoStrip
              branch="main"
              runtime="libkrun"
              path="/workspace"
              ports={showActivePorts ? [3000, 5432, 8080] : []}
            />
            <AppTerminal />
          </div>

          {/* Inspector */}
          <div style={{ opacity: inspectorOpacity }}>
            <PortsInspector
              title={showActivePorts ? "Tunnels Active" : "Tunnels Inactive"}
              rows={PORT_ROWS.map((row, i) => ({ ...row, tunneled: tunneled(i) }))}
              showStart={!showActivePorts}
            />
          </div>

        </NexusAppWindow>
      </div>
    </AbsoluteFill>
  );
};
