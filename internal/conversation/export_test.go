package conversation

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestExport_JSON(t *testing.T) {
	conv := &Conversation{
		ID:           "sess_export_json",
		Title:        "JSON Export Test",
		RepoPath:     "/workspace/agent-mesh",
		SystemPrompt: "System instruction goes here.",
		Messages: []Message{
			{
				ID:          "m1",
				SequenceNum: 0,
				Role:        RoleUser,
				Content:     "Hello world",
				CreatedAt:   time.Now().UTC(),
			},
			{
				ID:          "m2",
				SequenceNum: 1,
				Role:        RoleAssistant,
				Content:     "Hello there!",
				CreatedAt:   time.Now().UTC(),
			},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	data, err := ExportJSON(conv)
	if err != nil {
		t.Fatalf("ExportJSON failed: %v", err)
	}

	var roundTrip Conversation
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("failed to unmarshal exported JSON: %v", err)
	}

	if roundTrip.ID != conv.ID || roundTrip.Title != conv.Title || len(roundTrip.Messages) != 2 {
		t.Errorf("mismatch after JSON round trip: %+v", roundTrip)
	}
}

func TestExport_Markdown(t *testing.T) {
	conv := &Conversation{
		ID:           "sess_export_md",
		Title:        "Markdown Export Test",
		RepoPath:     "/workspace/agent-mesh",
		SystemPrompt: "Act as an expert Go engineer.",
		Messages: []Message{
			{
				Role:    RoleUser,
				Content: "Check build status",
			},
			{
				Role:    RoleAssistant,
				Content: "Running command now.",
				ToolCalls: []ToolCall{
					{
						ID:        "call_cmd_1",
						Name:      "run_command",
						Arguments: `{"command":"go build ./..."}`,
					},
				},
			},
			{
				Role: RoleTool,
				ToolResults: []ToolResult{
					{
						ToolCallID: "call_cmd_1",
						Name:       "run_command",
						Content:    "Build succeeded (exit 0)",
						IsError:    false,
					},
				},
			},
			{
				Role:    RoleAssistant,
				Content: "Build is clean and passing.",
			},
		},
		ProviderHandles: map[string]*ProviderHandle{
			"anthropic": {
				Provider: "anthropic",
				Handle:   "claude_h1",
				Model:    "claude-3-5-sonnet",
			},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	md := ExportMarkdown(conv)

	expectedSnippets := []string{
		"# Markdown Export Test",
		"ID: `sess_export_md`",
		"## System Prompt",
		"Act as an expert Go engineer.",
		"`anthropic`: `claude_h1` (claude-3-5-sonnet)",
		"### User",
		"Check build status",
		"### Assistant",
		"Running command now.",
		"**Tool Call:** `run_command` (ID: `call_cmd_1`)",
		"**Tool Result:** `run_command`",
		"Build succeeded (exit 0)",
		"Build is clean and passing.",
	}

	for _, snip := range expectedSnippets {
		if !strings.Contains(md, snip) {
			t.Errorf("expected markdown to contain %q, but was missing.\nMarkdown:\n%s", snip, md)
		}
	}
}
