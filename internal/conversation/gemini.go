package conversation

import (
	"encoding/json"
	"strings"
	"time"
)

// GeminiPayload represents the Gemini GenerateContent request structure.
type GeminiPayload struct {
	SystemInstruction *GeminiContent  `json:"systemInstruction,omitempty"`
	Contents          []GeminiContent `json:"contents"`
}

// GeminiContent represents a turn in Gemini conversation.
type GeminiContent struct {
	Role  string       `json:"role,omitempty"` // "user" or "model"
	Parts []GeminiPart `json:"parts"`
}

// GeminiPart represents a single content part in Gemini turn.
type GeminiPart struct {
	Text             string                  `json:"text,omitempty"`
	FunctionCall     *GeminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *GeminiFunctionResponse `json:"functionResponse,omitempty"`
}

// GeminiFunctionCall represents a model function call.
type GeminiFunctionCall struct {
	ID   string                 `json:"id,omitempty"`
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args,omitempty"`
}

// GeminiFunctionResponse represents the execution response for a function call.
type GeminiFunctionResponse struct {
	ID       string                 `json:"id,omitempty"`
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
}

// FromGemini converts a Gemini payload into a canonical Conversation.
func FromGemini(payload *GeminiPayload) *Conversation {
	conv := &Conversation{
		ID:              newID(),
		ProviderHandles: make(map[string]*ProviderHandle),
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}

	if payload.SystemInstruction != nil {
		var sysParts []string
		for _, p := range payload.SystemInstruction.Parts {
			if p.Text != "" {
				sysParts = append(sysParts, p.Text)
			}
		}
		conv.SystemPrompt = strings.Join(sysParts, "\n\n")
	}

	for i, c := range payload.Contents {
		msg := Message{
			ID:          newID(),
			SequenceNum: i,
			CreatedAt:   conv.CreatedAt,
		}

		if c.Role == "model" {
			msg.Role = RoleAssistant
		} else {
			msg.Role = RoleUser
		}

		var textParts []string
		for _, p := range c.Parts {
			if p.Text != "" {
				textParts = append(textParts, p.Text)
			}
			if p.FunctionCall != nil {
				argsJSON, _ := json.Marshal(p.FunctionCall.Args)
				if len(argsJSON) == 0 || string(argsJSON) == "null" {
					argsJSON = []byte("{}")
				}
				msg.ToolCalls = append(msg.ToolCalls, ToolCall{
					ID:        p.FunctionCall.ID,
					Name:      p.FunctionCall.Name,
					Arguments: string(argsJSON),
				})
			}
			if p.FunctionResponse != nil {
				contentStr := ""
				isErr := false
				if resVal, ok := p.FunctionResponse.Response["result"]; ok {
					if s, ok := resVal.(string); ok {
						contentStr = s
					} else {
						bs, _ := json.Marshal(resVal)
						contentStr = string(bs)
					}
				} else if errVal, ok := p.FunctionResponse.Response["error"]; ok {
					isErr = true
					if s, ok := errVal.(string); ok {
						contentStr = s
					} else {
						bs, _ := json.Marshal(errVal)
						contentStr = string(bs)
					}
				} else {
					bs, _ := json.Marshal(p.FunctionResponse.Response)
					contentStr = string(bs)
				}

				msg.ToolResults = append(msg.ToolResults, ToolResult{
					ToolCallID: p.FunctionResponse.ID,
					Name:       p.FunctionResponse.Name,
					Content:    contentStr,
					IsError:    isErr,
				})
			}
		}

		msg.Content = strings.Join(textParts, "\n\n")
		conv.Messages = append(conv.Messages, msg)
	}

	return conv
}

// ToGemini converts a canonical Conversation into a Gemini payload.
func ToGemini(conv *Conversation) *GeminiPayload {
	payload := &GeminiPayload{}

	if conv.SystemPrompt != "" {
		payload.SystemInstruction = &GeminiContent{
			Parts: []GeminiPart{{Text: conv.SystemPrompt}},
		}
	}

	for _, msg := range conv.Messages {
		if msg.Role == RoleSystem {
			if payload.SystemInstruction == nil {
				payload.SystemInstruction = &GeminiContent{
					Parts: []GeminiPart{{Text: msg.Content}},
				}
			}
			continue
		}

		var parts []GeminiPart

		if msg.Content != "" {
			parts = append(parts, GeminiPart{Text: msg.Content})
		}

		for _, tc := range msg.ToolCalls {
			var args map[string]interface{}
			if tc.Arguments != "" {
				_ = json.Unmarshal([]byte(tc.Arguments), &args)
			}
			if args == nil {
				args = make(map[string]interface{})
			}
			parts = append(parts, GeminiPart{
				FunctionCall: &GeminiFunctionCall{
					ID:   tc.ID,
					Name: tc.Name,
					Args: args,
				},
			})
		}

		for _, tr := range msg.ToolResults {
			name := tr.Name
			if name == "" {
				// Find matching tool call name if available
				for _, m := range conv.Messages {
					for _, tc := range m.ToolCalls {
						if tc.ID == tr.ToolCallID {
							name = tc.Name
							break
						}
					}
				}
			}
			if name == "" {
				name = "tool_response"
			}

			var respMap map[string]interface{}
			if err := json.Unmarshal([]byte(tr.Content), &respMap); err != nil {
				key := "result"
				if tr.IsError {
					key = "error"
				}
				respMap = map[string]interface{}{key: tr.Content}
			}

			parts = append(parts, GeminiPart{
				FunctionResponse: &GeminiFunctionResponse{
					ID:       tr.ToolCallID,
					Name:     name,
					Response: respMap,
				},
			})
		}

		if msg.Role == RoleTool && len(parts) == 0 && msg.Content != "" {
			name := "tool_response"
			if n, ok := msg.Metadata["name"].(string); ok && n != "" {
				name = n
			}
			tcID := ""
			if id, ok := msg.Metadata["tool_call_id"].(string); ok {
				tcID = id
			}
			var respMap map[string]interface{}
			if err := json.Unmarshal([]byte(msg.Content), &respMap); err != nil {
				respMap = map[string]interface{}{"result": msg.Content}
			}
			parts = append(parts, GeminiPart{
				FunctionResponse: &GeminiFunctionResponse{
					ID:       tcID,
					Name:     name,
					Response: respMap,
				},
			})
		}

		role := "user"
		if msg.Role == RoleAssistant {
			role = "model"
		}

		payload.Contents = append(payload.Contents, GeminiContent{
			Role:  role,
			Parts: parts,
		})
	}

	return payload
}
