package conversation

import (
	"fmt"
	"strings"
)

// FlattenForNonToolModels converts a Conversation with structured tool calls and
// results into a plain conversational history (only system, user, and assistant roles),
// embedding tool calls and tool results as formatted text blocks.
func FlattenForNonToolModels(conv *Conversation) *Conversation {
	flattened := &Conversation{
		ID:              conv.ID,
		Title:           conv.Title,
		RepoPath:        conv.RepoPath,
		SystemPrompt:    conv.SystemPrompt,
		ProviderHandles: conv.ProviderHandles,
		CreatedAt:       conv.CreatedAt,
		UpdatedAt:       conv.UpdatedAt,
		Metadata:        conv.Metadata,
	}

	seq := 0
	for _, msg := range conv.Messages {
		switch msg.Role {
		case RoleSystem:
			flatMsg := msg
			flatMsg.SequenceNum = seq
			seq++
			flattened.Messages = append(flattened.Messages, flatMsg)

		case RoleAssistant:
			var parts []string
			if strings.TrimSpace(msg.Content) != "" {
				parts = append(parts, strings.TrimSpace(msg.Content))
			}
			for _, tc := range msg.ToolCalls {
				tcBlock := fmt.Sprintf("[Tool Call: %s]\nArguments: %s", tc.Name, tc.Arguments)
				parts = append(parts, tcBlock)
			}
			flatMsg := msg
			flatMsg.SequenceNum = seq
			flatMsg.Content = strings.Join(parts, "\n\n")
			flatMsg.ToolCalls = nil
			flatMsg.ToolResults = nil
			seq++
			flattened.Messages = append(flattened.Messages, flatMsg)

		case RoleTool:
			var parts []string
			if len(msg.ToolResults) > 0 {
				for _, tr := range msg.ToolResults {
					tag := "Tool Result"
					if tr.IsError {
						tag = "Tool Error"
					}
					nameOrID := tr.Name
					if nameOrID == "" {
						nameOrID = tr.ToolCallID
					}
					parts = append(parts, fmt.Sprintf("[%s: %s]\n%s", tag, nameOrID, tr.Content))
				}
			} else if strings.TrimSpace(msg.Content) != "" {
				nameOrID := ""
				if n, ok := msg.Metadata["name"].(string); ok && n != "" {
					nameOrID = n
				} else if id, ok := msg.Metadata["tool_call_id"].(string); ok && id != "" {
					nameOrID = id
				}
				if nameOrID != "" {
					parts = append(parts, fmt.Sprintf("[Tool Result: %s]\n%s", nameOrID, strings.TrimSpace(msg.Content)))
				} else {
					parts = append(parts, fmt.Sprintf("[Tool Result]\n%s", strings.TrimSpace(msg.Content)))
				}
			}
			flatMsg := msg
			flatMsg.Role = RoleUser
			flatMsg.SequenceNum = seq
			flatMsg.Content = strings.Join(parts, "\n\n")
			flatMsg.ToolCalls = nil
			flatMsg.ToolResults = nil
			seq++
			flattened.Messages = append(flattened.Messages, flatMsg)

		case RoleUser:
			var parts []string
			if strings.TrimSpace(msg.Content) != "" {
				parts = append(parts, strings.TrimSpace(msg.Content))
			}
			for _, tr := range msg.ToolResults {
				tag := "Tool Result"
				if tr.IsError {
					tag = "Tool Error"
				}
				nameOrID := tr.Name
				if nameOrID == "" {
					nameOrID = tr.ToolCallID
				}
				parts = append(parts, fmt.Sprintf("[%s: %s]\n%s", tag, nameOrID, tr.Content))
			}
			flatMsg := msg
			flatMsg.SequenceNum = seq
			flatMsg.Content = strings.Join(parts, "\n\n")
			flatMsg.ToolCalls = nil
			flatMsg.ToolResults = nil
			seq++
			flattened.Messages = append(flattened.Messages, flatMsg)
		}
	}

	return flattened
}
