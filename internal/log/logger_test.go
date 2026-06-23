package log

import (
	"log/slog"
	"testing"
)

func TestSetupLogger(t *testing.T) {
	// SetupLogger reconfigures the global slog default. Just confirm it doesn't panic.
	levels := []string{"debug", "info", "warn", "error", "DEBUG", "INFO", "WARN", "ERROR", ""}
	for _, level := range levels {
		t.Run(level, func(t *testing.T) {
			SetupLogger(level) // must not panic
		})
	}
}

func TestParseLevel(t *testing.T) {
	cases := []struct {
		input string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"WARN", slog.LevelWarn},
		{"error", slog.LevelError},
		{"ERROR", slog.LevelError},
		{"", slog.LevelInfo},        // default
		{"unknown", slog.LevelInfo}, // default fallback
		{"verbose", slog.LevelInfo}, // default fallback
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := parseLevel(tc.input)
			if got != tc.want {
				t.Errorf("parseLevel(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}
