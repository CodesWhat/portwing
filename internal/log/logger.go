// SPDX-License-Identifier: MIT

package log

import (
	"log/slog"
	"os"
	"strings"
	"unicode"
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

// SanitizeField makes an untrusted value safe to log as a single field: line
// breaks stay visible as escapes, other control characters are dropped, and the
// result is cut to at most maxRunes runes with an ellipsis marking the cut.
func SanitizeField(value string, maxRunes int) string {
	value = Sanitize(value)
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	if maxRunes > 0 {
		if runes := []rune(value); len(runes) > maxRunes {
			return string(runes[:maxRunes]) + "..."
		}
	}
	return value
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
