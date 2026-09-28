package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestClarificationModel_Submit(t *testing.T) {
	question := "Should the authentication use OAuth2 PKCE or Personal Access Tokens?"
	m := InitialClarificationModel(question)

	answer := "Please use OAuth2 PKCE with local callback server."
	for _, r := range answer {
		mModel, _ := m.Update(tea.KeyMsg{
			Type:  tea.KeyRunes,
			Runes: []rune{r},
		})
		m = mModel.(ClarificationModel)
	}

	if m.Textarea.Value() != answer {
		t.Fatalf("expected textarea to contain %q, got %q", answer, m.Textarea.Value())
	}

	submitModel, cmd := m.Update(tea.KeyMsg{
		Type: tea.KeyCtrlS,
	})
	m = submitModel.(ClarificationModel)

	if !m.Submitted {
		t.Errorf("expected m.Submitted to be true after Ctrl+S")
	}
	if m.Value != answer {
		t.Errorf("expected m.Value to be %q, got %q", answer, m.Value)
	}
	if cmd == nil {
		t.Errorf("expected tea.Quit command on submit")
	}
}

func TestClarificationModel_Skip(t *testing.T) {
	m := InitialClarificationModel("Any architectural preferences?")

	skipModel, cmd := m.Update(tea.KeyMsg{
		Type: tea.KeyEsc,
	})
	m = skipModel.(ClarificationModel)

	if !m.Skipped {
		t.Errorf("expected m.Skipped to be true after Esc")
	}
	if cmd == nil {
		t.Errorf("expected tea.Quit command on skip")
	}
}

func TestClarificationModel_Cancel(t *testing.T) {
	m := InitialClarificationModel("Any questions?")

	cancelModel, cmd := m.Update(tea.KeyMsg{
		Type: tea.KeyCtrlC,
	})
	m = cancelModel.(ClarificationModel)

	if !m.Cancelled {
		t.Errorf("expected m.Cancelled to be true after Ctrl+C")
	}
	if cmd == nil {
		t.Errorf("expected tea.Quit command on cancel")
	}
}

func TestClarificationModel_View(t *testing.T) {
	question := "Specify database migration strategy."
	m := InitialClarificationModel(question)

	view := m.View()
	if !strings.Contains(view, "Clarification Modal") {
		t.Errorf("expected view to contain header, got: %s", view)
	}
	if !strings.Contains(view, question) {
		t.Errorf("expected view to contain question text, got: %s", view)
	}
}
