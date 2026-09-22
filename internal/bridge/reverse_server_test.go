package bridge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractClientPaths(t *testing.T) {
	clientHome := "/Users/testuser"

	tests := []struct {
		name     string
		prompt   string
		expected []string
	}{
		{
			name:     "Single client path",
			prompt:   "Can you take a look at /Users/testuser/Desktop/screenshot.png and tell me what it shows?",
			expected: []string{"/Users/testuser/Desktop/screenshot.png"},
		},
		{
			name:     "Multiple paths with trailing punctuation",
			prompt:   "Compare /Users/testuser/Documents/a.txt, with /Users/testuser/Documents/b.txt! Also check (/Users/testuser/Desktop/c.png).",
			expected: []string{"/Users/testuser/Documents/a.txt", "/Users/testuser/Documents/b.txt", "/Users/testuser/Desktop/c.png"},
		},
		{
			name:     "Duplicate paths in prompt",
			prompt:   "Look at /Users/testuser/foo.go and again /Users/testuser/foo.go please.",
			expected: []string{"/Users/testuser/foo.go"},
		},
		{
			name:     "Non-client paths ignored",
			prompt:   "Look at /tmp/foo.txt or /etc/hosts or /Users/otheruser/file.txt",
			expected: nil,
		},
		{
			name:     "Empty prompt or home",
			prompt:   "",
			expected: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractClientPaths(tc.prompt, clientHome)
			if len(got) != len(tc.expected) {
				t.Fatalf("ExtractClientPaths() returned %d items, want %d: %v", len(got), len(tc.expected), got)
			}
			for i := range got {
				if got[i] != tc.expected[i] {
					t.Errorf("match [%d]: got %q, want %q", i, got[i], tc.expected[i])
				}
			}
		})
	}
}

func TestReverseBridgeServerAndFetch(t *testing.T) {
	tmpDir := t.TempDir()
	clientHome := filepath.Join(tmpDir, "client-home")
	_ = os.MkdirAll(clientHome, 0755)

	// Create test file
	testFile := filepath.Join(clientHome, "hello.txt")
	expectedContent := "Hello from client machine over reverse tunnel!"
	if err := os.WriteFile(testFile, []byte(expectedContent), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	token := "test-secret-token-12345"
	port, shutdown, err := StartReverseBridgeServer(clientHome, token, 0)
	if err != nil {
		t.Fatalf("StartReverseBridgeServer error: %v", err)
	}
	defer shutdown()

	// Mock session on disk
	sessionDir := filepath.Join(tmpDir, ".agent-mesh")
	_ = os.MkdirAll(sessionDir, 0755)
	sessionPath := filepath.Join(sessionDir, "bridge-session.json")

	session := BridgeSession{
		ClientUser: "testuser",
		ClientHome: clientHome,
		ClientHost: "test-client",
		BridgePort: port,
		Token:      token,
		Active:     true,
	}

	// Override default session path in test
	origSessionPath := bridgeSessionPathOverride
	bridgeSessionPathOverride = sessionPath
	defer func() { bridgeSessionPathOverride = origSessionPath }()

	if err := SaveBridgeSession(session); err != nil {
		t.Fatalf("failed to save mock bridge session: %v", err)
	}

	ctx := context.Background()

	// 1. Fetch existing file
	destFile := filepath.Join(tmpDir, "fetched.txt")
	gotDest, err := FetchClientFile(ctx, testFile, destFile)
	if err != nil {
		t.Fatalf("FetchClientFile error: %v", err)
	}
	if gotDest != destFile {
		t.Errorf("FetchClientFile returned %s, want %s", gotDest, destFile)
	}

	content, err := os.ReadFile(destFile)
	if err != nil {
		t.Fatalf("failed to read fetched file: %v", err)
	}
	if string(content) != expectedContent {
		t.Errorf("fetched content = %q, want %q", string(content), expectedContent)
	}

	// 2. Try fetching forbidden path outside clientHome and outside temp
	outsideFile := "/opt/secret/forbidden.txt"
	_, err = FetchClientFile(ctx, outsideFile, "")
	if err == nil {
		t.Errorf("expected error fetching forbidden file, got nil")
	}

	// 3. Test ProcessPromptForClientPaths
	prompt := "Check " + testFile + " please"
	guidance, cachedPaths, err := ProcessPromptForClientPaths(ctx, prompt)
	if err != nil {
		t.Fatalf("ProcessPromptForClientPaths error: %v", err)
	}
	if len(cachedPaths) != 1 {
		t.Fatalf("expected 1 cached path, got %v", cachedPaths)
	}
	if !strings.Contains(guidance, "AGENT-MESH AUTO-FETCH") {
		t.Errorf("guidance missing header: %s", guidance)
	}
}
