package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// CloudSessionAdapter launches an Anthropic-managed cloud session via
// `claude --cloud`, consuming the $250 cloud session credit instead of local
// or API quota. Cloud sessions are **async by design**: the CLI exits after
// launching the session without streaming output. The adapter adopts a
// repo-output contract — the injected prompt instructs the cloud session to
// write its result to a file and open a PR so the result is retrievable.
//
// Environment variables:
//
//	CLAUDE_CLOUD_OUTPUT_FILE  Override the output file path written inside the session.
//	CLAUDE_CLOUD_REPO_DIR     Working dir passed to --cloud (defaults to req.Dir).
type CloudSessionAdapter struct{}

func (CloudSessionAdapter) Provider() string          { return "cloud_session" }
func (CloudSessionAdapter) BinaryName() string        { return "claude" }
func (CloudSessionAdapter) KnownMajorVersions() []int { return []int{2} }

func (CloudSessionAdapter) BuildArgs(opts ParsedOptions) []string {
	return buildCloudSessionArgs(opts)
}

func (a CloudSessionAdapter) Execute(ctx context.Context, req ExecRequest) error {
	return executeCloudSession(ctx, req)
}

// cloudLaunchResult is the JSON object emitted by `claude --cloud --output-format json`.
type cloudLaunchResult struct {
	SessionID string `json:"session_id"`
	URL       string `json:"url"`
}

// ParseStreamDelta handles the single synthetic result line emitted by Execute.
func (CloudSessionAdapter) ParseStreamDelta(line []byte) ([]StreamDelta, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, nil
	}
	var ev claudeEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil, fmt.Errorf("cloud_session stream: %w", err)
	}
	if ev.Type == "result" {
		return []StreamDelta{{
			Kind:      DeltaResult,
			SessionID: ev.SessionID,
			Status:    ev.Subtype,
			Text:      ev.Result,
			IsError:   ev.IsError,
		}}, nil
	}
	return []StreamDelta{{Kind: DeltaOther, SessionID: ev.SessionID, Raw: ev.Type}}, nil
}

func buildCloudSessionArgs(opts ParsedOptions) []string {
	args := []string{
		"--cloud",
		"--dangerously-skip-permissions",
		"--output-format", "json",
	}
	if opts.Prompt != "" {
		args = append(args, opts.Prompt)
	}
	return args
}

// wrapCloudPrompt appends the repo-output contract to the user prompt so the
// cloud session writes its result back into the repo via a branch + PR.
func wrapCloudPrompt(prompt, sessionSlug string) string {
	outputFile := os.Getenv("CLAUDE_CLOUD_OUTPUT_FILE")
	if outputFile == "" {
		if sessionSlug != "" {
			outputFile = "docs/cloud-output/" + sessionSlug + ".md"
		} else {
			outputFile = "docs/cloud-output/result.md"
		}
	}
	slug := sessionSlug
	if slug == "" {
		slug = "cloud-session"
	}
	suffix := fmt.Sprintf(
		"\n\nAfter completing the task, write your full output and result to %s, then run:\n"+
			"  git checkout -b cloud/%s && git add -A && git commit -m \"cloud session result\" "+
			"&& git push origin HEAD && gh pr create --title \"Cloud session result: %s\" --body \"Cloud session ID: %s\"",
		outputFile, slug, slug, slug,
	)
	return prompt + suffix
}

func executeCloudSession(ctx context.Context, req ExecRequest) error {
	dir := req.Dir
	if envDir := os.Getenv("CLAUDE_CLOUD_REPO_DIR"); envDir != "" {
		dir = envDir
	}

	wrappedOpts := req.Opts
	wrappedOpts.Prompt = wrapCloudPrompt(req.Opts.Prompt, "")

	var outBuf bytes.Buffer
	args := buildCloudSessionArgs(wrappedOpts)
	if err := runCommandWithEnv(ctx, dir, req.Bin, args, req.ExtraEnv, req.Stdin, &outBuf, req.Stderr); err != nil {
		return fmt.Errorf("cloud session launch: %w", err)
	}

	// Parse session_id and url from JSON output.
	raw := bytes.TrimSpace(outBuf.Bytes())
	var launch cloudLaunchResult
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &launch)
	}

	resultParts := []string{"Cloud session started."}
	if launch.URL != "" {
		resultParts = append(resultParts, "URL: "+launch.URL)
	}
	if launch.SessionID != "" {
		resultParts = append(resultParts, "Result will appear in branch: cloud/"+launch.SessionID)
	}

	// Emit synthetic stream-json result event so existing consumers handle it.
	type syntheticResult struct {
		Type      string `json:"type"`
		Subtype   string `json:"subtype"`
		SessionID string `json:"session_id,omitempty"`
		Result    string `json:"result"`
	}
	event := syntheticResult{
		Type:      "result",
		Subtype:   "cloud_session_started",
		SessionID: launch.SessionID,
		Result:    strings.Join(resultParts, "\n"),
	}
	b, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("cloud session marshal result: %w", err)
	}
	_, err = fmt.Fprintf(req.Stdout, "%s\n", b)
	return err
}
