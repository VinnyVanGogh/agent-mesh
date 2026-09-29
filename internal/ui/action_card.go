package ui

import (
	"fmt"
	"strings"

	"github.com/VinnyVanGogh/staypoint/internal/ai"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ActionType represents the choice selected on the 1-key action card.
type ActionType string

const (
	ActionDispatch          ActionType = "dispatch"
	ActionDispatchBacklog   ActionType = "backlog"
	ActionDispatchActive    ActionType = "active"
	ActionPromptDisposition ActionType = "prompt_disposition"
	ActionClarify           ActionType = "clarify"
	ActionEdit              ActionType = "edit"
	ActionCancel            ActionType = "cancel"
)

// ActionCardModel provides the pre-dispatch 1-key action card.
type ActionCardModel struct {
	Task         ai.InferredTask
	AssigneeName string
	AssigneeID   string
	Status       string
	Choice       ActionType
	Cancelled    bool
	Width        int
}

// InitialActionCardModel creates a new ActionCardModel initialized with task data.
func InitialActionCardModel(task ai.InferredTask, assigneeName, assigneeID string) ActionCardModel {
	status := task.Status
	if status == "" {
		status = "todo"
	}

	return ActionCardModel{
		Task:         task,
		AssigneeName: assigneeName,
		AssigneeID:   assigneeID,
		Status:       status,
		Width:        80,
	}
}

func (m ActionCardModel) Init() tea.Cmd {
	return nil
}

func (m ActionCardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch strings.ToLower(msg.String()) {
		case "d", "enter":
			m.Choice = ActionDispatch
			return m, tea.Quit
		case "b":
			m.Status = "backlog"
			m.Choice = ActionDispatchBacklog
			return m, tea.Quit
		case "a", "t":
			m.Status = "todo"
			m.Choice = ActionDispatchActive
			return m, tea.Quit
		case "p":
			m.Choice = ActionPromptDisposition
			return m, tea.Quit
		case "c":
			m.Choice = ActionClarify
			return m, tea.Quit
		case "e":
			m.Choice = ActionEdit
			return m, tea.Quit
		case "q", "x", "esc", "ctrl+c":
			m.Cancelled = true
			m.Choice = ActionCancel
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.Width = msg.Width
	}
	return m, nil
}

func (m ActionCardModel) View() string {
	cardBorder := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#7aa2f7")).
		Padding(1, 2).
		Width(76)

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#7dcfff"))

	metaKeyStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#bb9af7"))

	metaValStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#c0caf5"))

	var statusBadge string
	if strings.ToLower(m.Status) == "backlog" {
		statusBadge = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#e0af68")).
			Render("● BACKLOG (Parked - 0 agent wakes, unassigned)")
	} else {
		targetAssignee := m.AssigneeName
		if targetAssignee == "" {
			targetAssignee = m.Task.AssigneeRole
		}
		statusBadge = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#9ece6a")).
			Render(fmt.Sprintf("● ACTIVE / TODO (Dispatched to: %s)", targetAssignee))
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s\n\n", titleStyle.Render(m.Task.Title)))
	sb.WriteString(fmt.Sprintf("%s %s   %s %s\n",
		metaKeyStyle.Render("Org:"), metaValStyle.Render(m.Task.Organization),
		metaKeyStyle.Render("Project:"), metaValStyle.Render(m.Task.Project),
	))
	sb.WriteString(fmt.Sprintf("%s %s   %s %s\n",
		metaKeyStyle.Render("Priority:"), metaValStyle.Render(strings.ToUpper(m.Task.Priority)),
		metaKeyStyle.Render("Role:"), metaValStyle.Render(func() string {
			if strings.ToLower(m.Status) == "backlog" {
				return "Unassigned (Backlog)"
			}
			return m.Task.AssigneeRole
		}()),
	))
	if len(m.Task.Labels) > 0 {
		sb.WriteString(fmt.Sprintf("%s %s\n",
			metaKeyStyle.Render("Labels:"), metaValStyle.Render(strings.Join(m.Task.Labels, ", ")),
		))
	}
	sb.WriteString(fmt.Sprintf("\n%s %s\n", metaKeyStyle.Render("Disposition:"), statusBadge))

	if m.Task.AskClarification != "" {
		clarificationBox := lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(lipgloss.Color("#e0af68")).
			Foreground(lipgloss.Color("#e0af68")).
			PaddingLeft(1).
			Render(fmt.Sprintf("⚠️  Clarification Requested:\n%s", m.Task.AskClarification))
		sb.WriteString(fmt.Sprintf("\n%s\n", clarificationBox))
	}

	// 1-Key Action Card options
	keyStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#73daca"))

	actionItemStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#a9b1d6"))

	sb.WriteString("\n" + lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7aa2f7")).Render("── 1-Key Dispatch Options ────────────────────────────────") + "\n")
	sb.WriteString(fmt.Sprintf("  %s %s\n", keyStyle.Render("[d/Enter]"), actionItemStyle.Render("Dispatch with current disposition")))
	sb.WriteString(fmt.Sprintf("  %s %s\n", keyStyle.Render("[b]      "), actionItemStyle.Render("Park to Backlog (zero agent wakes)")))
	sb.WriteString(fmt.Sprintf("  %s %s\n", keyStyle.Render("[a]      "), actionItemStyle.Render("Active Execution (assign and start now)")))
	sb.WriteString(fmt.Sprintf("  %s %s\n", keyStyle.Render("[p]      "), actionItemStyle.Render("Prompt Disposition (interactive selector)")))
	if m.Task.AskClarification != "" {
		clarifyHighlight := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#e0af68")).Render("[c]       Answer Clarification (Recommended)")
		sb.WriteString(fmt.Sprintf("  %s\n", clarifyHighlight))
	} else {
		sb.WriteString(fmt.Sprintf("  %s %s\n", keyStyle.Render("[c]      "), actionItemStyle.Render("Add Clarification / Extra context")))
	}
	sb.WriteString(fmt.Sprintf("  %s %s\n", keyStyle.Render("[e]      "), actionItemStyle.Render("Edit Description in TUI")))
	sb.WriteString(fmt.Sprintf("  %s %s\n", keyStyle.Render("[q/Esc]  "), actionItemStyle.Render("Cancel task creation")))

	return cardBorder.Render(sb.String()) + "\n"
}

// RunActionCard launches the 1-key pre-dispatch action card.
func RunActionCard(task ai.InferredTask, assigneeName, assigneeID string) (ActionType, string, error) {
	p := tea.NewProgram(InitialActionCardModel(task, assigneeName, assigneeID))
	m, err := p.Run()
	if err != nil {
		return ActionCancel, "", fmt.Errorf("failed to run action card: %w", err)
	}

	model, ok := m.(ActionCardModel)
	if !ok || model.Cancelled {
		return ActionCancel, model.Status, nil
	}
	return model.Choice, model.Status, nil
}
