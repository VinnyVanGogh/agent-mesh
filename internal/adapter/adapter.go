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

	ch := make(chan string, 100)
	go func() {
		scanner := bufio.NewScanner(cmdStdout)
		buf := make([]byte, 64*1024)
		scanner.Buffer(buf, 16*1024*1024)
		for scanner.Scan() {
			ch <- scanner.Text()
		}
		close(ch)
	}()

	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()

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
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(2 * time.Second)
		case <-timer.C:
			// Emitting an empty string for keepalive to prevent Paperclip stall
			fmt.Fprintln(stdout, "")
			timer.Reset(2 * time.Second)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func RunAdapter(ctx context.Context, cwd string, pacerState *router.PacerState, rawArgs []string, stdout io.Writer, stderr io.Writer) error {
	var cleanArgs []string
	var modelVal string
	var effortVal string
	hasPrompt := false

	for i := 0; i < len(rawArgs); i++ {
		arg := rawArgs[i]
		switch {
		case arg == "--approval-mode":
			i++ // Skip next
		case arg == "--sandbox=none" || arg == "--sandbox" || arg == "-acp" || arg == "--acp":
			// Drop
		case arg == "--resume":
			if i+1 < len(rawArgs) {
				cleanArgs = append(cleanArgs, "--conversation", rawArgs[i+1])
				i++
			}
		case arg == "--model":
			if i+1 < len(rawArgs) {
				val := strings.TrimPrefix(rawArgs[i+1], "google/")
				re := regexp.MustCompile(`^(.*)-(low|medium|high)$`)
				if m := re.FindStringSubmatch(val); m != nil {
					modelVal = m[1]
					effortVal = m[2]
				} else {
					modelVal = val
				}
				cleanArgs = append(cleanArgs, "--model", modelVal)
				i++
			}
		case arg == "--effort":
			if i+1 < len(rawArgs) {
				effortVal = rawArgs[i+1]
				i++
			}
		case arg == "--output-format" || arg == "--input-format" || arg == "--add-dir":
			if i+1 < len(rawArgs) {
				cleanArgs = append(cleanArgs, arg, rawArgs[i+1])
				i++
			}
		case strings.HasPrefix(arg, "--output-format=") || strings.HasPrefix(arg, "--input-format=") || strings.HasPrefix(arg, "--add-dir="):
			cleanArgs = append(cleanArgs, arg)
		case arg == "--prompt":
			if i+1 < len(rawArgs) {
				cleanArgs = append(cleanArgs, "--prompt", rawArgs[i+1])
				hasPrompt = true
				i++
			}
		case strings.HasPrefix(arg, "--prompt="):
			cleanArgs = append(cleanArgs, arg)
			hasPrompt = true
		case strings.HasPrefix(arg, "--print=") || strings.HasPrefix(arg, "-p="):
			parts := strings.SplitN(arg, "=", 2)
			if len(parts) > 1 {
				cleanArgs = append(cleanArgs, "--prompt", parts[1])
				hasPrompt = true
			}
		case arg == "--print" || arg == "-p":
			if i+1 < len(rawArgs) && !strings.HasPrefix(rawArgs[i+1], "-") {
				cleanArgs = append(cleanArgs, "--prompt", rawArgs[i+1])
				hasPrompt = true
				i++
			}
		case arg == "--dangerously-skip-permissions":
			cleanArgs = append(cleanArgs, arg)
		case arg == "--verbose" || arg == "-v" || arg == "--strict-mcp-config" || arg == "--no-session-persistence" || arg == "--include-hook-events" || arg == "--include-partial-messages" || arg == "--replay-user-messages" || arg == "--restricted" || arg == "--safe-mode" || arg == "--chrome":
			// Drop
		case arg == "--mcp-config" || arg == "--config-dir" || arg == "--cwd" || arg == "--settings" || arg == "--permission-mode" || arg == "--permission-prompts" || arg == "--setting-sources" || arg == "--max-turns" || arg == "--max-budget-usd" || arg == "--tools" || arg == "--system-prompt" || arg == "--system-prompt-snapshot" || arg == "--append-system-prompt-file":
			i++ // Drop with next
		default:
			if !strings.HasPrefix(arg, "-") && !hasPrompt {
				cleanArgs = append(cleanArgs, "--prompt", arg)
				hasPrompt = true
			} else {
				cleanArgs = append(cleanArgs, arg)
			}
		}
	}

	if effortVal == "" {
		if strings.Contains(modelVal, "gemini-3.1-pro") {
			effortVal = "high"
		} else if strings.Contains(modelVal, "gemini-3.8-flash") {
			effortVal = "medium"
		} else {
			effortVal = "high"
		}
	}
	if effortVal != "" {
		cleanArgs = append(cleanArgs, "--effort", effortVal)
	}

	// Route decision
	decision, _ := router.Route(ctx, cwd, pacerState, router.RouteOptions{
		CheckSSH:       false,
		PreferredModel: modelVal,
	})

	// Fallback to agy if testing or missing command
	cmdName := decision.Command
	if cmdName == "" {
		cmdName = "agy"
	}

	bin := "/Users/vincevasile/.local/bin/" + cmdName
	var runArgs []string
	if cmdName == "agy" {
		runArgs = append(cleanArgs, "--dangerously-skip-permissions")
	} else {
	    runArgs = cleanArgs
	}

	isTest := false
	// For testing purposes: allow overriding bin
	if testBin := ctx.Value("testBin"); testBin != nil {
		bin = testBin.(string)
		isTest = true
	}

	err := runCommandWithKeepalive(ctx, bin, runArgs, stdout, stderr)
	if err != nil {
	    if !isTest && cmdName == "agy" {
	        fmt.Fprintf(stderr, "[paperclip-quota-gate] \u26A0\uFE0F Gemini failed (%v), falling back to Claude...\n", err)
	        // Attempt fallback to Claude
	        fallbackBin := "/Users/vincevasile/.local/bin/claude"
	        return runCommandWithKeepalive(ctx, fallbackBin, cleanArgs, stdout, stderr)
	    }
	    return err
	}
	return nil
}
