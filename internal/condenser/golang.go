package condenser

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	goPanicRegex     = regexp.MustCompile(`(?m)^panic:\s*(.*)$`)
	goGoroutineRegex = regexp.MustCompile(`(?m)^goroutine\s+(\d+)\s+\[([^\]]+)\]:`)
	goTestFailRegex  = regexp.MustCompile(`(?m)^--- FAIL:\s*([^\s]+)\s*\((.*?)\)`)
)

// IsGoOutput checks if text looks like Go compiler, test failure, or panic output.
func IsGoOutput(text string) bool {
	return goPanicRegex.MatchString(text) || strings.Contains(text, "=== RUN") || goTestFailRegex.MatchString(text)
}

// CondenseGo processes Go panics and go test outputs.
func CondenseGo(text string, maxLines int) string {
	clean := StripANSI(text)

	if goPanicRegex.MatchString(clean) {
		return condenseGoPanic(clean, maxLines)
	}

	if strings.Contains(clean, "=== RUN") || goTestFailRegex.MatchString(clean) {
		return condenseGoTest(clean, maxLines)
	}

	return strings.Join(ClampLines(DeduplicateLines(strings.Split(clean, "\n")), maxLines), "\n")
}

func condenseGoPanic(text string, maxLines int) string {
	lines := strings.Split(text, "\n")
	var output []string

	inPanickingGoroutine := false
	runtimeFrameCount := 0

	flushRuntimeFrames := func() {
		if runtimeFrameCount > 0 {
			output = append(output, fmt.Sprintf("    [... %d Go runtime frames collapsed ...]", runtimeFrameCount))
			runtimeFrameCount = 0
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// 1. Capture panic line
		if strings.HasPrefix(trimmed, "panic:") {
			output = append(output, trimmed)
			continue
		}

		// 2. Check goroutine header
		if matches := goGoroutineRegex.FindStringSubmatch(trimmed); len(matches) == 3 {
			flushRuntimeFrames()
			status := matches[2]
			if strings.Contains(status, "running") {
				inPanickingGoroutine = true
				output = append(output, trimmed)
			} else {
				// Non-running goroutines (chan receive, select, sleep) are discarded
				inPanickingGoroutine = false
			}
			continue
		}

		// If in idle goroutine, skip its stack lines
		if !inPanickingGoroutine {
			continue
		}

		// In panicking goroutine: collapse runtime frames
		if strings.Contains(trimmed, "runtime/") {
			runtimeFrameCount++
			continue
		}

		flushRuntimeFrames()
		if trimmed != "" {
			output = append(output, "  "+trimmed)
		}
	}
	flushRuntimeFrames()

	if len(output) == 0 {
		return strings.Join(ClampLines(lines, maxLines), "\n")
	}

	return strings.Join(ClampLines(output, maxLines), "\n")
}

func condenseGoTest(text string, maxLines int) string {
	lines := strings.Split(text, "\n")
	var output []string
	inFailure := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip successful test passes
		if strings.HasPrefix(trimmed, "=== RUN") || strings.HasPrefix(trimmed, "--- PASS:") || strings.HasPrefix(trimmed, "PASS") {
			inFailure = false
			continue
		}

		// Capture failure marker
		if strings.HasPrefix(trimmed, "--- FAIL:") || strings.HasPrefix(trimmed, "FAIL") {
			inFailure = true
			output = append(output, line)
			continue
		}

		// Keep lines within failing test
		if inFailure && trimmed != "" {
			output = append(output, line)
		}
	}

	if len(output) == 0 {
		// Compilation failure or other error without test run
		return strings.Join(ClampLines(DeduplicateLines(lines), maxLines), "\n")
	}

	return strings.Join(ClampLines(output, maxLines), "\n")
}
