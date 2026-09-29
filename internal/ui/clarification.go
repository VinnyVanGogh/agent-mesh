package ui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	ErrClarificationCancelled = errors.New("clarification cancelled by user")
	ErrClarificationSkipped   = errors.New("clarification skipped by user")
)

// ClarificationModel provides an interactive Bubble Tea modal for answering AI clarification prompts.
type ClarificationModel struct {
	Question  string
	Textarea  textarea.Model
	Err       error
	Submitted bool
	Cancelled bool
	Skipped   bool
	Value     string
	Width     int
	Height    int
}

// InitialClarificationModel initializes the clarification modal model.
func InitialClarificationModel(question string) ClarificationModel {
	ta := textarea.New()
	ta.Placeholder = "Enter clarification details, constraints, or architectural choices...\n(Ctrl+S or Enter to submit, Esc to skip, Ctrl+C to cancel)"
	ta.Focus()
	ta.CharLimit = 4000
	ta.SetWidth(78)
	ta.SetHeight(6)
	ta.ShowLineNumbers = false

	return ClarificationModel{
		Question: question,
		Textarea: ta,
		Width:    80,
		Height:   14,
	}
}

func (m ClarificationModel) Init() tea.Cmd {
	return textarea.Blink
}

func (m ClarificationModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			m.Cancelled = true
			return m, tea.Quit
		case tea.KeyEsc:
			m.Skipped = true
			return m, tea.Quit
		case tea.KeyCtrlS:
			val := strings.TrimSpace(m.Textarea.Value())
			m.Submitted = true
			m.Value = val
			return m, tea.Quit
		case tea.KeyEnter:
			val := strings.TrimSpace(m.Textarea.Value())
			if val != "" {
				m.Submitted = true
				m.Value = val
				return m, tea.Quit
			}
		default:
			if !m.Textarea.Focused() {
				cmd = m.Textarea.Focus()
				cmds = append(cmds, cmd)
			}
		}

	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height
		targetWidth := msg.Width - 6
		if targetWidth > 96 {
			targetWidth = 96
		} else if targetWidth < 40 {
			targetWidth = 40
		}
		m.Textarea.SetWidth(targetWidth)

	case error:
		m.Err = msg
		return m, nil
	}

	m.Textarea, cmd = m.Textarea.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m ClarificationModel) View() string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#e0af68")).
		MarginBottom(1)

	qHeaderStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#e0af68"))

	qBodyStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#c0caf5"))

	questionBoxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#e0af68")).
		Padding(0, 1).
		MarginBottom(1)

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#7aa2f7")).
		Padding(0, 1)

	helpStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#565f89")).
		MarginTop(1)

	shortcutStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#73daca"))

	header := titleStyle.Render("❓ [StayPoint :: Interactive Clarification Modal]")
	questionContent := fmt.Sprintf("%s\n%s", qHeaderStyle.Render("AI Ambiguity / Clarification Request:"), qBodyStyle.Render(m.Question))
	questionBox := questionBoxStyle.Render(questionContent)
	content := boxStyle.Render(m.Textarea.View())
	help := helpStyle.Render(fmt.Sprintf("%s Submit   %s Skip   %s Cancel",
		shortcutStyle.Render("Enter / Ctrl+S:"),
		shortcutStyle.Render("Esc:"),
		shortcutStyle.Render("Ctrl+C:"),
	))

	return fmt.Sprintf("%s\n%s\n%s\n%s\n", header, questionBox, content, help)
}

// RunClarificationModal executes the Bubble Tea modal for inputting task clarification.
func RunClarificationModal(question string) (string, error) {
	p := tea.NewProgram(InitialClarificationModel(question))
	m, err := p.Run()
	if err != nil {
		return "", fmt.Errorf("failed to run clarification modal: %w", err)
	}

	model, ok := m.(ClarificationModel)
	if !ok || model.Cancelled {
		return "", ErrClarificationCancelled
	}
	if model.Skipped {
		return "", ErrClarificationSkipped
	}
	return model.Value, nil
}
