package log

import (
	"log/slog"
	"os"
	"strings"
)

// SetupLogger configures the default slog logger with a JSON handler at the given level.
func SetupLogger(level string) {
	lvl := parseLevel(level)
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: lvl,
	})
	slog.SetDefault(slog.New(handler))
}

// Sanitize makes untrusted values safe for both JSON and plain-text log
// handlers while preserving line-break evidence as visible escape sequences.
func Sanitize(value string) string {
	value = strings.ReplaceAll(value, "\n", `\n`)
	return strings.ReplaceAll(value, "\r", `\r`)
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
