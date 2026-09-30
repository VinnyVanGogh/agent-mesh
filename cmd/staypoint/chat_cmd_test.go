package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestChatCmd_HelpAndFlags(t *testing.T) {
	buf := new(bytes.Buffer)
	chatCmd.SetOut(buf)
	chatCmd.SetErr(buf)

	err := chatCmd.Help()
	if err != nil {
		t.Fatalf("chat --help failed: %v", err)
	}

	output := buf.String()
	requiredSnippets := []string{
		"interactive full-screen Bubble Tea chat session",
		"--session",
		"--model",
		"--task",
		"--socket",
		"--in-process",
	}

	for _, snippet := range requiredSnippets {
		if !strings.Contains(output, snippet) {
			t.Errorf("chat --help output missing required snippet %q\nFull output:\n%s", snippet, output)
		}
	}
}
