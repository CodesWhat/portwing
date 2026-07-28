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

func TestSanitizeEscapesLineBreaks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "unchanged", input: "safe value", want: "safe value"},
		{name: "line feed", input: "first\nsecond", want: `first\nsecond`},
		{name: "carriage return", input: "first\rsecond", want: `first\rsecond`},
		{name: "windows line ending", input: "first\r\nsecond", want: `first\r\nsecond`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Sanitize(tt.input); got != tt.want {
				t.Fatalf("Sanitize(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
