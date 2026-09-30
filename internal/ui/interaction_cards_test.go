package ui

import (
	"strings"
	"testing"

	"github.com/VinnyVanGogh/staypoint/internal/context"
	tea "github.com/charmbracelet/bubbletea"
)

func TestConfirmationCard_KeyboardControls(t *testing.T) {
	payload := context.RequestConfirmationPayload{
		Prompt: "Approve deploy to production?",
		Target: context.ConfirmationTarget{
			Type:       "issue_document",
			Key:        "plan",
			RevisionId: 2,
		},
	}

	// 1. Not stale: 'y' confirms
	m := InitialConfirmationCardModel(payload, 2, false)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(ConfirmationCardModel)
	if m.Choice != ConfirmationAccept {
		t.Fatalf("expected accept on 'y', got %s", m.Choice)
	}
	if cmd == nil {
		t.Fatalf("expected quit command")
	}

	// 2. Stale: 'y' is blocked!
	mStale := InitialConfirmationCardModel(payload, 3, true)
	updatedStale, cmdStale := mStale.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	mStale = updatedStale.(ConfirmationCardModel)
	if mStale.Choice == ConfirmationAccept {
		t.Fatalf("stale confirmation must not be accepted with 'y'")
	}
	if cmdStale != nil {
		t.Fatalf("expected no quit command on blocked accept")
	}

	// Stale: Enter is also blocked
	updatedStale, cmdStale = mStale.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mStale = updatedStale.(ConfirmationCardModel)
	if mStale.Choice == ConfirmationAccept {
		t.Fatalf("stale confirmation must not be accepted with Enter")
	}
	if cmdStale != nil {
		t.Fatalf("expected no quit command on blocked Enter")
	}

	// 3. 'n' rejects
	mReject := InitialConfirmationCardModel(payload, 2, false)
	updated, _ = mReject.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	mReject = updated.(ConfirmationCardModel)
	if mReject.Choice != ConfirmationReject {
		t.Fatalf("expected reject on 'n', got %s", mReject.Choice)
	}

	// 4. 'q' cancels
	mCancel := InitialConfirmationCardModel(payload, 2, false)
	updated, _ = mCancel.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	mCancel = updated.(ConfirmationCardModel)
	if !mCancel.Cancelled || mCancel.Choice != ConfirmationCancel {
		t.Fatalf("expected cancelled on 'q'")
	}

	// View rendering contains key info
	view := m.View()
	if !strings.Contains(view, "Plan & Document Confirmation Card") {
		t.Fatalf("expected title in view")
	}
	if !strings.Contains(view, "Target Revision") {
		t.Fatalf("expected revision in view")
	}

	staleView := mStale.View()
	if !strings.Contains(staleView, "STALE REVISION") {
		t.Fatalf("expected STALE REVISION warning in stale view")
	}
}

func TestAskQuestionsCard_KeyboardControls(t *testing.T) {
	payload := context.AskUserQuestionsPayload{
		Questions: []context.QuestionItem{
			{
				ID:            "q1",
				Question:      "Choose runtime architecture",
				Options:       []string{"Monolith", "Microservices", "Event-Driven"},
				IsMultiSelect: false,
			},
			{
				ID:            "q2",
				Question:      "Select target platforms",
				Options:       []string{"macOS", "Linux", "Windows"},
				IsMultiSelect: true,
			},
		},
	}

	m := InitialAskQuestionsCardModel(payload)

	// Single select on Q1: move down to "Microservices" and press space
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(AskQuestionsCardModel)
	if m.OptionCursor != 1 {
		t.Fatalf("expected cursor at 1, got %d", m.OptionCursor)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = updated.(AskQuestionsCardModel)
	if !m.Selections[0][1] {
		t.Fatalf("expected option 1 selected on Q1")
	}

	// Advance to Q2 via Tab
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(AskQuestionsCardModel)
	if m.ActiveIndex != 1 {
		t.Fatalf("expected active question 1, got %d", m.ActiveIndex)
	}

	// Multi-select on Q2: select option 0 (macOS) and option 1 (Linux)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace}) // select 0
	m = updated.(AskQuestionsCardModel)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown}) // move to 1
	m = updated.(AskQuestionsCardModel)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace}) // select 1
	m = updated.(AskQuestionsCardModel)

	if !m.Selections[1][0] || !m.Selections[1][1] {
		t.Fatalf("expected options 0 and 1 selected on Q2")
	}

	// Submit via Enter
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(AskQuestionsCardModel)
	if !m.Submitted {
		t.Fatalf("expected model to be submitted")
	}
	if cmd == nil {
		t.Fatalf("expected quit command on submit")
	}

	answers := m.GetAnswers()
	if len(answers.Answers) != 2 {
		t.Fatalf("expected 2 answers, got %d", len(answers.Answers))
	}
	if len(answers.Answers[0].SelectedOptions) != 1 || answers.Answers[0].SelectedOptions[0] != "Microservices" {
		t.Fatalf("unexpected answer for Q1: %+v", answers.Answers[0])
	}
	if len(answers.Answers[1].SelectedOptions) != 2 {
		t.Fatalf("unexpected answer for Q2: %+v", answers.Answers[1])
	}

	// View rendering
	view := m.View()
	if !strings.Contains(view, "Structured Questions Card") {
		t.Fatalf("expected title in view")
	}
}

func TestSuggestTasksCard_KeyboardControls(t *testing.T) {
	payload := context.SuggestTasksPayload{
		Tasks: []context.SuggestedTaskItem{
			{ID: "t1", Title: "Task One", Description: "Desc 1"},
			{ID: "t2", Title: "Task Two", Description: "Desc 2"},
			{ID: "t3", Title: "Task Three", Description: "Desc 3"},
		},
	}

	m := InitialSuggestTasksCardModel(payload)

	// All tasks initially selected
	if len(m.GetAcceptedTasks()) != 3 {
		t.Fatalf("expected all 3 tasks initially selected")
	}

	// Press 'n' to select none
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = updated.(SuggestTasksCardModel)
	if len(m.GetAcceptedTasks()) != 0 {
		t.Fatalf("expected 0 tasks after 'n'")
	}

	// Press space on cursor 0 to toggle Task 1 back on
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = updated.(SuggestTasksCardModel)
	if len(m.GetAcceptedTasks()) != 1 || m.GetAcceptedTasks()[0].ID != "t1" {
		t.Fatalf("expected only task 1 selected")
	}

	// Press 'a' to select all again
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(SuggestTasksCardModel)
	if len(m.GetAcceptedTasks()) != 3 {
		t.Fatalf("expected all 3 tasks after 'a'")
	}

	// Submit via Enter
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(SuggestTasksCardModel)
	if !m.Submitted {
		t.Fatalf("expected model to be submitted")
	}
	if cmd == nil {
		t.Fatalf("expected quit command on submit")
	}

	// View rendering
	view := m.View()
	if !strings.Contains(view, "Suggested Tasks Dispatch Card") {
		t.Fatalf("expected title in view")
	}
}
