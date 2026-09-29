package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// DispositionOption represents a selectable task disposition state.
type DispositionOption struct {
	Status      string
	Title       string
	Description string
	Shortcut    string
}

// DispositionModel is a Bubble Tea model for selecting task disposition.
type DispositionModel struct {
	Options   []DispositionOption
	Cursor    int
	Selected  string
	Cancelled bool
	Width     int
}

// InitialDispositionModel constructs the disposition prompter model.
func InitialDispositionModel(currentStatus string) DispositionModel {
	options := []DispositionOption{
		{
			Status:      "todo",
			Title:       "Active Execution (todo)",
			Description: "Immediate assignment and dispatch to agent. High priority / urgent tasks.",
			Shortcut:    "1 / a",
		},
		{
			Status:      "backlog",
			Title:       "Backlog Parking (backlog)",
			Description: "Park task without assigning agents (0 compute / token usage). Ideas and future tasks.",
			Shortcut:    "2 / b",
		},
	}

	initialCursor := 0
	if strings.ToLower(currentStatus) == "backlog" {
		initialCursor = 1
	}

	return DispositionModel{
		Options:  options,
		Cursor:   initialCursor,
		Selected: options[initialCursor].Status,
		Width:    80,
	}
}

func (m DispositionModel) Init() tea.Cmd {
	return nil
}

func (m DispositionModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.Cancelled = true
			return m, tea.Quit
		case "up", "k":
			if m.Cursor > 0 {
				m.Cursor--
			}
		case "down", "j":
			if m.Cursor < len(m.Options)-1 {
				m.Cursor++
			}
		case "1", "a":
			m.Cursor = 0
			m.Selected = m.Options[0].Status
			return m, tea.Quit
		case "2", "b":
			m.Cursor = 1
			m.Selected = m.Options[1].Status
			return m, tea.Quit
		case "enter", " ":
			m.Selected = m.Options[m.Cursor].Status
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.Width = msg.Width
	}
	return m, nil
}

func (m DispositionModel) View() string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#7dcfff")).
		MarginBottom(1)

	header := titleStyle.Render("⚡ [StayPoint :: Disposition Prompter]")
	subtitle := lipgloss.NewStyle().Foreground(lipgloss.Color("#a9b1d6")).Render("Select execution disposition before dispatch:\n")

	var sb strings.Builder
	sb.WriteString(header + "\n")
	sb.WriteString(subtitle + "\n")

	for i, opt := range m.Options {
		cursor := "  "
		itemStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#c0caf5"))
		descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#565f89"))

		if m.Cursor == i {
			cursor = "❯ "
			itemStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#73daca"))
			descStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#9ece6a"))
		}

		shortcutBadge := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#bb9af7")).
			Render(fmt.Sprintf("[%s]", opt.Shortcut))

		sb.WriteString(fmt.Sprintf("%s%s %s\n", cursor, shortcutBadge, itemStyle.Render(opt.Title)))
		sb.WriteString(fmt.Sprintf("    %s\n\n", descStyle.Render(opt.Description)))
	}

	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#565f89"))
	sb.WriteString(helpStyle.Render("↑/↓ / j/k: Navigate   Enter / 1 / 2: Select   Esc: Cancel\n"))

	return sb.String()
}

// RunDispositionPrompter launches the interactive disposition prompter.
func RunDispositionPrompter(currentStatus string) (string, error) {
	p := tea.NewProgram(InitialDispositionModel(currentStatus))
	m, err := p.Run()
	if err != nil {
		return "", fmt.Errorf("failed to run disposition prompter: %w", err)
	}

	model, ok := m.(DispositionModel)
	if !ok || model.Cancelled {
		return currentStatus, nil // maintain current status if cancelled
	}
	return model.Selected, nil
}
