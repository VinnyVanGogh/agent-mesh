package conversation

import (
	"encoding/json"
	"strings"
	"time"
)

// AnthropicPayload represents the top-level Anthropic Messages API payload.
type AnthropicPayload struct {
	System   AnthropicSystem    `json:"system,omitempty"`
	Messages []AnthropicMessage `json:"messages"`
}

// AnthropicSystem handles system prompts as either a plain string or an array of blocks.
type AnthropicSystem string

func (s *AnthropicSystem) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		*s = AnthropicSystem(str)
		return nil
	}
	var blocks []AnthropicContentBlock
	if err := json.Unmarshal(data, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		*s = AnthropicSystem(strings.Join(parts, "\n\n"))
		return nil
	}
	return nil
}

func (s AnthropicSystem) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(s))
}

// AnthropicMessage represents a message turn in Anthropic API.
type AnthropicMessage struct {
	Role    string           `json:"role"` // "user" or "assistant"
	Content AnthropicContent `json:"content"`
}

// AnthropicContent handles content as either a single string or an array of blocks.
type AnthropicContent []AnthropicContentBlock

func (c *AnthropicContent) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		*c = []AnthropicContentBlock{{Type: "text", Text: str}}
		return nil
	}
	var blocks []AnthropicContentBlock
	if err := json.Unmarshal(data, &blocks); err == nil {
		*c = blocks
		return nil
	}
	return nil
}

func (c AnthropicContent) MarshalJSON() ([]byte, error) {
	if len(c) == 1 && c[0].Type == "text" && c[0].ID == "" && c[0].Name == "" && c[0].ToolUseID == "" {
		return json.Marshal(c[0].Text)
	}
	return json.Marshal([]AnthropicContentBlock(c))
}

// AnthropicContentBlock represents a single block within content.
type AnthropicContentBlock struct {
	Type      string                 `json:"type"` // "text", "tool_use", "tool_result"
	Text      string                 `json:"text,omitempty"`
	ID        string                 `json:"id,omitempty"`
	Name      string                 `json:"name,omitempty"`
	Input     map[string]interface{} `json:"input,omitempty"`
	ToolUseID string                 `json:"tool_use_id,omitempty"`
	Content   interface{}            `json:"content,omitempty"`
	IsError   bool                   `json:"is_error,omitempty"`
}

// FromAnthropic converts an Anthropic payload into a canonical Conversation.
func FromAnthropic(payload *AnthropicPayload) *Conversation {
	conv := &Conversation{
		ID:              newID(),
		SystemPrompt:    string(payload.System),
		ProviderHandles: make(map[string]*ProviderHandle),
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}

	for i, m := range payload.Messages {
		msg := Message{
			ID:          newID(),
			SequenceNum: i,
			CreatedAt:   conv.CreatedAt,
		}

		if m.Role == "assistant" {
			msg.Role = RoleAssistant
		} else {
			msg.Role = RoleUser
		}

		var textParts []string
		for _, b := range m.Content {
			switch b.Type {
			case "text":
				if b.Text != "" {
					textParts = append(textParts, b.Text)
				}
			case "tool_use":
				argsJSON, _ := json.Marshal(b.Input)
				if len(argsJSON) == 0 || string(argsJSON) == "null" {
					argsJSON = []byte("{}")
				}
				msg.ToolCalls = append(msg.ToolCalls, ToolCall{
					ID:        b.ID,
					Name:      b.Name,
					Arguments: string(argsJSON),
				})
			case "tool_result":
				resContent := ""
				if str, ok := b.Content.(string); ok {
					resContent = str
				} else if b.Content != nil {
					bs, _ := json.Marshal(b.Content)
					resContent = string(bs)
				}
				msg.ToolResults = append(msg.ToolResults, ToolResult{
					ToolCallID: b.ToolUseID,
					Content:    resContent,
					IsError:    b.IsError,
				})
			}
		}

		msg.Content = strings.Join(textParts, "\n\n")
		conv.Messages = append(conv.Messages, msg)
	}

	return conv
}

// ToAnthropic converts a canonical Conversation into an Anthropic payload.
func ToAnthropic(conv *Conversation) *AnthropicPayload {
	payload := &AnthropicPayload{
		System: AnthropicSystem(conv.SystemPrompt),
	}

	for _, msg := range conv.Messages {
		if msg.Role == RoleSystem {
			if payload.System == "" {
				payload.System = AnthropicSystem(msg.Content)
			}
			continue
		}

		var blocks []AnthropicContentBlock

		if msg.Content != "" {
			blocks = append(blocks, AnthropicContentBlock{
				Type: "text",
				Text: msg.Content,
			})
		}

		for _, tc := range msg.ToolCalls {
			var input map[string]interface{}
			if tc.Arguments != "" {
				_ = json.Unmarshal([]byte(tc.Arguments), &input)
			}
			if input == nil {
				input = make(map[string]interface{})
			}
			blocks = append(blocks, AnthropicContentBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Name,
				Input: input,
			})
		}

		for _, tr := range msg.ToolResults {
			blocks = append(blocks, AnthropicContentBlock{
				Type:      "tool_result",
				ToolUseID: tr.ToolCallID,
				Content:   tr.Content,
				IsError:   tr.IsError,
			})
		}

		if msg.Role == RoleTool && len(blocks) == 0 && msg.Content != "" {
			// Single tool result message without explicit ToolResults struct
			toolCallID := ""
			if tcID, ok := msg.Metadata["tool_call_id"].(string); ok {
				toolCallID = tcID
			}
			blocks = append(blocks, AnthropicContentBlock{
				Type:      "tool_result",
				ToolUseID: toolCallID,
				Content:   msg.Content,
			})
		}

		role := "user"
		if msg.Role == RoleAssistant {
			role = "assistant"
		}

		payload.Messages = append(payload.Messages, AnthropicMessage{
			Role:    role,
			Content: blocks,
		})
	}

	return payload
}
