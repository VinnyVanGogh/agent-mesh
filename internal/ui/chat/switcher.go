package chat

import (
	"fmt"
	"strings"

	"github.com/VinnyVanGogh/staypoint/internal/adapter"
	"github.com/VinnyVanGogh/staypoint/internal/condenser"
)

// ProviderFamily maps a provider name to its canonical provider family.
func ProviderFamily(provider string) string {
	p := strings.ToLower(provider)
	switch {
	case p == "claude" || strings.Contains(p, "claude"):
		return "claude"
	case p == "gemini" || p == "agy" || strings.Contains(p, "gemini"):
		return "gemini"
	case p == "codex" || p == "openai" || strings.Contains(p, "codex"):
		return "codex"
	case p == "local" || p == "ollama" || p == "openai-compat":
		return "local"
	default:
		return p
	}
}

// ModelContextWindow returns the token context window size for a given model or provider.
func ModelContextWindow(model string) int {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "gemini") || strings.Contains(m, "flash") || strings.Contains(m, "pro") || strings.Contains(m, "agy"):
		return 1000000 // 1M tokens
	case strings.Contains(m, "claude") || strings.Contains(m, "sonnet") || strings.Contains(m, "opus") || strings.Contains(m, "haiku"):
		return 200000 // 200k tokens
	case strings.Contains(m, "codex") || strings.Contains(m, "o1") || strings.Contains(m, "o3") || strings.Contains(m, "gpt-4o"):
		return 128000 // 128k tokens
	case strings.Contains(m, "local") || strings.Contains(m, "ollama") || strings.Contains(m, "llama") || strings.Contains(m, "openai-compat"):
		return 8192 // 8k tokens
	default:
		return 128000
	}
}

// IsDownshift returns true if switching from fromModel to toModel downshifts to a smaller context window.
func IsDownshift(fromModel, toModel string) bool {
	if fromModel == "" || toModel == "" {
		return false
	}
	return ModelContextWindow(toModel) < ModelContextWindow(fromModel)
}

// CondenseMessagesForDownshift condenses previous conversation turns using internal/condenser
// so they fit compactly into the smaller context window of the target model.
func CondenseMessagesForDownshift(messages []ChatMessage, targetWindow int) []ChatMessage {
	if len(messages) == 0 {
		return nil
	}

	// Budget for conversation history in smaller context window:
	// For instance, for an 8k window, keep history turns bounded to leave room for response.
	perMessageMaxTokens := targetWindow / (len(messages) + 2)
	if perMessageMaxTokens < 200 {
		perMessageMaxTokens = 200
	}
	if perMessageMaxTokens > 1200 {
		perMessageMaxTokens = 1200
	}

	condensed := make([]ChatMessage, len(messages))
	for i, msg := range messages {
		condensed[i] = msg
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}

		res, err := condenser.Condense(content, condenser.CondenseOptions{
			MaxTokens:   perMessageMaxTokens,
			MaxLines:    50,
			ShowSavings: false,
		})
		if err == nil && res != nil && res.Condensed != "" {
			condensed[i].Content = res.Condensed
		}
	}
	return condensed
}

// FormatRehydratedPrompt combines previous conversation turns and the latest prompt
// into a structured prompt suitable for CLI-based providers.
func FormatRehydratedPrompt(messages []ChatMessage, prompt string) string {
	if len(messages) == 0 {
		return prompt
	}

	var sb strings.Builder
	sb.WriteString("[Conversation History]\n")
	for _, m := range messages {
		roleName := "User"
		if m.Role == RoleAssistant {
			roleName = "Assistant"
		} else if m.Role == RoleSystem {
			roleName = "System"
		}
		sb.WriteString(fmt.Sprintf("%s: %s\n\n", roleName, strings.TrimSpace(m.Content)))
	}
	sb.WriteString("[Current User Request]\n")
	sb.WriteString(prompt)
	return sb.String()
}

// BuildRehydratedTurn prepares rehydrated prompt and structured history.
func BuildRehydratedTurn(messages []ChatMessage, prompt string, downshift bool, targetWindow int) (string, []adapter.ChatMessageInput) {
	msgs := messages
	if downshift {
		msgs = CondenseMessagesForDownshift(messages, targetWindow)
	}

	var history []adapter.ChatMessageInput
	for _, m := range msgs {
		role := string(m.Role)
		if role == string(RoleAssistant) {
			role = "assistant"
		} else if role == string(RoleUser) {
			role = "user"
		} else {
			role = "system"
		}
		history = append(history, adapter.ChatMessageInput{
			Role:    role,
			Content: m.Content,
		})
	}

	formattedPrompt := FormatRehydratedPrompt(msgs, prompt)
	return formattedPrompt, history
}
