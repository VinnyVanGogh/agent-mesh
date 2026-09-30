package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestBoardCmd_HelpAndFlags(t *testing.T) {
	buf := new(bytes.Buffer)
	boardCmd.SetOut(buf)
	boardCmd.SetErr(buf)

	err := boardCmd.Help()
	if err != nil {
		t.Fatalf("board --help failed: %v", err)
	}

	output := buf.String()
	requiredSnippets := []string{
		"interactive Kanban board TUI",
		"4 Kanban columns (todo, in_progress, in_review, done)",
		"--db",
		"--daemon-url",
		"--token",
		"--standalone",
	}

	for _, snippet := range requiredSnippets {
		if !strings.Contains(output, snippet) {
			t.Errorf("board --help output missing required snippet %q\nFull output:\n%s", snippet, output)
		}
	}
}
