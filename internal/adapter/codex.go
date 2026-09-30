package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// CodexAdapter drives the OpenAI Codex CLI in quiet mode
// (`codex -q --dangerously-auto-approve-everything`). Verified against codex 0.1.x.
type CodexAdapter struct{}

func (CodexAdapter) Provider() string          { return "codex" }
func (CodexAdapter) BinaryName() string        { return "codex" }
func (CodexAdapter) KnownMajorVersions() []int { return []int{0} }

func (CodexAdapter) BuildArgs(opts ParsedOptions) []string {
	return buildCodexArgs(opts)
}

func (a CodexAdapter) Execute(ctx context.Context, req ExecRequest) error {
	return execute(ctx, a, req)
}

type codexContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Refusal  string `json:"refusal,omitempty"`
	Filename string `json:"filename,omitempty"`
}

type codexMetadata struct {
	ExitCode        int     `json:"exit_code"`
	DurationSeconds float64 `json:"duration_seconds"`
}

type codexItem struct {
	ID         string         `json:"id,omitempty"`
	Type       string         `json:"type"`
	Role       string         `json:"role,omitempty"`
	Status     string         `json:"status,omitempty"`
	Name       string         `json:"name,omitempty"`
	CallID     string         `json:"call_id,omitempty"`
	Arguments  string         `json:"arguments,omitempty"`
	Output     string         `json:"output,omitempty"`
	Metadata   *codexMetadata `json:"metadata,omitempty"`
	Content    []codexContent `json:"content,omitempty"`
	DurationMs int            `json:"duration_ms,omitempty"`
	Usage      *Usage         `json:"usage,omitempty"`
}

// ParseStreamDelta normalizes one codex stream line.
func (CodexAdapter) ParseStreamDelta(line []byte) ([]StreamDelta, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, nil
	}
	var item codexItem
	if err := json.Unmarshal(line, &item); err != nil {
		return nil, fmt.Errorf("codex stream: %w", err)
	}

	switch item.Type {
	case "reasoning":
		return []StreamDelta{{
			Kind:      DeltaThinking,
			SessionID: item.ID,
			Raw:       "reasoning",
		}}, nil

	case "function_call":
		return []StreamDelta{{
			Kind:      DeltaToolUse,
			SessionID: item.ID,
			ToolName:  item.Name,
			ToolID:    item.CallID,
			Status:    item.Status,
		}}, nil

	case "function_call_output":
		isErr := false
		if item.Metadata != nil && item.Metadata.ExitCode != 0 {
			isErr = true
		}
		return []StreamDelta{{
			Kind:    DeltaToolResult,
			ToolID:  item.CallID,
			Text:    item.Output,
			IsError: isErr,
		}}, nil

	case "message":
		switch item.Role {
		case "user":
			return []StreamDelta{{
				Kind:      DeltaOther,
				SessionID: item.ID,
				Raw:       "message:user",
			}}, nil
		case "assistant":
			var out []StreamDelta
			var textBuf strings.Builder
			for _, c := range item.Content {
				switch c.Type {
				case "output_text":
					textBuf.WriteString(c.Text)
					out = append(out, StreamDelta{
						Kind:      DeltaText,
						SessionID: item.ID,
						Text:      c.Text,
					})
				case "refusal":
					out = append(out, StreamDelta{
						Kind:      DeltaError,
						SessionID: item.ID,
						IsError:   true,
						Error:     c.Refusal,
					})
				}
			}
			if item.Status == "completed" {
				res := StreamDelta{
					Kind:      DeltaResult,
					SessionID: item.ID,
					Status:    "completed",
					Text:      textBuf.String(),
					Usage:     item.Usage,
				}
				out = append(out, res)
			}
			return out, nil
		case "system":
			var textBuf strings.Builder
			for _, c := range item.Content {
				textBuf.WriteString(c.Text)
			}
			return []StreamDelta{{
				Kind:      DeltaError,
				SessionID: item.ID,
				IsError:   true,
				Error:     textBuf.String(),
			}}, nil
		default:
			return []StreamDelta{{
				Kind:      DeltaOther,
				SessionID: item.ID,
				Raw:       "message:" + item.Role,
			}}, nil
		}

	case "result":
		return []StreamDelta{{
			Kind:      DeltaResult,
			SessionID: item.ID,
			Status:    item.Status,
			Text:      item.Output,
			Usage:     item.Usage,
		}}, nil

	default:
		return []StreamDelta{{
			Kind:      DeltaOther,
			SessionID: item.ID,
			Raw:       item.Type,
		}}, nil
	}
}

func buildCodexArgs(opts ParsedOptions) []string {
	args := []string{
		"-q",
		"--dangerously-auto-approve-everything",
	}
	if opts.Model != "" {
		args = append(args, "-m", opts.Model)
	}
	for _, dir := range opts.AddDirs {
		args = append(args, "-w", dir)
	}
	if opts.Prompt != "" {
		args = append(args, opts.Prompt)
	}
	return args
}
