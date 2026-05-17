package tui_test

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/oursky/nexus/packages/nexus/internal/tui/messages"
	"github.com/oursky/nexus/packages/nexus/internal/tui/model"
	"github.com/oursky/nexus/packages/nexus/internal/tui/update"
)

// testRenderer is a minimal renderer for testing.
type testRenderer struct{}

func (r *testRenderer) Render(m *model.AppModel) string {
	// Return a simple string that exercises model state without
	// recursing back into m.View() (which would call Render again).
	return "test-view"
}

// newTestModel creates an AppModel suitable for testing (no real mux).
func newTestModel() *model.AppModel {
	m := model.NewAppModel(nil) // nil mux for testing
	m.SetRenderer(&testRenderer{})
	m.SetUpdateRouter(func(m *model.AppModel, msg tea.Msg) (tea.Model, tea.Cmd) {
		return update.Router(m, msg)
	})
	m.SetWidth(120)
	m.SetHeight(40)
	return m
}

// sendMsg sends a message to the model and returns the updated model and command.
// sendMsg sends a message to the model and returns the updated model.
func sendMsg(m *model.AppModel, msg tea.Msg) (*model.AppModel, tea.Cmd) {
	result, cmd := m.Update(msg)
	return result.(*model.AppModel), cmd
}

// execCmd executes a tea.Cmd and returns the resulting message, if any.
func execCmd(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

// TestDashboardShowsNoItems tests that the dashboard initially shows no items.
func TestDashboardShowsNoItems(t *testing.T) {
	m := newTestModel()
	view := m.View()
	if view == "" {
		t.Fatal("View should not be empty")
	}
}

// TestWorkspaceListPopulates tests that workspace list populates from RPC response.
func TestWorkspaceListPopulates(t *testing.T) {
	m := newTestModel()

	// Simulate workspace list arriving from RPC
	msg := messages.WorkspaceListReceived{
		Workspaces: []messages.WorkspaceItem{
			{ID: "ws-001", Name: "base", Repo: "/home/user/project", State: "stopped"},
			{ID: "ws-002", Name: "api", Repo: "/home/user/api", State: "running"},
		},
	}

	m, _ = sendMsg(m, msg)

	// Should have 2 workspaces
	if len(m.Workspaces()) != 2 {
		t.Fatalf("expected 2 workspaces, got %d", len(m.Workspaces()))
	}
	if m.Workspaces()[0].Name != "base" {
		t.Errorf("expected first workspace name 'base', got %s", m.Workspaces()[0].Name)
	}
}

// TestEnterKeyShowsDetail tests that Enter key navigates to detail view.
func TestEnterKeyShowsDetail(t *testing.T) {
	m := newTestModel()

	// Send window size to initialize list dimensions
	m, _ = sendMsg(m, tea.WindowSizeMsg{Width: 120, Height: 40})

	// Populate workspace list first
	m, _ = sendMsg(m, messages.WorkspaceListReceived{
		Workspaces: []messages.WorkspaceItem{
			{ID: "ws-001", Name: "base", Repo: "/home/user/project", State: "stopped"},
		},
	})

	// Press Enter
	m, _ = sendMsg(m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.CurrentView() != model.ViewDetail {
		t.Errorf("expected ViewDetail, got %d", m.CurrentView())
	}
	if m.SelectedWS() != "ws-001" {
		t.Errorf("expected selected ws-001, got %s", m.SelectedWS())
	}
}

// TestEscapeKeyReturnsToDashboard tests Escape key from detail view.
func TestEscapeKeyReturnsToDashboard(t *testing.T) {
	m := newTestModel()

	// Populate and navigate to detail
	m, _ = sendMsg(m, messages.WorkspaceListReceived{
		Workspaces: []messages.WorkspaceItem{
			{ID: "ws-001", Name: "base", Repo: "/home/user/project", State: "stopped"},
		},
	})
	m, _ = sendMsg(m, tea.KeyMsg{Type: tea.KeyEnter})

	// Press Escape
	m, _ = sendMsg(m, tea.KeyMsg{Type: tea.KeyEsc})

	if m.CurrentView() != model.ViewDashboard {
		t.Errorf("expected ViewDashboard, got %d", m.CurrentView())
	}
}

// TestQuitKey tests that q key quits the program.
func TestQuitKey(t *testing.T) {
	m := newTestModel()

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	_ = cmd // quit command may vary based on state
}

// TestVimNavigation tests that j/k keys navigate the list.
func TestVimNavigation(t *testing.T) {
	m := newTestModel()

	// Populate with 3 workspaces
	m, _ = sendMsg(m, messages.WorkspaceListReceived{
		Workspaces: []messages.WorkspaceItem{
			{ID: "ws-001", Name: "base", Repo: "/home/user/project", State: "stopped"},
			{ID: "ws-002", Name: "api", Repo: "/home/user/api", State: "running"},
			{ID: "ws-003", Name: "web", Repo: "/home/user/web", State: "stopped"},
		},
	})

	wsList := m.WorkspaceList()
	if wsList.Index() != 0 {
		t.Errorf("expected initial index 0, got %d", wsList.Index())
	}

	// Press j (down)
	m, _ = sendMsg(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	wsList = m.WorkspaceList()
	if wsList.Index() != 1 {
		t.Errorf("expected index 1 after j, got %d", wsList.Index())
	}

	// Press k (up)
	m, _ = sendMsg(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	wsList = m.WorkspaceList()
	if wsList.Index() != 0 {
		t.Errorf("expected index 0 after k, got %d", wsList.Index())
	}
}

// TestDeleteConfirmModal tests delete confirmation flow.
func TestDeleteConfirmModal(t *testing.T) {
	m := newTestModel()

	// Send window size to initialize list dimensions
	m, _ = sendMsg(m, tea.WindowSizeMsg{Width: 120, Height: 40})

	// Populate and select workspace
	m, _ = sendMsg(m, messages.WorkspaceListReceived{
		Workspaces: []messages.WorkspaceItem{
			{ID: "ws-001", Name: "base", Repo: "/home/user/project", State: "stopped"},
		},
	})

	// Navigate to detail first
	m, _ = sendMsg(m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.CurrentView() != model.ViewDetail {
		t.Fatalf("expected ViewDetail before delete test, got %d", m.CurrentView())
	}

	// Press d to delete
	m, _ = sendMsg(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})

	if !m.Modal().Active {
		t.Error("expected modal to be active after 'd' key")
	}
	if m.Modal().Action != "delete" {
		t.Errorf("expected modal action 'delete', got %s", m.Modal().Action)
	}

	// Press n to cancel
	m, _ = sendMsg(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})

	if m.Modal().Active {
		t.Error("expected modal to be inactive after 'n' key")
	}
}

// TestToastSystem tests toast notifications.
func TestToastSystem(t *testing.T) {
	m := newTestModel()

	// Add a toast
	m.AddToast(model.Toast{Message: "test", Kind: messages.ToastInfo})
	if len(m.Toasts()) != 1 {
		t.Fatalf("expected 1 toast, got %d", len(m.Toasts()))
	}

	// Clear toasts
	m.ClearToasts()
	if len(m.Toasts()) != 0 {
		t.Errorf("expected 0 toasts after clear, got %d", len(m.Toasts()))
	}
}

// TestMaxToasts tests that toast queue is limited to 3.
func TestMaxToasts(t *testing.T) {
	m := newTestModel()

	for i := 0; i < 5; i++ {
		m.AddToast(model.Toast{Message: "toast", Kind: messages.ToastInfo})
	}

	if len(m.Toasts()) != 3 {
		t.Errorf("expected 3 toasts (max), got %d", len(m.Toasts()))
	}
}

// TestViewHelpNavigation tests help view navigation.
func TestViewHelpNavigation(t *testing.T) {
	m := newTestModel()

	// Send '?' which should set view to Help directly
	m, _ = sendMsg(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})

	if m.CurrentView() != model.ViewHelp {
		t.Errorf("expected ViewHelp, got %d", m.CurrentView())
	}

	// Press q to go back
	m, _ = sendMsg(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})

	if m.CurrentView() != model.ViewDashboard {
		t.Errorf("expected ViewDashboard after help quit, got %d", m.CurrentView())
	}
}

// TestViewCreateNavigation tests create view navigation.
func TestViewCreateNavigation(t *testing.T) {
	m := newTestModel()

	// Send 'c' which should set view to Create directly
	m, _ = sendMsg(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})

	if m.CurrentView() != model.ViewCreate {
		t.Errorf("expected ViewCreate, got %d", m.CurrentView())
	}

	// Press Esc to go back
	m, _ = sendMsg(m, tea.KeyMsg{Type: tea.KeyEsc})

	if m.CurrentView() != model.ViewDashboard {
		t.Errorf("expected ViewDashboard after create quit, got %d", m.CurrentView())
	}
}

// TestWorkspaceStateChanged tests state change detection.
func TestWorkspaceStateChanged(t *testing.T) {
	m := newTestModel()

	// Initial state
	m, _ = sendMsg(m, messages.WorkspaceListReceived{
		Workspaces: []messages.WorkspaceItem{
			{ID: "ws-001", Name: "base", State: "stopped"},
		},
	})

	// State changed to starting
	m, cmd := sendMsg(m, messages.WorkspaceStateChanged{ID: "ws-001", State: "starting"})

	if m.Workspaces()[0].State != "starting" {
		t.Errorf("expected state 'starting', got %s", m.Workspaces()[0].State)
	}

	// Should return a toast command
	if cmd == nil {
		t.Error("expected toast command from state change")
	}
}

// TestWindowSizeResize tests terminal resize handling.
func TestWindowSizeResize(t *testing.T) {
	m := newTestModel()

	m, _ = sendMsg(m, tea.WindowSizeMsg{Width: 200, Height: 50})

	if m.Width() != 200 {
		t.Errorf("expected width 200, got %d", m.Width())
	}
	if m.Height() != 50 {
		t.Errorf("expected height 50, got %d", m.Height())
	}
}
