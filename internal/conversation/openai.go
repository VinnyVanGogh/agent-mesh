package conversation

import (
	"time"
)

// OpenAIPayload represents the Chat Completions request format.
type OpenAIPayload struct {
	Messages []OpenAIMessage `json:"messages"`
}

// OpenAIMessage represents a message turn in OpenAI API.
type OpenAIMessage struct {
	Role       string           `json:"role"` // "system", "user", "assistant", "tool"
	Content    string           `json:"content"`
	Name       string           `json:"name,omitempty"`
	ToolCalls  []OpenAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

// OpenAIToolCall represents a tool call made by the assistant.
type OpenAIToolCall struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"` // "function"
	Function OpenAIFunction `json:"function"`
}

// OpenAIFunction represents function call arguments in OpenAI format.
type OpenAIFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // stringified JSON
}

// FromOpenAI converts an OpenAI payload into a canonical Conversation.
func FromOpenAI(payload *OpenAIPayload) *Conversation {
	conv := &Conversation{
		ID:              newID(),
		ProviderHandles: make(map[string]*ProviderHandle),
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}

	for i, m := range payload.Messages {
		if m.Role == "system" && conv.SystemPrompt == "" && len(conv.Messages) == 0 {
			conv.SystemPrompt = m.Content
			continue
		}

		msg := Message{
			ID:          newID(),
			SequenceNum: i,
			Content:     m.Content,
			CreatedAt:   conv.CreatedAt,
			Metadata:    make(map[string]interface{}),
		}

		switch m.Role {
		case "system":
			msg.Role = RoleSystem
		case "assistant":
			msg.Role = RoleAssistant
			for _, tc := range m.ToolCalls {
				msg.ToolCalls = append(msg.ToolCalls, ToolCall{
					ID:        tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				})
			}
		case "tool":
			msg.Role = RoleTool
			if m.ToolCallID != "" {
				msg.Metadata["tool_call_id"] = m.ToolCallID
				msg.ToolResults = append(msg.ToolResults, ToolResult{
					ToolCallID: m.ToolCallID,
					Name:       m.Name,
					Content:    m.Content,
				})
			}
			if m.Name != "" {
				msg.Metadata["name"] = m.Name
			}
		default:
			msg.Role = RoleUser
		}

		conv.Messages = append(conv.Messages, msg)
	}

	return conv
}

// ToOpenAI converts a canonical Conversation into an OpenAI payload.
func ToOpenAI(conv *Conversation) *OpenAIPayload {
	payload := &OpenAIPayload{}

	if conv.SystemPrompt != "" {
		payload.Messages = append(payload.Messages, OpenAIMessage{
			Role:    "system",
			Content: conv.SystemPrompt,
		})
	}

	for _, msg := range conv.Messages {
		if msg.Role == RoleSystem {
			payload.Messages = append(payload.Messages, OpenAIMessage{
				Role:    "system",
				Content: msg.Content,
			})
			continue
		}

		if msg.Role == RoleAssistant {
			var tcs []OpenAIToolCall
			for _, tc := range msg.ToolCalls {
				tcs = append(tcs, OpenAIToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: OpenAIFunction{
						Name:      tc.Name,
						Arguments: tc.Arguments,
					},
				})
			}
			payload.Messages = append(payload.Messages, OpenAIMessage{
				Role:      "assistant",
				Content:   msg.Content,
				ToolCalls: tcs,
			})
			continue
		}

		if msg.Role == RoleTool {
			if len(msg.ToolResults) > 0 {
				for _, tr := range msg.ToolResults {
					name := tr.Name
					if name == "" {
						if n, ok := msg.Metadata["name"].(string); ok {
							name = n
						}
					}
					payload.Messages = append(payload.Messages, OpenAIMessage{
						Role:       "tool",
						ToolCallID: tr.ToolCallID,
						Name:       name,
						Content:    tr.Content,
					})
				}
			} else {
				tcID := ""
				if id, ok := msg.Metadata["tool_call_id"].(string); ok {
					tcID = id
				}
				name := ""
				if n, ok := msg.Metadata["name"].(string); ok {
					name = n
				}
				payload.Messages = append(payload.Messages, OpenAIMessage{
					Role:       "tool",
					ToolCallID: tcID,
					Name:       name,
					Content:    msg.Content,
				})
			}
			continue
		}

		// User message: might contain ToolResults if imported from Anthropic or Gemini
		if len(msg.ToolResults) > 0 {
			if msg.Content != "" {
				payload.Messages = append(payload.Messages, OpenAIMessage{
					Role:    "user",
					Content: msg.Content,
				})
			}
			for _, tr := range msg.ToolResults {
				payload.Messages = append(payload.Messages, OpenAIMessage{
					Role:       "tool",
					ToolCallID: tr.ToolCallID,
					Name:       tr.Name,
					Content:    tr.Content,
				})
			}
			continue
		}

		payload.Messages = append(payload.Messages, OpenAIMessage{
			Role:    "user",
			Content: msg.Content,
		})
	}

	return payload
}
