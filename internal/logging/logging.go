package logging

import (
	"log/slog"
	"os"
	"strings"
)

// Level is the global log level, atomically switchable at runtime.
var Level = &slog.LevelVar{}

// Init configures slog with the global LevelVar and a text handler on stdout.
// Call once at startup before any logging.
func Init(levelStr string) {
	Level.Set(ParseLevel(levelStr))
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: Level,
	})
	slog.SetDefault(slog.New(handler))
}

// ParseLevel converts a string to slog.Level. Defaults to INFO.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// LevelString returns the lowercase string name of a level.
func LevelString(l slog.Level) string {
	switch {
	case l <= slog.LevelDebug:
		return "debug"
	case l <= slog.LevelInfo:
		return "info"
	case l <= slog.LevelWarn:
		return "warn"
	default:
		return "error"
	}
}

// PayloadPreview returns the first maxLen bytes of a payload,
// truncated with "..." if longer. Used for DEBUG-level logging.
func PayloadPreview(payload []byte, maxLen int) string {
	if len(payload) <= maxLen {
		return string(payload)
	}
	return string(payload[:maxLen]) + "..."
}
