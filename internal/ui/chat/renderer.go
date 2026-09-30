package chat

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

var (
	userHeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7dcfff"))

	assistantHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#bb9af7"))

	systemHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#e0af68"))

	errorHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#f7768e"))

	metaStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#565f89")).
			Faint(true)

	badgeStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#24283b")).
			Foreground(lipgloss.Color("#7aa2f7")).
			Padding(0, 1)

	streamingStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#73daca")).
			Italic(true)
)

// MarkdownRenderer encapsulates glamour markdown rendering and terminal styling.
type MarkdownRenderer struct {
	mu       sync.Mutex
	width    int
	renderer *glamour.TermRenderer
}

// NewMarkdownRenderer creates a new renderer with word-wrapping at width.
func NewMarkdownRenderer(width int) *MarkdownRenderer {
	r := &MarkdownRenderer{}
	r.SetWidth(width)
	return r
}

// SetWidth updates word-wrapping width and re-initializes the glamour renderer.
func (r *MarkdownRenderer) SetWidth(width int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if width <= 0 {
		width = 80
	}
	if r.width == width && r.renderer != nil {
		return
	}

	r.width = width
	tr, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
	)
	if err == nil {
		r.renderer = tr
	}
}

// Render renders raw markdown text into ANSI formatted text.
// If glamour fails, it returns the raw text trimmed.
func (r *MarkdownRenderer) Render(md string) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if strings.TrimSpace(md) == "" {
		return ""
	}

	if r.renderer != nil {
		rendered, err := r.renderer.Render(md)
		if err == nil {
			return strings.TrimRight(rendered, "\n")
		}
	}
	return strings.TrimRight(md, "\n")
}

// RenderMessage renders a complete ChatMessage with appropriate role banners.
func (r *MarkdownRenderer) RenderMessage(msg ChatMessage) string {
	ts := msg.Timestamp.Format("15:04:05")
	var header string

	switch msg.Role {
	case RoleUser:
		header = fmt.Sprintf("%s  %s",
			userHeaderStyle.Render("❯ You"),
			metaStyle.Render(ts),
		)
		body := r.Render(msg.Content)
		return fmt.Sprintf("%s\n%s\n", header, indent(body, "  "))

	case RoleAssistant:
		modelBadge := ""
		if msg.Model != "" {
			modelBadge = " " + badgeStyle.Render(msg.Model)
		}
		tokenInfo := ""
		if msg.TokenCount > 0 {
			tokenInfo = fmt.Sprintf(" (%d tokens)", msg.TokenCount)
		}
		header = fmt.Sprintf("%s%s  %s",
			assistantHeaderStyle.Render("❯ Assistant"),
			modelBadge,
			metaStyle.Render(ts+tokenInfo),
		)
		body := r.Render(msg.Content)
		return fmt.Sprintf("%s\n%s\n", header, indent(body, "  "))

	case RoleSystem:
		header = fmt.Sprintf("%s  %s",
			systemHeaderStyle.Render("⚙ System"),
			metaStyle.Render(ts),
		)
		body := r.Render(msg.Content)
		return fmt.Sprintf("%s\n%s\n", header, indent(body, "  "))

	case RoleError:
		header = fmt.Sprintf("%s  %s",
			errorHeaderStyle.Render("✖ Error"),
			metaStyle.Render(ts),
		)
		return fmt.Sprintf("%s\n%s\n", header, indent(msg.Content, "  "))

	default:
		return msg.Content
	}
}

// RenderStreamingAssistant formats the currently streaming assistant message with a spinner.
func (r *MarkdownRenderer) RenderStreamingAssistant(model string, content string, spinnerView string, startTime time.Time) string {
	ts := startTime.Format("15:04:05")
	modelBadge := ""
	if model != "" {
		modelBadge = " " + badgeStyle.Render(model)
	}

	header := fmt.Sprintf("%s%s  %s  %s",
		assistantHeaderStyle.Render("❯ Assistant"),
		modelBadge,
		metaStyle.Render(ts),
		streamingStyle.Render(spinnerView+" streaming..."),
	)

	body := r.Render(content)
	if body == "" {
		body = streamingStyle.Render("Thinking...")
	}
	return fmt.Sprintf("%s\n%s\n", header, indent(body, "  "))
}

func indent(text, prefix string) string {
	if text == "" {
		return prefix
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "\n")
}
