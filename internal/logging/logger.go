package logging

import (
	"github.com/VinnyVanGogh/staypoint/internal/security"
	"io"
	"log/slog"
	"os"
	"strings"
)

// SetupLogger configures and returns an slog.Logger instance based on the provided level and format.
// It also sets the logger as the default global logger.
func SetupLogger(level string, format string, writer io.Writer) *slog.Logger {
	if writer == nil {
		writer = os.Stderr
	}

	var slogLevel slog.Level
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "DEBUG":
		slogLevel = slog.LevelDebug
	case "INFO":
		slogLevel = slog.LevelInfo
	case "WARN", "WARNING":
		slogLevel = slog.LevelWarn
	case "ERROR":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}

	// slog emits one complete record per Write, so a stateless per-write redactor suffices.
	writer = redactingWriter{dst: writer}

	opts := &slog.HandlerOptions{
		Level: slogLevel,
	}

	var handler slog.Handler
	if strings.ToLower(strings.TrimSpace(format)) == "json" {
		handler = slog.NewJSONHandler(writer, opts)
	} else {
		handler = slog.NewTextHandler(writer, opts)
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger
}

// redactingWriter scrubs secrets from every log record before it is written.
type redactingWriter struct{ dst io.Writer }

func (w redactingWriter) Write(p []byte) (int, error) {
	if _, err := w.dst.Write(security.RedactBytes(p)); err != nil {
		return 0, err
	}
	return len(p), nil
}
