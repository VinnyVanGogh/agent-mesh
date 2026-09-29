package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTextareaModel_UpdateAndSubmit(t *testing.T) {
	m := InitialTextareaModel()

	// Simulate speech-to-text dictation with unescaped quotes, backticks, brackets
	sttInput := `Please implement "fast" task creation with ` + "`bubbletea`" + ` and [brackets]!`
	for _, r := range sttInput {
		mModel, _ := m.Update(tea.KeyMsg{
			Type:  tea.KeyRunes,
			Runes: []rune{r},
		})
		m = mModel.(TextareaModel)
	}

	if !strings.Contains(m.Textarea.Value(), sttInput) {
		t.Fatalf("expected textarea to contain dictated input %q, got %q", sttInput, m.Textarea.Value())
	}

	// Submit via Ctrl+S
	submitModel, cmd := m.Update(tea.KeyMsg{
		Type: tea.KeyCtrlS,
	})
	m = submitModel.(TextareaModel)

	if !m.Submitted {
		t.Errorf("expected m.Submitted to be true after Ctrl+S")
	}
	if m.Value != sttInput {
		t.Errorf("expected m.Value to be %q, got %q", sttInput, m.Value)
	}
	if cmd == nil {
		t.Errorf("expected tea.Quit command on submit")
	}
}

func TestTextareaModel_Cancel(t *testing.T) {
	m := InitialTextareaModel()

	cancelModel, cmd := m.Update(tea.KeyMsg{
		Type: tea.KeyCtrlC,
	})
	m = cancelModel.(TextareaModel)

	if !m.Cancelled {
		t.Errorf("expected m.Cancelled to be true after Ctrl+C")
	}
	if cmd == nil {
		t.Errorf("expected tea.Quit command on cancel")
	}

	// Also test Esc
	m2 := InitialTextareaModel()
	escModel, _ := m2.Update(tea.KeyMsg{
		Type: tea.KeyEsc,
	})
	m2 = escModel.(TextareaModel)
	if !m2.Cancelled {
		t.Errorf("expected m2.Cancelled to be true after Esc")
	}
}

func TestTextareaModel_WindowResize(t *testing.T) {
	m := InitialTextareaModel()

	resizeModel, _ := m.Update(tea.WindowSizeMsg{
		Width:  120,
		Height: 40,
	})
	m = resizeModel.(TextareaModel)

	if m.Width != 120 || m.Height != 40 {
		t.Errorf("expected model dimensions to update, got %dx%d", m.Width, m.Height)
	}

	view := m.View()
	if !strings.Contains(view, "[StayPoint Dynamic Task Generator") {
		t.Errorf("expected view to contain header title, got: %s", view)
	}
}
