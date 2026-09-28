package condenser

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	tsErrorRegex = regexp.MustCompile(`(?m)^([^\s].*?\.(?:ts|tsx|js|jsx)):(\d+):(\d+)\s*-\s*error\s*(TS\d+):\s*(.*)$`)
	tsTypeRegex  = regexp.MustCompile(`\{[^{}]{80,}\}`)
)

// IsTypeScriptOutput checks if text looks like TypeScript/Vite/esbuild compiler output.
func IsTypeScriptOutput(text string) bool {
	return tsErrorRegex.MatchString(text) || strings.Contains(text, "error TS")
}

// CondenseTypeScript groups cascading TS errors by error code, keeping top instances.
func CondenseTypeScript(text string, maxLines int) string {
	lines := strings.Split(StripANSI(text), "\n")
	type ErrorGroup struct {
		Code    string
		Message string
		Samples []string
		Count   int
	}

	groups := make(map[string]*ErrorGroup)
	var groupOrder []string

	i := 0
	for i < len(lines) {
		line := lines[i]
		matches := tsErrorRegex.FindStringSubmatch(line)
		if len(matches) == 6 {
			file := matches[1]
			lineNum := matches[2]
			colNum := matches[3]
			code := matches[4]
			msg := matches[5]

			// Collect context lines following this error (indented lines or carets)
			var contextBlock []string
			contextBlock = append(contextBlock, fmt.Sprintf("%s:%s:%s - error %s: %s", file, lineNum, colNum, code, msg))
			i++
			for i < len(lines) && (strings.HasPrefix(lines[i], " ") || strings.HasPrefix(lines[i], "\t") || strings.HasPrefix(lines[i], "~")) {
				trimmed := lines[i]
				// Shorten gigantic types
				trimmed = tsTypeRegex.ReplaceAllString(trimmed, "{ ... [type truncated] ... }")
				contextBlock = append(contextBlock, trimmed)
				i++
			}

			eg, exists := groups[code]
			if !exists {
				eg = &ErrorGroup{
					Code:    code,
					Message: msg,
				}
				groups[code] = eg
				groupOrder = append(groupOrder, code)
			}
			eg.Count++
			if len(eg.Samples) < 2 {
				eg.Samples = append(eg.Samples, strings.Join(contextBlock, "\n"))
			}
			continue
		}

		i++
	}

	if len(groups) == 0 {
		return strings.Join(ClampLines(DeduplicateLines(lines), maxLines), "\n")
	}

	var output []string
	totalErrors := 0

	for _, code := range groupOrder {
		eg := groups[code]
		totalErrors += eg.Count
		output = append(output, fmt.Sprintf("─── [%s] (Occurred %d times) ───", eg.Code, eg.Count))
		output = append(output, eg.Samples...)
		if eg.Count > len(eg.Samples) {
			output = append(output, fmt.Sprintf("  ... and %d more %s errors collapsed", eg.Count-len(eg.Samples), eg.Code))
		}
		output = append(output, "")
	}

	output = append(output, fmt.Sprintf("Found %d total TypeScript errors across %d distinct error codes.", totalErrors, len(groups)))
	return strings.Join(ClampLines(output, maxLines), "\n")
}
