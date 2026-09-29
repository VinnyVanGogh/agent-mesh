package adapter

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/router"
)

func TestAdapterKeepalive(t *testing.T) {
	// Create a dummy shell script that sleeps for 3 seconds then prints "done"
	script := `#!/bin/sh
sleep 3
echo "done"
`
	f, err := os.CreateTemp("", "dummy-agent-*.sh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString(script)
	f.Close()
	os.Chmod(f.Name(), 0755)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Use custom context value to override the binary path
	ctx = context.WithValue(ctx, "testBin", f.Name())

	var stdout, stderr bytes.Buffer
	pacerState := &router.PacerState{}

	start := time.Now()
	err = RunAdapter(ctx, ".", pacerState, []string{"--model", "google/gemini-3.8-flash"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunAdapter failed: %v", err)
	}
	elapsed := time.Since(start)

	out := stdout.String()
	// Should have emitted at least one keepalive (blank line)
	if !strings.Contains(out, "\n\n") && !strings.HasPrefix(out, "\n") {
		t.Errorf("Expected keepalive newline in output, got %q", out)
	}
	if !strings.Contains(out, "done") {
		t.Errorf("Expected 'done' in output, got %q", out)
	}
	if elapsed < 3*time.Second {
		t.Errorf("Expected script to take at least 3 seconds, took %v", elapsed)
	}
}

func TestAdapterFallback(t *testing.T) {
	// Dummy script that exits with 1 (simulating agy crashing or quota fail)
	failScript := `#!/bin/sh
exit 1
`
	f, err := os.CreateTemp("", "dummy-fail-*.sh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString(failScript)
	f.Close()
	os.Chmod(f.Name(), 0755)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Use custom context value to override the binary path
	ctx = context.WithValue(ctx, "testBin", f.Name())

	var stdout, stderr bytes.Buffer
	pacerState := &router.PacerState{}

	err = RunAdapter(ctx, ".", pacerState, []string{"--model", "google/gemini-3.8-flash"}, &stdout, &stderr)
	// It should return an error because we overrode testBin and test fallback doesn't trigger when testBin is set
	if err == nil {
		t.Errorf("Expected error from failing script")
	}
}
