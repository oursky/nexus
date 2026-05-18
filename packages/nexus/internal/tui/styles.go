package tui

import "github.com/oursky/nexus/packages/nexus/internal/tui/design"

// Backward-compatible aliases to design tokens.
var (
	colorAccent  = design.Accent
	colorSuccess = design.Success
	colorWarning = design.Warning
	colorError   = design.Error
	colorMuted   = design.Muted
	colorText    = design.Text
	colorSubtext = design.Subtext
	colorBorder  = design.Border
	colorSelBg   = design.SelBg

	titleStyle        = design.TitleStyle
	statusOkStyle     = design.StatusOkStyle
	statusErrStyle    = design.StatusErrStyle
	mutedStyle        = design.MutedStyle
	detailKeyStyle    = design.DetailKeyStyle
	detailValStyle    = design.DetailValStyle
	warningStyle      = design.WarningStyle
	accentStyle       = design.AccentStyle
	separatorStyle    = design.SeparatorStyle
	sectionLabelStyle = design.SectionLabelStyle
	activeTabStyle    = design.ActiveTabStyle
	inactiveTabStyle  = design.InactiveTabStyle
	tabSepStyle       = design.TabSepStyle
)
