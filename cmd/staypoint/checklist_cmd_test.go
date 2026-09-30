package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestChecklistCmd_HelpAndFlags(t *testing.T) {
	buf := new(bytes.Buffer)
	checklistCmd.SetOut(buf)
	checklistCmd.SetErr(buf)

	err := checklistCmd.Help()
	if err != nil {
		t.Fatalf("checklist --help failed: %v", err)
	}

	output := buf.String()
	requiredSnippets := []string{
		"checklist",
		"verify",
	}

	for _, snippet := range requiredSnippets {
		if !strings.Contains(output, snippet) {
			t.Errorf("checklist --help output missing required snippet %q\nFull output:\n%s", snippet, output)
		}
	}

	// Test verify subcommand
	buf.Reset()
	checklistVerifyCmd.SetOut(buf)
	checklistVerifyCmd.SetErr(buf)

	err = checklistVerifyCmd.Help()
	if err != nil {
		t.Fatalf("checklist verify --help failed: %v", err)
	}

	verifyOutput := buf.String()
	verifySnippets := []string{
		"--sprint",
		"--repo-dir",
		"--downgrade",
		"--notify-paperclip",
	}

	for _, snippet := range verifySnippets {
		if !strings.Contains(verifyOutput, snippet) {
			t.Errorf("checklist verify --help output missing required snippet %q\nFull output:\n%s", snippet, verifyOutput)
		}
	}
}
