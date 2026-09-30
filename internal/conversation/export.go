package conversation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ExportJSON exports a conversation as indented JSON.
func ExportJSON(conv *Conversation) ([]byte, error) {
	return json.MarshalIndent(conv, "", "  ")
}

// ExportMarkdown exports a conversation as clean, readable Markdown.
func ExportMarkdown(conv *Conversation) string {
	var buf bytes.Buffer

	title := conv.Title
	if title == "" {
		title = "Untitled Session"
	}
	buf.WriteString(fmt.Sprintf("# %s\n\n", title))
	buf.WriteString(fmt.Sprintf("*ID: `%s` | Created: %s | Updated: %s*\n\n",
		conv.ID,
		conv.CreatedAt.Format(time.RFC3339),
		conv.UpdatedAt.Format(time.RFC3339),
	))

	if len(conv.ProviderHandles) > 0 {
		buf.WriteString("**Provider Handles:**\n")
		for prov, h := range conv.ProviderHandles {
			modelInfo := ""
			if h.Model != "" {
				modelInfo = fmt.Sprintf(" (%s)", h.Model)
			}
			buf.WriteString(fmt.Sprintf("- `%s`: `%s`%s\n", prov, h.Handle, modelInfo))
		}
		buf.WriteString("\n")
	}

	if conv.SystemPrompt != "" {
		buf.WriteString("## System Prompt\n\n")
		buf.WriteString(fmt.Sprintf("> %s\n\n", strings.ReplaceAll(conv.SystemPrompt, "\n", "\n> ")))
	}

	buf.WriteString("---\n\n")

	for _, msg := range conv.Messages {
		roleHeader := strings.ToUpper(string(msg.Role)[:1]) + string(msg.Role)[1:]
		buf.WriteString(fmt.Sprintf("### %s\n\n", roleHeader))

		if strings.TrimSpace(msg.Content) != "" {
			buf.WriteString(msg.Content)
			buf.WriteString("\n\n")
		}

		for _, tc := range msg.ToolCalls {
			buf.WriteString(fmt.Sprintf("> **Tool Call:** `%s` (ID: `%s`)\n>\n", tc.Name, tc.ID))
			argsFormatted := tc.Arguments
			var prettyArgs bytes.Buffer
			if err := json.Indent(&prettyArgs, []byte(tc.Arguments), "> ", "  "); err == nil {
				argsFormatted = prettyArgs.String()
			}
			buf.WriteString("> ```json\n")
			buf.WriteString(fmt.Sprintf("> %s\n", argsFormatted))
			buf.WriteString("> ```\n\n")
		}

		for _, tr := range msg.ToolResults {
			title := "Tool Result"
			if tr.IsError {
				title = "Tool Error"
			}
			name := tr.Name
			if name == "" {
				name = tr.ToolCallID
			}
			buf.WriteString(fmt.Sprintf("> **%s:** `%s`\n>\n", title, name))
			buf.WriteString("> ```\n")
			for _, line := range strings.Split(tr.Content, "\n") {
				buf.WriteString(fmt.Sprintf("> %s\n", line))
			}
			buf.WriteString("> ```\n\n")
		}

		buf.WriteString("---\n\n")
	}

	return buf.String()
}
