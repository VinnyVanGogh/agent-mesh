package ui

import (
	"strings"
	"testing"

	"github.com/VinnyVanGogh/staypoint/internal/ai"
	tea "github.com/charmbracelet/bubbletea"
)

func sampleTestTask() ai.InferredTask {
	return ai.InferredTask{
		Organization:     "StayPoint",
		Project:          "StayPoint Core Engine",
		Title:            "Feature: Add Distributed Quota Sync",
		Description:      "## Objectives\nSync quota across nodes.",
		Priority:         "high",
		Labels:           []string{"quota", "fleet"},
		AssigneeRole:     "Chief of Staff",
		Status:           "todo",
		AskClarification: "Should the sync interval be 5s or 15s?",
	}
}

func TestActionCardModel_DispatchDefault(t *testing.T) {
	task := sampleTestTask()
	m := InitialActionCardModel(task, "Chief of Staff", "agent-cos")

	// Press 'd'
	mD, cmd := m.Update(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune{'d'},
	})
	m = mD.(ActionCardModel)

	if m.Choice != ActionDispatch {
		t.Errorf("expected choice ActionDispatch, got %s", m.Choice)
	}
	if cmd == nil {
		t.Errorf("expected tea.Quit command on 'd'")
	}
}

func TestActionCardModel_1KeyBacklog(t *testing.T) {
	task := sampleTestTask()
	m := InitialActionCardModel(task, "Chief of Staff", "agent-cos")

	// Press 'b' (1-key park to backlog)
	mB, cmd := m.Update(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune{'b'},
	})
	m = mB.(ActionCardModel)

	if m.Choice != ActionDispatchBacklog {
		t.Errorf("expected choice ActionDispatchBacklog, got %s", m.Choice)
	}
	if m.Status != "backlog" {
		t.Errorf("expected status 'backlog', got %s", m.Status)
	}
	if cmd == nil {
		t.Errorf("expected tea.Quit command on 'b'")
	}
}

func TestActionCardModel_1KeyActive(t *testing.T) {
	task := sampleTestTask()
	task.Status = "backlog"
	m := InitialActionCardModel(task, "Chief of Staff", "agent-cos")

	// Press 'a' (1-key active execution)
	mA, cmd := m.Update(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune{'a'},
	})
	m = mA.(ActionCardModel)

	if m.Choice != ActionDispatchActive {
		t.Errorf("expected choice ActionDispatchActive, got %s", m.Choice)
	}
	if m.Status != "todo" {
		t.Errorf("expected status 'todo', got %s", m.Status)
	}
	if cmd == nil {
		t.Errorf("expected tea.Quit command on 'a'")
	}
}

func TestActionCardModel_1KeyClarify(t *testing.T) {
	task := sampleTestTask()
	m := InitialActionCardModel(task, "Chief of Staff", "agent-cos")

	// Press 'c'
	mC, cmd := m.Update(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune{'c'},
	})
	m = mC.(ActionCardModel)

	if m.Choice != ActionClarify {
		t.Errorf("expected choice ActionClarify, got %s", m.Choice)
	}
	if cmd == nil {
		t.Errorf("expected tea.Quit command on 'c'")
	}
}

func TestActionCardModel_1KeyPromptDisposition(t *testing.T) {
	task := sampleTestTask()
	m := InitialActionCardModel(task, "Chief of Staff", "agent-cos")

	// Press 'p'
	mP, cmd := m.Update(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune{'p'},
	})
	m = mP.(ActionCardModel)

	if m.Choice != ActionPromptDisposition {
		t.Errorf("expected choice ActionPromptDisposition, got %s", m.Choice)
	}
	if cmd == nil {
		t.Errorf("expected tea.Quit on 'p'")
	}
}

func TestActionCardModel_1KeyCancel(t *testing.T) {
	task := sampleTestTask()
	m := InitialActionCardModel(task, "Chief of Staff", "agent-cos")

	mQ, cmd := m.Update(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune{'q'},
	})
	m = mQ.(ActionCardModel)

	if !m.Cancelled || m.Choice != ActionCancel {
		t.Errorf("expected cancelled after 'q'")
	}
	if cmd == nil {
		t.Errorf("expected tea.Quit on 'q'")
	}
}

func TestActionCardModel_ViewWithClarification(t *testing.T) {
	task := sampleTestTask()
	m := InitialActionCardModel(task, "Chief of Staff", "agent-cos")
	view := m.View()

	if !strings.Contains(view, "Feature: Add Distributed Quota Sync") {
		t.Errorf("expected title in view, got: %s", view)
	}
	if !strings.Contains(view, "Clarification Requested") {
		t.Errorf("expected clarification box in view, got: %s", view)
	}
	if !strings.Contains(view, "Should the sync interval be 5s or 15s?") {
		t.Errorf("expected question in view, got: %s", view)
	}
	if !strings.Contains(view, "1-Key Dispatch Options") {
		t.Errorf("expected options section in view, got: %s", view)
	}
}
