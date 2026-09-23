package condenser

import (
	"fmt"
	"regexp"
	"strings"
)

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// StripANSI removes terminal color and cursor escape sequences.
func StripANSI(raw string) string {
	return ansiRegex.ReplaceAllString(raw, "")
}

// DeduplicateLines compresses identical consecutive lines into repeat markers.
func DeduplicateLines(lines []string) []string {
	if len(lines) == 0 {
		return lines
	}

	var result []string
	var lastLine string
	var repeatCount int

	flushRepeat := func() {
		if repeatCount > 1 {
			result = append(result, fmt.Sprintf("  [... previous line repeated %d times ...]", repeatCount))
		} else if repeatCount == 1 {
			result = append(result, lastLine)
		}
		repeatCount = 0
	}

	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \r\t")
		if trimmed == lastLine && trimmed != "" {
			repeatCount++
		} else {
			flushRepeat()
			result = append(result, trimmed)
			lastLine = trimmed
		}
	}
	flushRepeat()

	return result
}

// ClampLines limits output to maxLines, keeping top and tail if truncated.
func ClampLines(lines []string, maxLines int) []string {
	if maxLines <= 0 || len(lines) <= maxLines {
		return lines
	}

	headCount := maxLines * 2 / 3
	tailCount := maxLines - headCount - 1
	if tailCount < 1 {
		tailCount = 1
	}

	var result []string
	result = append(result, lines[:headCount]...)
	omitted := len(lines) - headCount - tailCount
	result = append(result, fmt.Sprintf("... [Token Diet: %d verbose lines omitted] ...", omitted))
	result = append(result, lines[len(lines)-tailCount:]...)

	return result
}
