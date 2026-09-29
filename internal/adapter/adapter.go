package adapter

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/router"
)

func runCommandWithKeepalive(ctx context.Context, bin string, args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	cmdExec := exec.CommandContext(ctx, bin, args...)
	if stdin != nil {
		cmdExec.Stdin = stdin
	}
	cmdExec.Stderr = stderr

	cmdStdout, err := cmdExec.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe error: %w", err)
	}

	if err := cmdExec.Start(); err != nil {
		return fmt.Errorf("start error: %w", err)
	}

	ch := make(chan []byte, 1000)
	errCh := make(chan error, 1)

	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := cmdStdout.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				ch <- chunk
			}
			if err != nil {
				if err != io.EOF {
					errCh <- err
				}
				close(ch)
				return
			}
		}
	}()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case chunk, ok := <-ch:
			if !ok {
				err := cmdExec.Wait()
				if err != nil {
					return fmt.Errorf("agent exited with error: %w", err)
				}
				return nil
			}
			if _, err := stdout.Write(chunk); err != nil {
				return err
			}
		case <-ticker.C:
			// Emit newline keepalive to keep Paperclip stream consumer alive
			_, _ = stdout.Write([]byte("\n"))
		case err := <-errCh:
			return err
		case <-ctx.Done():
			if cmdExec.Process != nil {
				_ = cmdExec.Process.Kill()
			}
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

	argLoop:
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
			if i+1 < len(rawArgs) {
				i++ // Drop flag with its parameter
			}
		case arg == "--":
			if i+1 < len(rawArgs) {
				opts.Prompt = strings.Join(rawArgs[i+1:], " ")
			}
			break argLoop
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
		"--output-format", opts.OutputFormat,
		"--dangerously-skip-permissions",
	}
	if opts.Prompt != "" {
		args = append(args, "--prompt", opts.Prompt)
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

// RunAdapter executes the requested agent provider with anti-stall keepalive and quota failover.
func RunAdapter(ctx context.Context, cwd string, pacerState *router.PacerState, provider string, rawArgs []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	opts := parseRawArgs(rawArgs)

	var stdinBytes []byte
	if stdin != nil {
		data, err := io.ReadAll(stdin)
		if err == nil && len(data) > 0 {
			stdinBytes = data
			if opts.Prompt == "" {
				opts.Prompt = string(data)
			}
		}
	}

	// Check if overridden by test
	if testBin := ctx.Value("testBin"); testBin != nil {
		bin := testBin.(string)
		args := buildAgyArgs(opts)
		return runCommandWithKeepalive(ctx, bin, args, bytes.NewReader(stdinBytes), stdout, stderr)
	}

	// Routing logic
	targetTool := "agy"
	if provider == "claude" {
		targetTool = "claude"
	}

	if pacerState != nil {
		poolGemini := pacerState.Pools[router.PoolGeminiNative]
		poolClaude := pacerState.Pools[router.PoolPersonalClaude]
		if poolClaude == nil {
			poolClaude = pacerState.Pools[router.PoolWorkClaude]
		}

		claudeLocked := poolClaude != nil && (poolClaude.IsLocked || (poolClaude.FiveHour.ResetsAt.After(time.Now()) && poolClaude.FiveHour.RemainingPct <= 0.0))
		geminiLocked := poolGemini != nil && (poolGemini.IsLocked || (poolGemini.FiveHour.ResetsAt.After(time.Now()) && poolGemini.FiveHour.RemainingPct <= 0.0))

		if targetTool == "agy" {
			if geminiLocked && !claudeLocked {
				fmt.Fprintf(stderr, "[paperclip-quota-gate] ⚠️ Gemini locked. Failing over to Claude...\n")
				targetTool = "claude"
			}
		} else if targetTool == "claude" {
			if claudeLocked && !geminiLocked {
				fmt.Fprintf(stderr, "[paperclip-quota-gate] ⚠️ Claude locked or exhausted. Failing over to Gemini...\n")
				targetTool = "agy"
			}
		}
	}

	// Bidirectional execution: try targetTool first; if it fails, silently fail over/back to the alternative tool.
	if targetTool == "claude" {
		bin := "/Users/vincevasile/.local/bin/claude"
		args := buildClaudeArgs(opts)
		if provider == "gemini" {
			var buf bytes.Buffer
			err := runCommandWithKeepalive(ctx, bin, args, bytes.NewReader(stdinBytes), &buf, stderr)
			if err != nil {
				fmt.Fprintf(stderr, "[paperclip-quota-gate] ⚠️ Claude failover failed (%v), failing back to Gemini...\n", err)
				fallbackBin := "/Users/vincevasile/.local/bin/agy"
				fallbackArgs := buildAgyArgs(opts)
				return runCommandWithKeepalive(ctx, fallbackBin, fallbackArgs, bytes.NewReader(stdinBytes), stdout, stderr)
			}
			_, _ = io.Copy(stdout, &buf)
			return nil
		}
		err := runCommandWithKeepalive(ctx, bin, args, bytes.NewReader(stdinBytes), stdout, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "[paperclip-quota-gate] ⚠️ Claude failed (%v), failing back to Gemini...\n", err)
			fallbackBin := "/Users/vincevasile/.local/bin/agy"
			fallbackArgs := buildAgyArgs(opts)
			return runCommandWithKeepalive(ctx, fallbackBin, fallbackArgs, bytes.NewReader(stdinBytes), stdout, stderr)
		}
		return nil
	}

	// Try agy first
	bin := "/Users/vincevasile/.local/bin/agy"
	args := buildAgyArgs(opts)
	if provider == "claude" {
		var buf bytes.Buffer
		err := runCommandWithKeepalive(ctx, bin, args, bytes.NewReader(stdinBytes), &buf, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "[paperclip-quota-gate] ⚠️ Gemini failover failed (%v), failing back to Claude...\n", err)
			fallbackBin := "/Users/vincevasile/.local/bin/claude"
			fallbackArgs := buildClaudeArgs(opts)
			return runCommandWithKeepalive(ctx, fallbackBin, fallbackArgs, bytes.NewReader(stdinBytes), stdout, stderr)
		}
		_, _ = io.Copy(stdout, &buf)
		return nil
	}
	err := runCommandWithKeepalive(ctx, bin, args, bytes.NewReader(stdinBytes), stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "[paperclip-quota-gate] ⚠️ Gemini failed (%v), falling back to Claude...\n", err)
		fallbackBin := "/Users/vincevasile/.local/bin/claude"
		fallbackArgs := buildClaudeArgs(opts)
		return runCommandWithKeepalive(ctx, fallbackBin, fallbackArgs, bytes.NewReader(stdinBytes), stdout, stderr)
	}

	return nil
}
