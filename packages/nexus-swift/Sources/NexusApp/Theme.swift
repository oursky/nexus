import NexusCore
import SwiftUI
import AppKit

// MARK: - Color tokens
// All values measured directly from the Conductor screenshot.
// Text tokens use NSColor so they adapt correctly to the sidebar vibrancy material.

enum Theme {

    // ── Backgrounds ────────────────────────────────────────────────
    /// Window / main pane.
    static let bgApp     = Color(nsColor: .windowBackgroundColor)
    /// Main content area.
    static let bgContent = Color(nsColor: .textBackgroundColor)
    /// Inset terminal card background
    static let bgTerm    = Color(hex: "#1C1C1C")
    /// Elevated panels/sheets should follow system window background to avoid
    /// appearance-specific contrast regressions across different macOS setups.
    static let bgElevated = Color(nsColor: .windowBackgroundColor)
    /// Sidebar – handled by NSVisualEffectView, no hex needed

    // ── Sidebar interaction ────────────────────────────────────────
    /// Conductor selected row — very light, not as dark as macOS default list selection
    static let sidebarSelected = Color.black.opacity(0.05)
    static let sidebarHover    = Color.black.opacity(0.03)

    // ── Borders ────────────────────────────────────────────────────
    static let separator     = Color(nsColor: .separatorColor).opacity(0.65)
    static let separatorMid  = Color(nsColor: .separatorColor)
    static let badgeMutedBg  = Color(nsColor: .quaternaryLabelColor).opacity(0.18)

    // ── Semantic text (NSColor adapts to vibrancy/appearances) ─────
    static let label          = Color(nsColor: .labelColor)
    static let labelSecondary = Color(nsColor: .secondaryLabelColor)
    static let labelTertiary  = Color(nsColor: .tertiaryLabelColor)

    // ── Status ─────────────────────────────────────────────────────
    static let green  = Color(nsColor: .systemGreen)
    static let orange = Color(nsColor: .systemOrange)
    static let yellow = Color(nsColor: .systemYellow)
    static let red    = Color(nsColor: .systemRed)

    // ── Accent – matches Conductor's coral/red brand color ─────────
    static let accent = Color(nsColor: .controlAccentColor)

    // ── Terminal syntax ────────────────────────────────────────────
    static let termText   = Color(hex: "#D4D4D4")
    static let termDim    = Color(hex: "#6B6B6B")
    static let termGreen  = Color(hex: "#4EC994")
    static let termBlue   = Color(hex: "#569CD6")
    static let termYellow = Color(hex: "#DCDCAA")

    // ── Typography ─────────────────────────────────────────────────
    static let fontSm   = Font.system(size: 11)
    static let fontBody = Font.system(size: 13)
    static let fontMono = Font.system(size: 12, design: .monospaced)
    static let spaceSm: CGFloat = 8
    static let spaceMd: CGFloat = 12
    static let spaceLg: CGFloat = 16
    static let spaceXl: CGFloat = 20

    // ── Helpers ────────────────────────────────────────────────────
    static func statusColor(_ s: WorkspaceStatus) -> Color {
        switch s {
        case .running, .restored: return green
        case .starting:           return orange
        case .paused:             return orange
        case .stopped, .created:  return Color(nsColor: .tertiaryLabelColor)
        }
    }
}

// MARK: - Hex init

extension Color {
    init(hex: String) {
        let h = hex.trimmingCharacters(in: CharacterSet.alphanumerics.inverted)
        var n: UInt64 = 0
        Scanner(string: h).scanHexInt64(&n)
        self.init(
            red:   Double((n >> 16) & 0xFF) / 255,
            green: Double((n >>  8) & 0xFF) / 255,
            blue:  Double( n        & 0xFF) / 255
        )
    }
}

// MARK: - NSColor Hex init (for SwiftTerm terminal styling)

extension NSColor {
    convenience init?(hex: String) {
        let s = hex.hasPrefix("#") ? String(hex.dropFirst()) : hex
        guard s.count == 6, let n = UInt64(s, radix: 16) else { return nil }
        self.init(
            red:   CGFloat((n >> 16) & 0xFF) / 255,
            green: CGFloat((n >>  8) & 0xFF) / 255,
            blue:  CGFloat( n        & 0xFF) / 255,
            alpha: 1
        )
    }
}

// MARK: - Sidebar vibrancy

struct SidebarMaterial: NSViewRepresentable {
    func makeNSView(context: Context) -> NSVisualEffectView {
        let v = NSVisualEffectView()
        v.material      = .sidebar
        v.blendingMode  = .behindWindow
        v.state         = .active
        return v
    }
    func updateNSView(_ v: NSVisualEffectView, context: Context) {}
}
