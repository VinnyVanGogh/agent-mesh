package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestDispositionModel_DefaultSelection(t *testing.T) {
	m := InitialDispositionModel("todo")
	if m.Selected != "todo" || m.Cursor != 0 {
		t.Errorf("expected default todo at index 0, got %s at %d", m.Selected, m.Cursor)
	}

	mBacklog := InitialDispositionModel("backlog")
	if mBacklog.Selected != "backlog" || mBacklog.Cursor != 1 {
		t.Errorf("expected default backlog at index 1, got %s at %d", mBacklog.Selected, mBacklog.Cursor)
	}
}

func TestDispositionModel_KeyboardNavigation(t *testing.T) {
	m := InitialDispositionModel("todo")

	// Press down
	mDown, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = mDown.(DispositionModel)
	if m.Cursor != 1 {
		t.Errorf("expected cursor 1 after down, got %d", m.Cursor)
	}

	// Press enter
	mEnter, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mEnter.(DispositionModel)
	if m.Selected != "backlog" {
		t.Errorf("expected selected backlog after enter, got %s", m.Selected)
	}
	if cmd == nil {
		t.Errorf("expected tea.Quit on enter")
	}
}

func TestDispositionModel_Shortcuts(t *testing.T) {
	m := InitialDispositionModel("todo")

	// Press 'b' (shortcut for backlog)
	mB, cmd := m.Update(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune{'b'},
	})
	m = mB.(DispositionModel)
	if m.Selected != "backlog" {
		t.Errorf("expected selected backlog after 'b', got %s", m.Selected)
	}
	if cmd == nil {
		t.Errorf("expected tea.Quit on 'b'")
	}

	// Press '1' or 'a' (shortcut for todo)
	m2 := InitialDispositionModel("backlog")
	mA, cmd2 := m2.Update(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune{'a'},
	})
	m2 = mA.(DispositionModel)
	if m2.Selected != "todo" {
		t.Errorf("expected selected todo after 'a', got %s", m2.Selected)
	}
	if cmd2 == nil {
		t.Errorf("expected tea.Quit on 'a'")
	}
}

func TestDispositionModel_Cancel(t *testing.T) {
	m := InitialDispositionModel("todo")

	mEsc, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mEsc.(DispositionModel)
	if !m.Cancelled {
		t.Errorf("expected Cancelled = true after Esc")
	}
	if cmd == nil {
		t.Errorf("expected tea.Quit on Esc")
	}
}

func TestDispositionModel_View(t *testing.T) {
	m := InitialDispositionModel("todo")
	view := m.View()

	if !strings.Contains(view, "Disposition Prompter") {
		t.Errorf("expected header in view, got: %s", view)
	}
	if !strings.Contains(view, "Active Execution") {
		t.Errorf("expected active option in view, got: %s", view)
	}
	if !strings.Contains(view, "Backlog Parking") {
		t.Errorf("expected backlog option in view, got: %s", view)
	}
}
