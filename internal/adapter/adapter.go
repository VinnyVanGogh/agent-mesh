package adapter

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/router"
)

func runCommandWithKeepalive(ctx context.Context, bin string, args []string, stdout io.Writer, stderr io.Writer) error {
	cmdExec := exec.CommandContext(ctx, bin, args...)
	cmdExec.Stderr = stderr

	cmdStdout, err := cmdExec.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe error: %w", err)
	}

	if err := cmdExec.Start(); err != nil {
		return fmt.Errorf("start error: %w", err)
	}

	// Buffered channel of 10,000 lines to prevent OS pipe backpressure from stalling agy
	ch := make(chan string, 10000)
	go func() {
		scanner := bufio.NewScanner(cmdStdout)
		buf := make([]byte, 64*1024)
		scanner.Buffer(buf, 16*1024*1024)
		for scanner.Scan() {
			ch <- scanner.Text()
		}
		close(ch)
	}()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case line, ok := <-ch:
			if !ok {
				err := cmdExec.Wait()
				if err != nil {
					return fmt.Errorf("agent exited with error: %w", err)
				}
				return nil
			}
			fmt.Fprintln(stdout, line)
		case <-ticker.C:
			// Emitting an empty string keepalive to prevent Paperclip stream consumer stall
			fmt.Fprintln(stdout, "")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// ParsedOptions holds common execution options extracted from adapter command arguments.
type ParsedOptions struct {
	Prompt         string
	Model          string
	Effort         string
	ConversationID string
	OutputFormat   string
	AddDirs        []string
}

func parseRawArgs(rawArgs []string) ParsedOptions {
	var opts ParsedOptions
	opts.OutputFormat = "stream-json"

	for i := 0; i < len(rawArgs); i++ {
		arg := rawArgs[i]
		switch {
		case arg == "--approval-mode":
			i++
		case arg == "--sandbox=none" || arg == "--sandbox" || arg == "-acp" || arg == "--acp":
			// Drop unsupported sandbox and acp flags
		case arg == "--resume" || arg == "--conversation":
			if i+1 < len(rawArgs) {
				opts.ConversationID = rawArgs[i+1]
				i++
			}
		case strings.HasPrefix(arg, "--conversation="):
			opts.ConversationID = strings.TrimPrefix(arg, "--conversation=")
		case strings.HasPrefix(arg, "--resume="):
			opts.ConversationID = strings.TrimPrefix(arg, "--resume=")
		case arg == "--model":
			if i+1 < len(rawArgs) {
				val := strings.TrimPrefix(rawArgs[i+1], "google/")
				re := regexp.MustCompile(`^(.*)-(low|medium|high)$`)
				if m := re.FindStringSubmatch(val); m != nil {
					opts.Model = m[1]
					opts.Effort = m[2]
				} else {
					opts.Model = val
				}
				i++
			}
		case strings.HasPrefix(arg, "--model="):
			val := strings.TrimPrefix(strings.TrimPrefix(arg, "--model="), "google/")
			re := regexp.MustCompile(`^(.*)-(low|medium|high)$`)
			if m := re.FindStringSubmatch(val); m != nil {
				opts.Model = m[1]
				opts.Effort = m[2]
			} else {
				opts.Model = val
			}
		case arg == "--effort":
			if i+1 < len(rawArgs) {
				opts.Effort = rawArgs[i+1]
				i++
			}
		case strings.HasPrefix(arg, "--effort="):
			opts.Effort = strings.TrimPrefix(arg, "--effort=")
		case arg == "--output-format":
			if i+1 < len(rawArgs) {
				opts.OutputFormat = rawArgs[i+1]
				i++
			}
		case strings.HasPrefix(arg, "--output-format="):
			opts.OutputFormat = strings.TrimPrefix(arg, "--output-format=")
		case arg == "--add-dir":
			if i+1 < len(rawArgs) {
				opts.AddDirs = append(opts.AddDirs, rawArgs[i+1])
				i++
			}
		case strings.HasPrefix(arg, "--add-dir="):
			opts.AddDirs = append(opts.AddDirs, strings.TrimPrefix(arg, "--add-dir="))
		case arg == "--prompt":
			if i+1 < len(rawArgs) {
				opts.Prompt = rawArgs[i+1]
				i++
			}
		case strings.HasPrefix(arg, "--prompt="):
			opts.Prompt = strings.TrimPrefix(arg, "--prompt=")
		case strings.HasPrefix(arg, "--print=") || strings.HasPrefix(arg, "-p="):
			parts := strings.SplitN(arg, "=", 2)
			if len(parts) > 1 {
				opts.Prompt = parts[1]
			}
		case arg == "--print" || arg == "-p":
			if i+1 < len(rawArgs) && !strings.HasPrefix(rawArgs[i+1], "-") {
				opts.Prompt = rawArgs[i+1]
				i++
			}
		case arg == "--dangerously-skip-permissions":
			// Handled unconditionally
		case arg == "--verbose" || arg == "-v" || arg == "--strict-mcp-config" || arg == "--no-session-persistence" || arg == "--include-hook-events" || arg == "--include-partial-messages" || arg == "--replay-user-messages" || arg == "--restricted" || arg == "--safe-mode" || arg == "--chrome":
			// Drop unsupported verbose and strict flags
		case arg == "--mcp-config" || arg == "--config-dir" || arg == "--cwd" || arg == "--settings" || arg == "--permission-mode" || arg == "--permission-prompts" || arg == "--setting-sources" || arg == "--max-turns" || arg == "--max-budget-usd" || arg == "--tools" || arg == "--system-prompt" || arg == "--system-prompt-snapshot" || arg == "--append-system-prompt-file":
			i++ // Drop flag with its parameter
		default:
			if !strings.HasPrefix(arg, "-") && opts.Prompt == "" {
				opts.Prompt = arg
			}
		}
	}

	if opts.Effort == "" {
		if strings.Contains(opts.Model, "gemini-3.1-pro") {
			opts.Effort = "high"
		} else if strings.Contains(opts.Model, "gemini-3.8-flash") {
			opts.Effort = "medium"
		}
	}

	return opts
}

func buildAgyArgs(opts ParsedOptions) []string {
	args := []string{
		"--prompt", opts.Prompt,
		"--output-format", opts.OutputFormat,
		"--dangerously-skip-permissions",
	}
	if opts.Model != "" && !strings.Contains(opts.Model, "claude") && !strings.Contains(opts.Model, "sonnet") && !strings.Contains(opts.Model, "opus") {
		args = append(args, "--model", opts.Model)
	}
	if opts.Effort != "" {
		args = append(args, "--effort", opts.Effort)
	}
	if opts.ConversationID != "" {
		args = append(args, "--conversation", opts.ConversationID)
	}
	for _, dir := range opts.AddDirs {
		args = append(args, "--add-dir", dir)
	}
	return args
}

func buildClaudeArgs(opts ParsedOptions) []string {
	args := []string{
		"--print", opts.Prompt,
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
	return args
}

// RunAdapter executes the requested agent provider with anti-stall keepalive and quota failover.
func RunAdapter(ctx context.Context, cwd string, pacerState *router.PacerState, provider string, rawArgs []string, stdout io.Writer, stderr io.Writer) error {
	opts := parseRawArgs(rawArgs)

	// Check if overridden by test
	if testBin := ctx.Value("testBin"); testBin != nil {
		bin := testBin.(string)
		args := buildAgyArgs(opts)
		return runCommandWithKeepalive(ctx, bin, args, stdout, stderr)
	}

	// Routing logic
	targetTool := "agy"
	if provider == "claude" {
		targetTool = "claude"
	}

	if pacerState != nil {
		poolGemini := pacerState.Pools[router.PoolGeminiNative]
		if poolGemini != nil && (poolGemini.IsLocked || poolGemini.Weekly.RemainingPct < 25.0) {
			if provider == "gemini" {
				fmt.Fprintf(stderr, "[paperclip-quota-gate] ⚠️ Gemini quota tight or locked (<25%% weekly headroom). Route recommending failover to Claude...\n")
				targetTool = "claude"
			}
		}
	}

	if targetTool == "claude" {
		bin := "/Users/vincevasile/.local/bin/claude"
		args := buildClaudeArgs(opts)
		return runCommandWithKeepalive(ctx, bin, args, stdout, stderr)
	}

	// Try agy first
	bin := "/Users/vincevasile/.local/bin/agy"
	args := buildAgyArgs(opts)
	err := runCommandWithKeepalive(ctx, bin, args, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "[paperclip-quota-gate] ⚠️ Gemini failed (%v), falling back to Claude...\n", err)
		fallbackBin := "/Users/vincevasile/.local/bin/claude"
		fallbackArgs := buildClaudeArgs(opts)
		return runCommandWithKeepalive(ctx, fallbackBin, fallbackArgs, stdout, stderr)
	}

	return nil
}
