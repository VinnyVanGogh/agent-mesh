package ui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/VinnyVanGogh/staypoint/internal/context"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	ErrInteractionCancelled = errors.New("interaction cancelled by user")
	ErrStaleConfirmation    = errors.New("cannot confirm stale revision")
)

// ConfirmationChoice represents user selection on confirmation card.
type ConfirmationChoice string

const (
	ConfirmationAccept ConfirmationChoice = "accept"
	ConfirmationReject ConfirmationChoice = "reject"
	ConfirmationCancel ConfirmationChoice = "cancel"
)

// ============================================================================
// 1. Confirmation Card Model (Bubble Tea)
// ============================================================================

// ConfirmationCardModel provides an interactive Bubble Tea card for request_confirmation.
type ConfirmationCardModel struct {
	Payload        context.RequestConfirmationPayload
	LatestRevision int
	IsStale        bool
	Choice         ConfirmationChoice
	Feedback       string
	Cancelled      bool
	Width          int
}

// InitialConfirmationCardModel initializes a ConfirmationCardModel.
func InitialConfirmationCardModel(payload context.RequestConfirmationPayload, latestRevision int, isStale bool) ConfirmationCardModel {
	return ConfirmationCardModel{
		Payload:        payload,
		LatestRevision: latestRevision,
		IsStale:        isStale,
		Width:          80,
	}
}

func (m ConfirmationCardModel) Init() tea.Cmd {
	return nil
}

func (m ConfirmationCardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch strings.ToLower(msg.String()) {
		case "y", "enter":
			if m.IsStale {
				// Keyboard cannot accept stale revision
				return m, nil
			}
			m.Choice = ConfirmationAccept
			return m, tea.Quit
		case "n":
			m.Choice = ConfirmationReject
			return m, tea.Quit
		case "q", "x", "esc", "ctrl+c":
			m.Cancelled = true
			m.Choice = ConfirmationCancel
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.Width = msg.Width
	}
	return m, nil
}

func (m ConfirmationCardModel) View() string {
	borderColor := lipgloss.Color("#7aa2f7")
	if m.IsStale {
		borderColor = lipgloss.Color("#f7768e")
	}

	cardBorder := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
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

	keyStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#73daca"))

	actionItemStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#a9b1d6"))

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s\n\n", titleStyle.Render("🔒 [StayPoint :: Plan & Document Confirmation Card]")))

	if m.Payload.Target.Key != "" {
		sb.WriteString(fmt.Sprintf("%s %s   %s v%d\n",
			metaKeyStyle.Render("Target Document:"), metaValStyle.Render(m.Payload.Target.Key),
			metaKeyStyle.Render("Target Revision:"), m.Payload.Target.RevisionId,
		))
	}

	if m.IsStale {
		staleAlert := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#f7768e")).
			Render(fmt.Sprintf("❌ STALE REVISION: Confirmation bound to revision v%d, but latest is v%d.\n   Approval is disabled until plan is refreshed.",
				m.Payload.Target.RevisionId, m.LatestRevision))
		sb.WriteString(fmt.Sprintf("\n%s\n", staleAlert))
	} else if m.Payload.Target.Key != "" {
		lockedBadge := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#9ece6a")).
			Render(fmt.Sprintf("✔ REVISION LOCKED: v%d (Matches latest document state)", m.Payload.Target.RevisionId))
		sb.WriteString(fmt.Sprintf("\n%s\n", lockedBadge))
	}

	promptBox := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(lipgloss.Color("#7aa2f7")).
		Foreground(lipgloss.Color("#c0caf5")).
		PaddingLeft(1).
		Render(fmt.Sprintf("Prompt:\n%s", m.Payload.Prompt))
	sb.WriteString(fmt.Sprintf("\n%s\n", promptBox))

	sb.WriteString("\n" + lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7aa2f7")).Render("── Keyboard Actions ─────────────────────────────────────") + "\n")
	if !m.IsStale {
		sb.WriteString(fmt.Sprintf("  %s %s\n", keyStyle.Render("[y/Enter]"), actionItemStyle.Render("Confirm and accept proposal")))
	} else {
		disabledStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#565f89"))
		sb.WriteString(fmt.Sprintf("  %s %s\n", disabledStyle.Render("[y/Enter]"), disabledStyle.Render("Confirm (Disabled: document revision is stale)")))
	}
	sb.WriteString(fmt.Sprintf("  %s %s\n", keyStyle.Render("[n]      "), actionItemStyle.Render("Reject proposal")))
	sb.WriteString(fmt.Sprintf("  %s %s\n", keyStyle.Render("[q/Esc]  "), actionItemStyle.Render("Cancel card without decision")))

	return cardBorder.Render(sb.String()) + "\n"
}

// RunConfirmationCard launches the Bubble Tea confirmation card.
func RunConfirmationCard(payload context.RequestConfirmationPayload, latestRevision int, isStale bool) (ConfirmationChoice, error) {
	p := tea.NewProgram(InitialConfirmationCardModel(payload, latestRevision, isStale))
	m, err := p.Run()
	if err != nil {
		return ConfirmationCancel, fmt.Errorf("failed to run confirmation card: %w", err)
	}

	model, ok := m.(ConfirmationCardModel)
	if !ok || model.Cancelled {
		return ConfirmationCancel, ErrInteractionCancelled
	}
	if model.Choice == ConfirmationAccept && model.IsStale {
		return ConfirmationCancel, ErrStaleConfirmation
	}
	return model.Choice, nil
}

// ============================================================================
// 2. Ask Questions Card Model (Bubble Tea)
// ============================================================================

// AskQuestionsCardModel provides an interactive Bubble Tea card for answering structured questions.
type AskQuestionsCardModel struct {
	Questions    []context.QuestionItem
	ActiveIndex  int
	OptionCursor int
	Selections   map[int]map[int]bool // question index -> option index -> isSelected
	CustomInputs map[int]string
	Submitted    bool
	Cancelled    bool
	Width        int
}

// InitialAskQuestionsCardModel initializes AskQuestionsCardModel.
func InitialAskQuestionsCardModel(payload context.AskUserQuestionsPayload) AskQuestionsCardModel {
	selections := make(map[int]map[int]bool)
	for i := range payload.Questions {
		selections[i] = make(map[int]bool)
	}

	return AskQuestionsCardModel{
		Questions:    payload.Questions,
		ActiveIndex:  0,
		OptionCursor: 0,
		Selections:   selections,
		CustomInputs: make(map[int]string),
		Width:        80,
	}
}

func (m AskQuestionsCardModel) Init() tea.Cmd {
	return nil
}

func (m AskQuestionsCardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if len(m.Questions) == 0 {
		return m, tea.Quit
	}

	currentQ := m.Questions[m.ActiveIndex]
	optCount := len(currentQ.Options)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch strings.ToLower(msg.String()) {
		case "up", "k":
			if m.OptionCursor > 0 {
				m.OptionCursor--
			} else {
				m.OptionCursor = optCount - 1
			}
		case "down", "j":
			if m.OptionCursor < optCount-1 {
				m.OptionCursor++
			} else {
				m.OptionCursor = 0
			}
		case " ":
			// Toggle selection
			if currentQ.IsMultiSelect {
				m.Selections[m.ActiveIndex][m.OptionCursor] = !m.Selections[m.ActiveIndex][m.OptionCursor]
			} else {
				// Single select: clear others and set this one
				m.Selections[m.ActiveIndex] = make(map[int]bool)
				m.Selections[m.ActiveIndex][m.OptionCursor] = true
			}
		case "tab", "n", "right":
			if m.ActiveIndex < len(m.Questions)-1 {
				m.ActiveIndex++
				m.OptionCursor = 0
			}
		case "shift+tab", "p", "left":
			if m.ActiveIndex > 0 {
				m.ActiveIndex--
				m.OptionCursor = 0
			}
		case "enter", "ctrl+s":
			// If on last question, submit; otherwise advance to next
			if m.ActiveIndex < len(m.Questions)-1 {
				m.ActiveIndex++
				m.OptionCursor = 0
			} else {
				m.Submitted = true
				return m, tea.Quit
			}
		case "q", "esc", "ctrl+c":
			m.Cancelled = true
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.Width = msg.Width
	}
	return m, nil
}

func (m AskQuestionsCardModel) View() string {
	if len(m.Questions) == 0 {
		return "No questions to display.\n"
	}

	cardBorder := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#7aa2f7")).
		Padding(1, 2).
		Width(76)

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#7dcfff"))

	qStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#e0af68"))

	cursorStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#73daca"))

	selectedStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#9ece6a"))

	normalStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#c0caf5"))

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#565f89"))

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s  %s\n\n",
		titleStyle.Render("❓ [StayPoint :: Structured Questions Card]"),
		dimStyle.Render(fmt.Sprintf("(Question %d of %d)", m.ActiveIndex+1, len(m.Questions))),
	))

	currentQ := m.Questions[m.ActiveIndex]
	sb.WriteString(qStyle.Render(fmt.Sprintf("Q: %s", currentQ.Question)) + "\n")
	if currentQ.IsMultiSelect {
		sb.WriteString(dimStyle.Render("   (Multi-select: Space toggles, Enter confirms)") + "\n\n")
	} else {
		sb.WriteString(dimStyle.Render("   (Single-select: Space selects, Enter confirms)") + "\n\n")
	}

	for i, opt := range currentQ.Options {
		isSelected := m.Selections[m.ActiveIndex][i]
		isCursor := i == m.OptionCursor

		var checkMark string
		if currentQ.IsMultiSelect {
			if isSelected {
				checkMark = selectedStyle.Render("[x]")
			} else {
				checkMark = normalStyle.Render("[ ]")
			}
		} else {
			if isSelected {
				checkMark = selectedStyle.Render("(•)")
			} else {
				checkMark = normalStyle.Render("( )")
			}
		}

		cursorPrefix := "  "
		if isCursor {
			cursorPrefix = cursorStyle.Render("▸ ")
		}

		label := normalStyle.Render(opt)
		if isSelected {
			label = selectedStyle.Render(opt)
		}

		sb.WriteString(fmt.Sprintf("%s%s %s\n", cursorPrefix, checkMark, label))
	}

	sb.WriteString("\n" + lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7aa2f7")).Render("── Keyboard Navigation ──────────────────────────────────") + "\n")
	sb.WriteString(fmt.Sprintf("  %s %s   %s %s\n",
		cursorStyle.Render("[↑/↓ or k/j]"), dimStyle.Render("Navigate options"),
		cursorStyle.Render("[Space]"), dimStyle.Render("Select / Toggle"),
	))
	sb.WriteString(fmt.Sprintf("  %s %s   %s %s\n",
		cursorStyle.Render("[Tab/n]"), dimStyle.Render("Next question"),
		cursorStyle.Render("[Shift+Tab/p]"), dimStyle.Render("Prev question"),
	))
	sb.WriteString(fmt.Sprintf("  %s %s   %s %s\n",
		cursorStyle.Render("[Enter]"), dimStyle.Render("Submit / Continue"),
		cursorStyle.Render("[q/Esc]"), dimStyle.Render("Cancel"),
	))

	return cardBorder.Render(sb.String()) + "\n"
}

// GetAnswers converts selections into context.AskQuestionsResponse.
func (m AskQuestionsCardModel) GetAnswers() context.AskQuestionsResponse {
	var resp context.AskQuestionsResponse
	for i, q := range m.Questions {
		var selected []string
		for optIdx := 0; optIdx < len(q.Options); optIdx++ {
			if m.Selections[i][optIdx] {
				selected = append(selected, q.Options[optIdx])
			}
		}
		resp.Answers = append(resp.Answers, context.QuestionAnswer{
			QuestionID:      q.ID,
			SelectedOptions: selected,
			CustomAnswer:    m.CustomInputs[i],
		})
	}
	return resp
}

// RunAskQuestionsCard runs the interactive questions modal.
func RunAskQuestionsCard(payload context.AskUserQuestionsPayload) (context.AskQuestionsResponse, error) {
	p := tea.NewProgram(InitialAskQuestionsCardModel(payload))
	m, err := p.Run()
	if err != nil {
		return context.AskQuestionsResponse{}, fmt.Errorf("failed to run questions card: %w", err)
	}

	model, ok := m.(AskQuestionsCardModel)
	if !ok || model.Cancelled {
		return context.AskQuestionsResponse{}, ErrInteractionCancelled
	}
	return model.GetAnswers(), nil
}

// ============================================================================
// 3. Suggest Tasks Card Model (Bubble Tea)
// ============================================================================

// SuggestTasksCardModel provides an interactive Bubble Tea card for choosing suggested tasks.
type SuggestTasksCardModel struct {
	Tasks        []context.SuggestedTaskItem
	Cursor       int
	Selected     map[int]bool
	Submitted    bool
	Cancelled    bool
	Width        int
}

// InitialSuggestTasksCardModel initializes SuggestTasksCardModel.
func InitialSuggestTasksCardModel(payload context.SuggestTasksPayload) SuggestTasksCardModel {
	selected := make(map[int]bool)
	// Default: all tasks selected initially
	for i := range payload.Tasks {
		selected[i] = true
	}

	return SuggestTasksCardModel{
		Tasks:    payload.Tasks,
		Cursor:   0,
		Selected: selected,
		Width:    80,
	}
}

func (m SuggestTasksCardModel) Init() tea.Cmd {
	return nil
}

func (m SuggestTasksCardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	taskCount := len(m.Tasks)
	if taskCount == 0 {
		return m, tea.Quit
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch strings.ToLower(msg.String()) {
		case "up", "k":
			if m.Cursor > 0 {
				m.Cursor--
			} else {
				m.Cursor = taskCount - 1
			}
		case "down", "j":
			if m.Cursor < taskCount-1 {
				m.Cursor++
			} else {
				m.Cursor = 0
			}
		case " ":
			m.Selected[m.Cursor] = !m.Selected[m.Cursor]
		case "a":
			// Select all
			for i := range m.Tasks {
				m.Selected[i] = true
			}
		case "n":
			// Select none
			for i := range m.Tasks {
				m.Selected[i] = false
			}
		case "enter", "ctrl+s":
			m.Submitted = true
			return m, tea.Quit
		case "q", "esc", "ctrl+c":
			m.Cancelled = true
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.Width = msg.Width
	}
	return m, nil
}

func (m SuggestTasksCardModel) View() string {
	if len(m.Tasks) == 0 {
		return "No suggested tasks.\n"
	}

	cardBorder := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#7aa2f7")).
		Padding(1, 2).
		Width(76)

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#7dcfff"))

	cursorStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#73daca"))

	selectedStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#9ece6a"))

	normalStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#c0caf5"))

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#565f89"))

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s\n\n", titleStyle.Render("📋 [StayPoint :: Suggested Tasks Dispatch Card]")))
	sb.WriteString(dimStyle.Render("Choose which proposed tasks to approve and schedule into the task graph:") + "\n\n")

	for i, t := range m.Tasks {
		isSel := m.Selected[i]
		isCursor := i == m.Cursor

		cursorPrefix := "  "
		if isCursor {
			cursorPrefix = cursorStyle.Render("▸ ")
		}

		checkMark := normalStyle.Render("[ ]")
		if isSel {
			checkMark = selectedStyle.Render("[x]")
		}

		titleText := normalStyle.Render(t.Title)
		if isSel {
			titleText = selectedStyle.Render(t.Title)
		}

		sb.WriteString(fmt.Sprintf("%s%s %s\n", cursorPrefix, checkMark, titleText))
		if t.Description != "" {
			sb.WriteString(fmt.Sprintf("      %s\n", dimStyle.Render(t.Description)))
		}
	}

	sb.WriteString("\n" + lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7aa2f7")).Render("── Keyboard Actions ─────────────────────────────────────") + "\n")
	sb.WriteString(fmt.Sprintf("  %s %s   %s %s\n",
		cursorStyle.Render("[↑/↓ or k/j]"), dimStyle.Render("Navigate tasks"),
		cursorStyle.Render("[Space]"), dimStyle.Render("Toggle task"),
	))
	sb.WriteString(fmt.Sprintf("  %s %s   %s %s\n",
		cursorStyle.Render("[a]"), dimStyle.Render("Select all"),
		cursorStyle.Render("[n]"), dimStyle.Render("Select none"),
	))
	sb.WriteString(fmt.Sprintf("  %s %s   %s %s\n",
		cursorStyle.Render("[Enter]"), dimStyle.Render("Approve selected tasks"),
		cursorStyle.Render("[q/Esc]"), dimStyle.Render("Cancel card"),
	))

	return cardBorder.Render(sb.String()) + "\n"
}

// GetAcceptedTasks returns the list of accepted tasks.
func (m SuggestTasksCardModel) GetAcceptedTasks() []context.SuggestedTaskItem {
	var accepted []context.SuggestedTaskItem
	for i, t := range m.Tasks {
		if m.Selected[i] {
			accepted = append(accepted, t)
		}
	}
	return accepted
}

// RunSuggestTasksCard runs the interactive suggested tasks card.
func RunSuggestTasksCard(payload context.SuggestTasksPayload) ([]context.SuggestedTaskItem, error) {
	p := tea.NewProgram(InitialSuggestTasksCardModel(payload))
	m, err := p.Run()
	if err != nil {
		return nil, fmt.Errorf("failed to run suggest tasks card: %w", err)
	}

	model, ok := m.(SuggestTasksCardModel)
	if !ok || model.Cancelled {
		return nil, ErrInteractionCancelled
	}
	return model.GetAcceptedTasks(), nil
}
