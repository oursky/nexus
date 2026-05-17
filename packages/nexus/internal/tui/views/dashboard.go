package views

import (
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/oursky/nexus/packages/nexus/internal/tui/design"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
)

// DashboardView renders the workspace list with project groups.
// This is the main landing view showing all workspaces organized by project.
type DashboardView struct {
	width      int
	height     int
	showFilter bool
	list       list.Model
}

// NewDashboardView creates a new dashboard view instance.
func NewDashboardView(width, height int) *DashboardView {
	delegate := list.NewDefaultDelegate()
	workspaceList := list.New(nil, delegate, width, height)

	t := design.GetTheme()
	s := design.GetStyles()

	workspaceList.Styles.Title = s.Title
	workspaceList.Styles.TitleBar = s.Title.Background(t.Colors.BgSubtle).Padding(design.SpaceSM, design.SpaceMD)
	workspaceList.Styles.FilterPrompt = s.Body
	workspaceList.Styles.FilterCursor = s.AccentStyle
	workspaceList.Styles.DefaultFilterCharacterMatch = s.AccentStyle
	workspaceList.Styles.StatusBar = s.Caption.Background(t.Colors.BgSubtle)
	workspaceList.Styles.StatusEmpty = s.MutedStyle
	workspaceList.Styles.StatusBarActiveFilter = s.Body.Background(t.Colors.BgSubtle)
	workspaceList.Styles.StatusBarFilterCount = s.Caption.Background(t.Colors.BgSubtle)
	workspaceList.Styles.NoItems = s.MutedStyle
	workspaceList.Styles.PaginationStyle = s.Caption
	workspaceList.Styles.HelpStyle = s.Caption
	workspaceList.Styles.ActivePaginationDot = s.AccentStyle
	workspaceList.Styles.InactivePaginationDot = s.MutedStyle
	workspaceList.Styles.ArabicPagination = s.Caption
	workspaceList.Styles.DividerDot = s.MutedStyle

	return &DashboardView{
		width:      width,
		height:     height,
		showFilter: false,
		list:       workspaceList,
	}
}

// Update handles user input in the dashboard view.
func (v *DashboardView) Update(msg tea.Msg, m *model.AppModel) (*model.AppModel, tea.Cmd) {
	result, cmd := m, tea.Cmd(nil)

	// Handle filter toggle
	if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.Type == tea.KeyRunes && string(keyMsg.Runes) == "/" {
		v.showFilter = !v.ShowFilter()
		return result, nil
	}

	// Update list
	var listCmd tea.Cmd
	v.list, listCmd = v.list.Update(msg)
	cmd = tea.Batch(cmd, listCmd)

	// For now, pass through to existing tui behavior
	// TODO: Implement workspace selection, filtering, actions
	// These will be filled in by Wave 7 (T16-T18)

	return result, cmd
}

// View renders the dashboard workspace list.
func (v *DashboardView) View(m *model.AppModel) string {
	v.list.SetSize(m.Width(), m.Height()-4)
	wsList := m.WorkspaceList()
	v.list.SetItems(wsList.Items())
	return v.list.View()
}

// SetSize updates the view dimensions.
func (v *DashboardView) SetSize(width, height int) {
	v.width = width
	v.height = height
	v.list.SetWidth(width)
	v.list.SetHeight(height)
}

// ShowFilter returns whether the filter input is visible.
func (v *DashboardView) ShowFilter() bool {
	return v.showFilter
}
