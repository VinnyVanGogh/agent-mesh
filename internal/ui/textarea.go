package ui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var ErrCancelled = errors.New("task creation cancelled by user")

type TextareaModel struct {
	Textarea  textarea.Model
	Err       error
	Submitted bool
	Cancelled bool
	Value     string
	Width     int
	Height    int
}

func InitialTextareaModel() TextareaModel {
	ta := textarea.New()
	ta.Placeholder = "Describe task in natural language or dictate using speech-to-text...\n(Ctrl+S or Ctrl+D to submit, Ctrl+C or Esc to cancel)"
	ta.Focus()
	ta.CharLimit = 10000
	ta.SetWidth(80)
	ta.SetHeight(10)
	ta.ShowLineNumbers = false

	return TextareaModel{
		Textarea: ta,
		Width:    80,
		Height:   10,
	}
}

func (m TextareaModel) Init() tea.Cmd {
	return textarea.Blink
}

func (m TextareaModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.Cancelled = true
			return m, tea.Quit
		case tea.KeyCtrlS, tea.KeyCtrlD:
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
		targetHeight := msg.Height - 8
		if targetHeight > 18 {
			targetHeight = 18
		} else if targetHeight < 6 {
			targetHeight = 6
		}
		m.Textarea.SetWidth(targetWidth)
		m.Textarea.SetHeight(targetHeight)

	case error:
		m.Err = msg
		return m, nil
	}

	m.Textarea, cmd = m.Textarea.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m TextareaModel) View() string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#7dcfff")).
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

	header := titleStyle.Render("[StayPoint Dynamic Task Generator :: TUI Input Box]")
	content := boxStyle.Render(m.Textarea.View())
	help := helpStyle.Render(fmt.Sprintf("%s Submit   %s Cancel",
		shortcutStyle.Render("Ctrl+S / Ctrl+D:"),
		shortcutStyle.Render("Esc / Ctrl+C:"),
	))

	return fmt.Sprintf("%s\n%s\n%s\n", header, content, help)
}

// RunTextareaModal launches the interactive Bubble Tea full-screen/modal text editor.
func RunTextareaModal() (string, error) {
	p := tea.NewProgram(InitialTextareaModel())
	m, err := p.Run()
	if err != nil {
		return "", fmt.Errorf("failed to run TUI: %w", err)
	}

	model, ok := m.(TextareaModel)
	if !ok || model.Cancelled {
		return "", ErrCancelled
	}
	if !model.Submitted || strings.TrimSpace(model.Value) == "" {
		return "", ErrCancelled
	}

	return model.Value, nil
}
