import React from "react";

/**
 * Faithful recreation of the Nexus macOS app window chrome.
 * Light theme: bgApp #F5F4F2, bgContent #FFFFFF, dark terminal #1C1C1C.
 * Accent: #E84343 (coral red). Green: #28CD41.
 */

// ── Color tokens (from Theme.swift) ────────────────────────────────────────
export const C = {
  bgApp:     "#F5F4F2",  // warm off-white window bg
  bgContent: "#FFFFFF",  // main pane
  bgTerm:    "#1C1C1C",  // terminal
  bgSidebar: "#EBEBEB",  // approximate sidebar vibrancy
  separator: "rgba(0,0,0,0.09)",
  label:     "#000000",
  labelSecondary: "rgba(0,0,0,0.50)",
  labelTertiary:  "rgba(0,0,0,0.28)",
  accent:    "#E84343",
  green:     "#28CD41",
  orange:    "#FF9500",
};

interface NexusAppWindowProps {
  children: React.ReactNode;
  /** Toolbar center breadcrumb content */
  breadcrumb?: React.ReactNode;
  width?: number | string;
  height?: number | string;
}

/**
 * Renders the outer macOS window shell with traffic lights + unified toolbar.
 * Children are rendered in the content area (below the toolbar).
 */
export const NexusAppWindow: React.FC<NexusAppWindowProps> = ({
  children,
  breadcrumb,
  width = "100%",
  height = "100%",
}) => {
  return (
    <div
      style={{
        width,
        height,
        background: C.bgApp,
        borderRadius: 12,
        overflow: "hidden",
        boxShadow: "0 24px 80px rgba(0,0,0,0.35)",
        display: "flex",
        flexDirection: "column",
        border: "1px solid rgba(0,0,0,0.18)",
      }}
    >
      {/* Unified title bar / toolbar */}
      <div
        style={{
          background: C.bgContent,
          height: 52,
          display: "flex",
          alignItems: "center",
          paddingLeft: 16,
          paddingRight: 12,
          borderBottom: `1px solid ${C.separator}`,
          flexShrink: 0,
          gap: 12,
        }}
      >
        {/* Traffic lights */}
        <div style={{ display: "flex", gap: 8, flexShrink: 0 }}>
          <div style={{ width: 13, height: 13, borderRadius: "50%", background: "#FF5F57" }} />
          <div style={{ width: 13, height: 13, borderRadius: "50%", background: "#FFBD2E" }} />
          <div style={{ width: 13, height: 13, borderRadius: "50%", background: "#28C840" }} />
        </div>

        {/* Sidebar toggle icon placeholder */}
        <div style={{ width: 22, height: 22, opacity: 0.25, flexShrink: 0 }}>
          <svg viewBox="0 0 22 22" fill="none">
            <rect x="1" y="1" width="20" height="20" rx="4" stroke="#000" strokeWidth="1.5"/>
            <line x1="7" y1="1" x2="7" y2="21" stroke="#000" strokeWidth="1.5"/>
          </svg>
        </div>

        {/* Breadcrumb */}
        <div style={{ flex: 1, display: "flex", justifyContent: "center" }}>
          {breadcrumb}
        </div>

        {/* Right toolbar icons (inspector + action menu) */}
        <div style={{ display: "flex", gap: 10, flexShrink: 0, opacity: 0.4 }}>
          <svg width="18" height="18" viewBox="0 0 18 18" fill="none">
            <rect x="1" y="1" width="16" height="16" rx="3" stroke="#000" strokeWidth="1.4"/>
            <line x1="12" y1="1" x2="12" y2="17" stroke="#000" strokeWidth="1.4"/>
          </svg>
          <svg width="18" height="18" viewBox="0 0 18 18" fill="none">
            <circle cx="9" cy="9" r="8" stroke="#000" strokeWidth="1.4"/>
            <circle cx="9" cy="6" r="1" fill="#000"/>
            <line x1="9" y1="9" x2="9" y2="13" stroke="#000" strokeWidth="1.4" strokeLinecap="round"/>
          </svg>
        </div>
      </div>

      {/* Content */}
      <div style={{ flex: 1, overflow: "hidden", display: "flex" }}>{children}</div>
    </div>
  );
};

/** Sidebar with vibrancy-approximated light background */
export const AppSidebar: React.FC<{ children: React.ReactNode }> = ({ children }) => (
  <div
    style={{
      width: 220,
      background: C.bgSidebar,
      borderRight: `1px solid ${C.separator}`,
      display: "flex",
      flexDirection: "column",
      flexShrink: 0,
    }}
  >
    {children}
  </div>
);

/** "Projects" header row in sidebar */
export const SidebarHeader: React.FC = () => (
  <div
    style={{
      height: 36,
      display: "flex",
      alignItems: "center",
      paddingLeft: 16,
      paddingRight: 6,
      borderBottom: `1px solid ${C.separator}`,
    }}
  >
    <span
      style={{
        fontFamily: "-apple-system, BlinkMacSystemFont, sans-serif",
        fontSize: 12,
        color: C.labelSecondary,
        flex: 1,
      }}
    >
      Projects
    </span>
    {/* + icon */}
    <div style={{ width: 26, height: 26, display: "flex", alignItems: "center", justifyContent: "center", opacity: 0.4 }}>
      <svg width="13" height="13" viewBox="0 0 13 13" fill="none">
        <line x1="6.5" y1="1" x2="6.5" y2="12" stroke="#000" strokeWidth="1.5" strokeLinecap="round"/>
        <line x1="1" y1="6.5" x2="12" y2="6.5" stroke="#000" strokeWidth="1.5" strokeLinecap="round"/>
      </svg>
    </div>
  </div>
);

/** Collapsible project section */
export const ProjectSection: React.FC<{ name: string; children: React.ReactNode }> = ({ name, children }) => (
  <div style={{ paddingTop: 4 }}>
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: 4,
        padding: "5px 12px",
      }}
    >
      {/* chevron */}
      <svg width="9" height="9" viewBox="0 0 9 9" fill="none" style={{ transform: "rotate(90deg)" }}>
        <path d="M2 3L4.5 6L7 3" stroke="rgba(0,0,0,0.45)" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round"/>
      </svg>
      <span style={{ fontFamily: "-apple-system, sans-serif", fontSize: 11, fontWeight: 500, color: C.labelSecondary }}>
        {name}
      </span>
    </div>
    {children}
  </div>
);

interface WorkspaceRowProps {
  name: string;
  badge?: string;
  isRoot?: boolean;
  isSelected?: boolean;
  running?: boolean;
  hasTunnels?: boolean;
  style?: React.CSSProperties;
}

/** Workspace row in sidebar */
export const WorkspaceRow: React.FC<WorkspaceRowProps> = ({
  name, badge, isRoot, isSelected, running, hasTunnels, style
}) => (
  <div
    style={{
      display: "flex",
      alignItems: "center",
      gap: 6,
      margin: "1px 6px",
      padding: "5px 10px",
      borderRadius: 5,
      background: isSelected ? "rgba(0,0,0,0.06)" : "transparent",
      ...style,
    }}
  >
    {/* Status dot */}
    <div style={{ position: "relative", width: 14, height: 14, flexShrink: 0 }}>
      <div
        style={{
          position: "absolute",
          inset: 0,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
        }}
      >
        <div
          style={{
            width: 7,
            height: 7,
            borderRadius: "50%",
            background: running ? C.green : "transparent",
            border: running ? "none" : "1.5px solid rgba(0,0,0,0.28)",
          }}
        />
      </div>
    </div>
    <span style={{ fontFamily: "-apple-system, sans-serif", fontSize: 13, color: C.label, flex: 1 }}>
      {name}
    </span>
    {isRoot && (
      <span style={{ fontFamily: "-apple-system, sans-serif", fontSize: 10, fontWeight: 500, color: C.labelTertiary }}>
        root
      </span>
    )}
    {hasTunnels && (
      <svg width="12" height="10" viewBox="0 0 12 10" fill="none">
        <path d="M1 5 Q6 1 11 5 Q6 9 1 5Z" fill={C.accent} opacity="0.8"/>
      </svg>
    )}
    {badge && (
      <span style={{ fontFamily: "-apple-system, sans-serif", fontSize: 9, fontWeight: 500, color: C.labelTertiary }}>
        {badge}
      </span>
    )}
  </div>
);

/** Sidebar footer with gear + connection status */
export const SidebarFooter: React.FC<{ connected?: boolean; label?: string }> = ({
  connected = true,
  label = "Connected",
}) => (
  <div
    style={{
      height: 34,
      borderTop: `1px solid ${C.separator}`,
      display: "flex",
      alignItems: "center",
      paddingLeft: 8,
      paddingRight: 10,
      gap: 4,
      marginTop: "auto",
    }}
  >
    {/* Gear */}
    <div style={{ width: 28, height: 28, display: "flex", alignItems: "center", justifyContent: "center", opacity: 0.35 }}>
      <svg width="14" height="14" viewBox="0 0 14 14" fill="none">
        <circle cx="7" cy="7" r="2.2" stroke="#000" strokeWidth="1.3"/>
        <circle cx="7" cy="7" r="6" stroke="#000" strokeWidth="1.3" strokeDasharray="2.4 1.8"/>
      </svg>
    </div>
    <div style={{ flex: 1 }} />
    {/* Connection pill */}
    <div style={{ display: "flex", alignItems: "center", gap: 5 }}>
      <div
        style={{
          width: 6,
          height: 6,
          borderRadius: "50%",
          background: connected ? C.green : "rgba(0,0,0,0.28)",
        }}
      />
      <span style={{ fontFamily: "-apple-system, sans-serif", fontSize: 10, color: C.labelSecondary }}>
        {label}
      </span>
    </div>
  </div>
);

/** Session info strip above the terminal */
export const SessionInfoStrip: React.FC<{
  branch?: string;
  runtime?: string;
  path?: string;
  ports?: number[];
}> = ({ branch = "main", runtime, path = "/workspace", ports = [] }) => (
  <div
    style={{
      height: 34,
      background: C.bgContent,
      borderBottom: `1px solid ${C.separator}`,
      display: "flex",
      alignItems: "center",
      paddingLeft: 16,
      paddingRight: 16,
      gap: 16,
    }}
  >
    <span style={{ display: "flex", alignItems: "center", gap: 4 }}>
      <span style={{ fontSize: 10, color: C.labelTertiary }}>⌥</span>
      <span style={{ fontFamily: "monospace", fontSize: 11, color: C.labelSecondary }}>{branch}</span>
    </span>
    {runtime && (
      <>
        <div style={{ width: 1, height: 12, background: C.separator }} />
        <span style={{ display: "flex", alignItems: "center", gap: 4 }}>
          <span style={{ fontSize: 10, color: C.labelTertiary }}>◻</span>
          <span style={{ fontFamily: "monospace", fontSize: 11, color: C.labelSecondary }}>{runtime}</span>
        </span>
      </>
    )}
    <div style={{ width: 1, height: 12, background: C.separator }} />
    <span style={{ display: "flex", alignItems: "center", gap: 4 }}>
      <span style={{ fontSize: 10, color: C.labelTertiary }}>📁</span>
      <span style={{ fontFamily: "monospace", fontSize: 11, color: C.labelSecondary }}>{path}</span>
    </span>
    {ports.length > 0 && (
      <>
        <div style={{ width: 1, height: 12, background: C.separator }} />
        <span style={{ display: "flex", alignItems: "center", gap: 4 }}>
          <span style={{ fontSize: 10, color: C.labelTertiary }}>⇄</span>
          {ports.map((p) => (
            <span key={p} style={{ fontFamily: "monospace", fontSize: 11, color: C.green, fontWeight: 600 }}>{p}</span>
          ))}
        </span>
      </>
    )}
  </div>
);

/** Right inspector panel with ports table */
export const PortsInspector: React.FC<{
  title?: string;
  rows: { local: number; remote: number; process: string; tunneled?: boolean }[];
  showStart?: boolean;
  style?: React.CSSProperties;
}> = ({ title = "Tunnels Inactive", rows, showStart = true, style }) => (
  <div
    style={{
      width: 340,
      background: C.bgContent,
      borderLeft: `1px solid ${C.separator}`,
      display: "flex",
      flexDirection: "column",
      flexShrink: 0,
      ...style,
    }}
  >
    {/* Header */}
    <div style={{ padding: "10px 12px 10px", borderBottom: `1px solid ${C.separator}` }}>
      <div style={{ display: "flex", alignItems: "center", marginBottom: 4 }}>
        <span
          style={{
            fontFamily: "-apple-system, sans-serif",
            fontSize: 11,
            fontWeight: 500,
            color: rows.some((r) => r.tunneled) ? C.green : C.labelSecondary,
            flex: 1,
          }}
        >
          {title}
        </span>
        {showStart && (
          <span style={{ fontFamily: "-apple-system, sans-serif", fontSize: 10, fontWeight: 500, color: C.accent }}>
            Start
          </span>
        )}
      </div>
      <span style={{ fontFamily: "-apple-system, sans-serif", fontSize: 10, color: C.labelTertiary }}>
        Only one sandbox can have active tunnels at a time.
      </span>
    </div>

    {/* Table header */}
    <div
      style={{
        display: "flex",
        padding: "5px 12px",
        borderBottom: `1px solid ${C.separator}`,
        background: C.bgContent,
      }}
    >
      {["Local", "Remote", "Process", "State", "Action"].map((h, i) => (
        <span
          key={h}
          style={{
            fontFamily: "-apple-system, sans-serif",
            fontSize: 11,
            fontWeight: 600,
            color: C.labelSecondary,
            flex: i === 2 ? 1 : "none",
            width: i === 0 || i === 1 ? 48 : i === 3 ? 40 : i === 4 ? 80 : undefined,
          }}
        >
          {h}
        </span>
      ))}
    </div>

    {/* Rows */}
    {rows.map((row) => (
      <div
        key={row.local}
        style={{
          display: "flex",
          alignItems: "flex-start",
          padding: "6px 12px",
          borderBottom: `1px solid ${C.separator}`,
        }}
      >
        <span style={{ width: 48, fontFamily: "monospace", fontSize: 11, fontWeight: 500, color: C.label }}>
          {row.local}
        </span>
        <span style={{ width: 48, fontFamily: "monospace", fontSize: 11, color: C.labelSecondary }}>
          {row.remote}
        </span>
        <span style={{ flex: 1, fontFamily: "-apple-system, sans-serif", fontSize: 11, color: C.labelSecondary }}>
          {row.process}
        </span>
        <span style={{ width: 40, display: "flex", alignItems: "center", gap: 3 }}>
          <div
            style={{
              width: 5,
              height: 5,
              borderRadius: "50%",
              background: row.tunneled ? C.green : C.labelTertiary,
            }}
          />
          <span
            style={{
              fontFamily: "-apple-system, sans-serif",
              fontSize: 10,
              color: row.tunneled ? C.green : C.labelTertiary,
            }}
          >
            {row.tunneled ? "On" : "Off"}
          </span>
        </span>
        <span style={{ width: 80, display: "flex", gap: 6, justifyContent: "flex-end" }}>
          <span style={{ fontFamily: "-apple-system, sans-serif", fontSize: 10, fontWeight: 500, color: C.accent }}>
            Add
          </span>
          <span style={{ fontFamily: "-apple-system, sans-serif", fontSize: 10, fontWeight: 500, color: C.labelSecondary }}>
            Open ↗
          </span>
        </span>
      </div>
    ))}
  </div>
);

/** Simulated dark terminal inside the app */
export const AppTerminal: React.FC<{ prompt?: string }> = ({ prompt = "root@(none):/workspace# " }) => (
  <div
    style={{
      flex: 1,
      background: C.bgApp,
      padding: 12,
    }}
  >
    <div
      style={{
        width: "100%",
        height: "100%",
        background: C.bgTerm,
        borderRadius: 8,
        border: "1px solid rgba(255,255,255,0.06)",
        overflow: "hidden",
        padding: 12,
        display: "flex",
        flexDirection: "column",
      }}
    >
      {/* Tab bar */}
      <div style={{ display: "flex", alignItems: "center", gap: 2, marginBottom: 8 }}>
        <div
          style={{
            padding: "3px 12px",
            borderRadius: 4,
            background: "rgba(255,255,255,0.08)",
            fontFamily: "-apple-system, sans-serif",
            fontSize: 11,
            color: "rgba(255,255,255,0.7)",
          }}
        >
          Terminal 1
        </div>
        <div
          style={{
            width: 20,
            height: 20,
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            opacity: 0.4,
          }}
        >
          <span style={{ color: "#fff", fontSize: 14 }}>+</span>
        </div>
      </div>
      <div
        style={{
          fontFamily: "monospace",
          fontSize: 12,
          color: "rgba(255,255,255,0.85)",
          flex: 1,
        }}
      >
        {prompt}
        <span
          style={{
            display: "inline-block",
            width: 8,
            height: 14,
            background: "rgba(255,255,255,0.7)",
            verticalAlign: "middle",
          }}
        />
      </div>
    </div>
  </div>
);
