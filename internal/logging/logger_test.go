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

func TestSetupLoggerRedactsSecrets(t *testing.T) {
	const key = "sk-ant-api03-Xk9fQ2mZ7vL0aB3cD4eF5gH6iJ7kL8mN9oP0qR1sT2uV3wX4yZ5aB6cD7eF8gH9iJ0"
	for _, format := range []string{"json", "text"} {
		var buf bytes.Buffer
		l := SetupLogger("info", format, &buf)
		l.Info("auth failed", slog.String("token", key), slog.String("hdr", "Bearer abcdef1234567890"))
		out := buf.String()
		if strings.Contains(out, key[:30]) || strings.Contains(out, "abcdef1234567890") {
			t.Errorf("%s: secret leaked: %s", format, out)
		}
		if !strings.Contains(out, "REDACTED") {
			t.Errorf("%s: no redaction marker: %s", format, out)
		}
	}
}

func TestWithComponent(t *testing.T) {
	var buf bytes.Buffer
	base := SetupLogger("INFO", "json", &buf)
	l := WithComponent(base, "harness")
	l.Info("test message")

	var parsed map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("expected valid JSON, got error: %v, raw: %q", err, buf.String())
	}
	if parsed["component"] != "harness" {
		t.Errorf("expected component=harness, got %v", parsed["component"])
	}
}

func TestWithRunContext(t *testing.T) {
	var buf bytes.Buffer
	base := SetupLogger("INFO", "json", &buf)
	l := WithRunContext(base, "task-123", "run-456", "claude")
	l.Info("run started")

	var parsed map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("expected valid JSON, got error: %v, raw: %q", err, buf.String())
	}
	if parsed["task_id"] != "task-123" {
		t.Errorf("expected task_id=task-123, got %v", parsed["task_id"])
	}
	if parsed["run_id"] != "run-456" {
		t.Errorf("expected run_id=run-456, got %v", parsed["run_id"])
	}
	if parsed["agent"] != "claude" {
		t.Errorf("expected agent=claude, got %v", parsed["agent"])
	}
}

func TestWithComponent_InheritsParentAttrs(t *testing.T) {
	var buf bytes.Buffer
	base := SetupLogger("INFO", "json", &buf)
	// Set an attr on the parent before calling WithComponent.
	parent := base.With(slog.String("env", "production"))
	l := WithComponent(parent, "server")
	l.Info("serving")

	var parsed map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("expected valid JSON, got error: %v, raw: %q", err, buf.String())
	}
	if parsed["component"] != "server" {
		t.Errorf("expected component=server, got %v", parsed["component"])
	}
	if parsed["env"] != "production" {
		t.Errorf("expected env=production inherited from parent, got %v", parsed["env"])
	}
}

func TestWithRunContext_InheritsParentAttrs(t *testing.T) {
	var buf bytes.Buffer
	base := SetupLogger("INFO", "json", &buf)
	parent := base.With(slog.String("env", "staging"))
	l := WithRunContext(parent, "task-789", "run-999", "gemini")
	l.Info("run check")

	var parsed map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("expected valid JSON, got error: %v, raw: %q", err, buf.String())
	}
	if parsed["task_id"] != "task-789" {
		t.Errorf("expected task_id=task-789, got %v", parsed["task_id"])
	}
	if parsed["env"] != "staging" {
		t.Errorf("expected env=staging inherited from parent, got %v", parsed["env"])
	}
}

func TestWithRunContext_CombinedWithComponent(t *testing.T) {
	var buf bytes.Buffer
	base := SetupLogger("INFO", "json", &buf)
	l := WithComponent(base, "harness")
	l = WithRunContext(l, "task-abc", "run-def", "claude")
	l.Info("harness run event")

	var parsed map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("expected valid JSON, got error: %v, raw: %q", err, buf.String())
	}
	if parsed["component"] != "harness" {
		t.Errorf("expected component=harness, got %v", parsed["component"])
	}
	if parsed["task_id"] != "task-abc" {
		t.Errorf("expected task_id=task-abc, got %v", parsed["task_id"])
	}
	if parsed["run_id"] != "run-def" {
		t.Errorf("expected run_id=run-def, got %v", parsed["run_id"])
	}
	if parsed["agent"] != "claude" {
		t.Errorf("expected agent=claude, got %v", parsed["agent"])
	}
}
