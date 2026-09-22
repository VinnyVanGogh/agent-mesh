package bridge

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// ExtractClientPaths finds all paths in the user prompt that reference files on the client machine.
func ExtractClientPaths(prompt string, clientHome string) []string {
	if clientHome == "" || prompt == "" {
		return nil
	}

	cleanHome := filepath.Clean(clientHome)
	escapedHome := regexp.QuoteMeta(cleanHome)

	// Match: <clientHome>/[non-whitespace]+
	pattern := regexp.MustCompile(fmt.Sprintf(`(%s/[^\s"'\` + "`" + `<>]+)`, escapedHome))
	matches := pattern.FindAllString(prompt, -1)

	var results []string
	seen := make(map[string]bool)

	for _, m := range matches {
		cleaned := strings.TrimRight(m, ".,;:!?)'\"`>")
		if cleaned != "" && !seen[cleaned] {
			seen[cleaned] = true
			results = append(results, cleaned)
		}
	}

	return results
}

// ProcessPromptForClientPaths checks for active bridge session, extracts any client paths
// in the prompt, fetches them automatically to /tmp/mesh-cache, and returns context guidance for Claude.
func ProcessPromptForClientPaths(ctx context.Context, prompt string) (string, []string, error) {
	session, err := LoadBridgeSession()
	if err != nil || session == nil || !session.Active || session.ClientHome == "" {
		return "", nil, nil
	}

	clientPaths := ExtractClientPaths(prompt, session.ClientHome)
	if len(clientPaths) == 0 {
		return "", nil, nil
	}

	var cachedPaths []string
	var notices []string

	for _, cp := range clientPaths {
		cached, err := FetchClientFile(ctx, cp, "")
		if err == nil {
			cachedPaths = append(cachedPaths, cached)
			notices = append(notices, fmt.Sprintf("• Client file '%s' -> auto-fetched to '%s'", cp, cached))
		}
	}

	if len(notices) == 0 {
		return "", nil, nil
	}

	guidance := fmt.Sprintf(
		"📂 [AGENT-MESH AUTO-FETCH]: Detected client machine path(s) in prompt:\n%s\nPlease read and inspect the local cached file(s) on this system.",
		strings.Join(notices, "\n"),
	)

	return guidance, cachedPaths, nil
}
