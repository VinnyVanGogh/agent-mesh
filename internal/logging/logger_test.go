package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestSetupLoggerLevels(t *testing.T) {
	tests := []struct {
		name          string
		levelStr      string
		logFunc       func(l *slog.Logger)
		shouldContain bool
		expectedMsg   string
	}{
		{
			name:     "DEBUG level logs DEBUG messages",
			levelStr: "DEBUG",
			logFunc: func(l *slog.Logger) {
				l.Debug("debug message")
			},
			shouldContain: true,
			expectedMsg:   "debug message",
		},
		{
			name:     "INFO level suppresses DEBUG messages",
			levelStr: "INFO",
			logFunc: func(l *slog.Logger) {
				l.Debug("debug message")
			},
			shouldContain: false,
			expectedMsg:   "debug message",
		},
		{
			name:     "INFO level logs INFO messages",
			levelStr: "info",
			logFunc: func(l *slog.Logger) {
				l.Info("info message")
			},
			shouldContain: true,
			expectedMsg:   "info message",
		},
		{
			name:     "WARN level suppresses INFO messages",
			levelStr: "WARN",
			logFunc: func(l *slog.Logger) {
				l.Info("info message")
			},
			shouldContain: false,
			expectedMsg:   "info message",
		},
		{
			name:     "WARN level logs WARN messages",
			levelStr: "warn",
			logFunc: func(l *slog.Logger) {
				l.Warn("warning message")
			},
			shouldContain: true,
			expectedMsg:   "warning message",
		},
		{
			name:     "ERROR level suppresses WARN messages",
			levelStr: "ERROR",
			logFunc: func(l *slog.Logger) {
				l.Warn("warning message")
			},
			shouldContain: false,
			expectedMsg:   "warning message",
		},
		{
			name:     "ERROR level logs ERROR messages",
			levelStr: "error",
			logFunc: func(l *slog.Logger) {
				l.Error("error message")
			},
			shouldContain: true,
			expectedMsg:   "error message",
		},
		{
			name:     "Default level falls back to INFO for unknown strings",
			levelStr: "unknown_level",
			logFunc: func(l *slog.Logger) {
				l.Debug("suppressed debug")
				l.Info("visible info")
			},
			shouldContain: true,
			expectedMsg:   "visible info",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := SetupLogger(tt.levelStr, "text", &buf)
			tt.logFunc(logger)
			output := buf.String()

			contains := strings.Contains(output, tt.expectedMsg)
			if contains != tt.shouldContain {
				t.Fatalf("level %s: expected contains=%v for %q, got: %q", tt.levelStr, tt.shouldContain, tt.expectedMsg, output)
			}
		})
	}
}

func TestSetupLoggerJSONFormat(t *testing.T) {
	var buf bytes.Buffer
	logger := SetupLogger("DEBUG", "json", &buf)

	logger.Info("user logged in", slog.String("user", "alice"), slog.Int("attempts", 2))

	var parsed map[string]interface{}
	err := json.Unmarshal(buf.Bytes(), &parsed)
	if err != nil {
		t.Fatalf("expected valid JSON output, got error: %v, raw: %q", err, buf.String())
	}

	if parsed["msg"] != "user logged in" {
		t.Errorf("expected msg 'user logged in', got %v", parsed["msg"])
	}
	if parsed["level"] != "INFO" {
		t.Errorf("expected level 'INFO', got %v", parsed["level"])
	}
	if parsed["user"] != "alice" {
		t.Errorf("expected user 'alice', got %v", parsed["user"])
	}
	if parsed["attempts"] != float64(2) {
		t.Errorf("expected attempts 2, got %v", parsed["attempts"])
	}
}

func TestSetupLoggerTextFormat(t *testing.T) {
	var buf bytes.Buffer
	logger := SetupLogger("INFO", "text", &buf)

	logger.Info("system ready", slog.String("service", "mesh"))
	output := buf.String()

	if !strings.Contains(output, "level=INFO") {
		t.Errorf("expected level=INFO in text output, got: %s", output)
	}
	if !strings.Contains(output, `msg="system ready"`) && !strings.Contains(output, "msg=system ready") {
		t.Errorf("expected msg in text output, got: %s", output)
	}
	if !strings.Contains(output, "service=mesh") {
		t.Errorf("expected service=mesh in text output, got: %s", output)
	}
}

func TestSetupLoggerSetDefault(t *testing.T) {
	var buf bytes.Buffer
	_ = SetupLogger("INFO", "text", &buf)

	slog.Info("default logger test", slog.String("key", "val"))
	output := buf.String()

	if !strings.Contains(output, "default logger test") || !strings.Contains(output, "key=val") {
		t.Errorf("expected slog.Default to write to buffer, got: %s", output)
	}
}
