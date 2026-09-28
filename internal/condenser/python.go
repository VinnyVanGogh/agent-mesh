package condenser

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	pyTracebackStartRegex = regexp.MustCompile(`(?m)^Traceback \(most recent call last\):`)
	pyFrameRegex          = regexp.MustCompile(`^\s*File "([^"]+)", line (\d+), in (.*)$`)
)

// IsPythonOutput checks if text looks like a Python traceback.
func IsPythonOutput(text string) bool {
	return pyTracebackStartRegex.MatchString(text) || strings.Contains(text, "Traceback (most recent call last):")
}

// CondensePython collapses library frames in Python tracebacks, isolating user code and exceptions.
func CondensePython(text string, maxLines int) string {
	clean := StripANSI(text)
	lines := strings.Split(clean, "\n")

	var output []string
	var inTraceback bool
	libraryFrameCount := 0

	flushLibFrames := func() {
		if libraryFrameCount > 0 {
			output = append(output, fmt.Sprintf("    [... %d library frames in site-packages collapsed ...]", libraryFrameCount))
			libraryFrameCount = 0
		}
	}

	i := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "Traceback (most recent call last):") {
			inTraceback = true
			output = append(output, trimmed)
			i++
			continue
		}

		if inTraceback {
			// Frame header line: File "...", line X, in Y
			if matches := pyFrameRegex.FindStringSubmatch(line); len(matches) == 4 {
				filePath := matches[1]
				lineNum := matches[2]
				funcName := matches[3]

				isLibrary := strings.Contains(filePath, "/site-packages/") ||
					strings.Contains(filePath, "/lib/python") ||
					strings.Contains(filePath, "<frozen")

				// Read frame code line if present
				var codeLine string
				if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "    ") && !strings.HasPrefix(strings.TrimSpace(lines[i+1]), "File ") {
					codeLine = lines[i+1]
					i++
				}

				if isLibrary {
					libraryFrameCount++
				} else {
					flushLibFrames()
					output = append(output, fmt.Sprintf("  File \"%s\", line %s, in %s", filePath, lineNum, funcName))
					if codeLine != "" {
						output = append(output, codeLine)
					}
				}
				i++
				continue
			}

			// End of traceback: Exception: message
			if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && trimmed != "" {
				flushLibFrames()
				output = append(output, line)
				inTraceback = false
				i++
				continue
			}
		}

		// Lines outside traceback
		if !inTraceback && trimmed != "" {
			output = append(output, line)
		}
		i++
	}
	flushLibFrames()

	if len(output) == 0 {
		return strings.Join(ClampLines(lines, maxLines), "\n")
	}

	return strings.Join(ClampLines(output, maxLines), "\n")
}
