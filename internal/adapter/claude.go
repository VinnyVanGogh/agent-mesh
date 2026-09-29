package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ClaudeAdapter drives the Claude Code CLI in print mode
// (`claude --print --output-format stream-json --verbose`). Verified against 2.1.x.
type ClaudeAdapter struct{}

func (ClaudeAdapter) Provider() string          { return "claude" }
func (ClaudeAdapter) BinaryName() string        { return "claude" }
func (ClaudeAdapter) KnownMajorVersions() []int { return []int{2} }

func (ClaudeAdapter) BuildArgs(opts ParsedOptions) []string { return buildClaudeArgs(opts) }

func (a ClaudeAdapter) Execute(ctx context.Context, req ExecRequest) error {
	return execute(ctx, a, req)
}

type claudeUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	OutputTokensDetails      struct {
		ThinkingTokens int64 `json:"thinking_tokens"`
	} `json:"output_tokens_details"`
}

type claudeContent struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	Thinking  string `json:"thinking"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	ToolUseID string `json:"tool_use_id"`
	IsError   bool   `json:"is_error"`
}

type claudeEvent struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	Model     string `json:"model"`
	Message   struct {
		Model string `json:"model"`
		// Content is an array of blocks, or a plain string for replayed user prompts.
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	IsError bool         `json:"is_error"`
	Result  string       `json:"result"`
	Usage   *claudeUsage `json:"usage"`
}

// ParseStreamDelta normalizes one Claude stream-json line.
func (ClaudeAdapter) ParseStreamDelta(line []byte) ([]StreamDelta, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, nil
	}
	var ev claudeEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil, fmt.Errorf("claude stream: %w", err)
	}

	switch ev.Type {
	case "system":
		if ev.Subtype == "init" {
			return []StreamDelta{{Kind: DeltaInit, SessionID: ev.SessionID, Model: ev.Model}}, nil
		}
		return []StreamDelta{{Kind: DeltaOther, SessionID: ev.SessionID, Raw: "system:" + ev.Subtype}}, nil

	case "assistant", "user":
		var blocks []claudeContent
		if len(ev.Message.Content) == 0 || json.Unmarshal(ev.Message.Content, &blocks) != nil {
			return []StreamDelta{{Kind: DeltaOther, SessionID: ev.SessionID, Raw: ev.Type}}, nil
		}
		var out []StreamDelta
		for _, b := range blocks {
			d := StreamDelta{SessionID: ev.SessionID, Model: ev.Message.Model}
			switch b.Type {
			case "text":
				d.Kind, d.Text = DeltaText, b.Text
			case "thinking":
				d.Kind, d.Text = DeltaThinking, b.Thinking
			case "tool_use":
				d.Kind, d.ToolName, d.ToolID = DeltaToolUse, b.Name, b.ID
			case "tool_result":
				d.Kind, d.ToolID, d.IsError = DeltaToolResult, b.ToolUseID, b.IsError
			default:
				d.Kind, d.Raw = DeltaOther, ev.Type+":"+b.Type
			}
			out = append(out, d)
		}
		return out, nil

	case "result":
		d := StreamDelta{
			Kind:      DeltaResult,
			SessionID: ev.SessionID,
			Status:    ev.Subtype,
			Text:      ev.Result,
			IsError:   ev.IsError,
		}
		if ev.Usage != nil {
			d.Usage = &Usage{
				InputTokens:         ev.Usage.InputTokens,
				OutputTokens:        ev.Usage.OutputTokens,
				CacheReadTokens:     ev.Usage.CacheReadInputTokens,
				CacheCreationTokens: ev.Usage.CacheCreationInputTokens,
				ThinkingTokens:      ev.Usage.OutputTokensDetails.ThinkingTokens,
			}
		}
		return []StreamDelta{d}, nil
	}

	return []StreamDelta{{Kind: DeltaOther, SessionID: ev.SessionID, Raw: ev.Type}}, nil
}

func buildClaudeArgs(opts ParsedOptions) []string {
	args := []string{
		"--print",
		"--output-format", opts.OutputFormat,
		"--verbose",
		"--dangerously-skip-permissions",
	}
	if strings.Contains(opts.Model, "claude") || strings.Contains(opts.Model, "sonnet") || strings.Contains(opts.Model, "opus") {
		args = append(args, "--model", opts.Model)
	}
	if opts.ConversationID != "" {
		args = append(args, "--resume", opts.ConversationID)
	}
	for _, dir := range opts.AddDirs {
		args = append(args, "--add-dir", dir)
	}
	if opts.Prompt != "" {
		args = append(args, opts.Prompt)
	}
	return args
}
