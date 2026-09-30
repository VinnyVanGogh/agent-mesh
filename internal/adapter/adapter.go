package adapter

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"regexp"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/router"
)

// testBinKey is the context key existing tests use to substitute a fake CLI
// binary for every provider. The plain string type is kept for compatibility.
const testBinKey = "testBin"

// runCommandWithEnv executes a command with optional extra environment variables,
// anti-stall keepalive newlines, and unthrottled 32KB chunk streaming.
func runCommandWithEnv(ctx context.Context, dir string, bin string, args []string, extraEnv []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	cmdExec := exec.Command(bin, args...)
	cmdExec.Dir = dir
	if stdin != nil {
		cmdExec.Stdin = stdin
	}
	cmdExec.Stderr = stderr
	if len(extraEnv) > 0 {
		cmdExec.Env = append(os.Environ(), extraEnv...)
	}
	setProcessGroup(cmdExec)

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
			killProcessGroup(cmdExec, 1500*time.Millisecond)
			go func() {
				_ = cmdExec.Wait()
			}()
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

// providerCandidate represents a single provider in the failover chain.
type providerCandidate struct {
	Name     string          // "gemini", "work-claude", "personal-claude"
	PoolID   router.PoolID   // for quota lock check
	Adapter  ProviderAdapter
	ExtraEnv []string        // e.g. CLAUDE_CONFIG_DIR for work claude
}

// isPoolLocked returns true if the quota pool is hard-locked or has zero 5-hour headroom.
func isPoolLocked(pool *router.QuotaPool) bool {
	if pool == nil {
		return false
	}
	if pool.IsLocked {
		return true
	}
	if pool.FiveHour.ResetsAt.After(time.Now()) && pool.FiveHour.RemainingPct <= 0.0 {
		return true
	}
	return false
}

// BuildProviderChain constructs the ordered failover chain based on repo type and starting provider.
//
// Work repo chains:
//   provider=gemini: [gemini, work-claude, personal-claude]
//   provider=claude: [work-claude, personal-claude, gemini]
//
// Personal repo chains:
//   provider=gemini: [gemini, personal-claude]
//   provider=claude: [personal-claude, gemini]
func BuildProviderChain(isWork bool, provider string) []providerCandidate {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "~"
	}

	gemini := providerCandidate{
		Name:      "gemini",
		PoolID:    router.PoolGeminiNative,
		Adapter:   AgyAdapter{},
	}

	workClaude := providerCandidate{
		Name:      "work-claude",
		PoolID:    router.PoolWorkClaude,
		Adapter:   ClaudeAdapter{},
		ExtraEnv:  []string{"CLAUDE_CONFIG_DIR=" + home + "/.claude-work"},
	}

	personalClaude := providerCandidate{
		Name:      "personal-claude",
		PoolID:    router.PoolPersonalClaude,
		Adapter:   ClaudeAdapter{},
	}

	if isWork {
		if provider == "claude" {
			return []providerCandidate{workClaude, personalClaude, gemini}
		}
		// provider == "gemini" (or default)
		return []providerCandidate{gemini, workClaude, personalClaude}
	}

	// Personal repo
	if provider == "claude" {
		return []providerCandidate{personalClaude, gemini}
	}
	return []providerCandidate{gemini, personalClaude}
}

// RunAdapter executes the requested agent provider with chain-based failover.
// Every invocation re-evaluates quota fresh. No sticky state between runs.
func RunAdapter(ctx context.Context, cwd string, pacerState *router.PacerState, provider string, rawArgs []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	var resolve func(ProviderAdapter) (string, error)
	if testBin, ok := ctx.Value(testBinKey).(string); ok && testBin != "" {
		resolve = func(ProviderAdapter) (string, error) { return testBin, nil }
	} else {
		resolve = NewDefaultResolver().Resolve
	}
	return runWithFailover(ctx, cwd, pacerState, provider, rawArgs, stdin, stdout, stderr, resolve)
}

func runWithFailover(ctx context.Context, cwd string, pacerState *router.PacerState, provider string, rawArgs []string, stdin io.Reader, stdout io.Writer, stderr io.Writer, resolve func(ProviderAdapter) (string, error)) error {
	opts := parseRawArgs(rawArgs)

	// Buffer stdin once so the fallback CLI receives the same input as the primary.
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

	// Detect work vs personal repo
	isWork, workSrc, _ := router.IsWorkRepo(cwd)

	// Build the provider chain
	chain := BuildProviderChain(isWork, provider)

	if isWork && workSrc != "" {
		fmt.Fprintf(stderr, "[staypoint-adapter] work repo detected (%s), chain: ", workSrc)
	} else {
		fmt.Fprintf(stderr, "[staypoint-adapter] personal repo, chain: ")
	}
	names := make([]string, len(chain))
	for i, c := range chain {
		names[i] = c.Name
	}
	fmt.Fprintf(stderr, "%s\n", strings.Join(names, " > "))

	if pacerState == nil {
		pacerState = &router.PacerState{
			Pools: make(map[router.PoolID]*router.QuotaPool),
		}
	}

	var lastErr error
	firstAttempt := true
	for _, candidate := range chain {
		pool := pacerState.Pools[candidate.PoolID]
		if isPoolLocked(pool) {
			fmt.Fprintf(stderr, "[staypoint-adapter] skipping %s (quota locked)\n", candidate.Name)
			continue
		}

		// Clear conversation ID for fallback candidates.
		// Session IDs are provider-specific: a Claude session cannot be resumed in agy.
		candidateOpts := opts
		if !firstAttempt {
			candidateOpts.ConversationID = ""
		}
		firstAttempt = false

		var buf bytes.Buffer
		fmt.Fprintf(stderr, "[staypoint-adapter] trying %s...\n", candidate.Name)

		bin, err := resolve(candidate.Adapter)
		if err != nil {
			fmt.Fprintf(stderr, "[staypoint-adapter] %s failed to resolve (%v), trying next provider...\n", candidate.Name, err)
			lastErr = err
			continue
		}

		err = candidate.Adapter.Execute(ctx, ExecRequest{
			Bin:      bin,
			Dir:      cwd,
			Opts:     candidateOpts,
			ExtraEnv: candidate.ExtraEnv,
			Stdin:    bytes.NewReader(stdinBytes),
			Stdout:   &buf,
			Stderr:   stderr,
		})

		if err == nil {
			// Success: flush buffered output to real stdout
			_, _ = io.Copy(stdout, &buf)
			return nil
		}

		// Failed: swallow stdout, log to stderr, try next candidate
		fmt.Fprintf(stderr, "[staypoint-adapter] %s failed (%v), trying next provider...\n", candidate.Name, err)
		lastErr = err
	}

	// All candidates exhausted
	if lastErr != nil {
		return fmt.Errorf("[staypoint-adapter] all providers exhausted: last error: %w", lastErr)
	}
	return fmt.Errorf("[staypoint-adapter] all providers exhausted or locked for this environment")
}
