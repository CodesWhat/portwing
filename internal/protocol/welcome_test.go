package protocol

import (
	"encoding/json"
	"testing"
)

// TestWelcomeMessagePollIntervalShapes verifies WelcomeMessage.PollInterval
// decodes correctly whether the wire sends it as a JSON number (the actual
// shape of Drydock's Edge Mode welcome frame) or as a numeric string (a
// shape documented elsewhere in the ecosystem, e.g. Drydock's REST
// AgentInfo surface). Before UnmarshalJSON was added, a numeric-string
// pollInterval failed the whole WelcomeMessage decode.
func TestWelcomeMessagePollIntervalShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		json string
		want int
	}{
		{
			name: "numeric pollInterval (real welcome frame shape)",
			json: `{"pollInterval":300}`,
			want: 300,
		},
		{
			name: "numeric-string pollInterval (compat shape)",
			json: `{"pollInterval":"300"}`,
			want: 300,
		},
		{
			name: "numeric pollInterval with config",
			json: `{"pollInterval":60,"config":{"serverCompatLevel":"1.5.0"}}`,
			want: 60,
		},
		{
			name: "numeric-string pollInterval with config",
			json: `{"pollInterval":"60","config":{"serverCompatLevel":"1.5.0"}}`,
			want: 60,
		},
		{
			name: "missing pollInterval",
			json: `{"config":{"serverCompatLevel":"1.5.0"}}`,
			want: 0,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var w WelcomeMessage
			if err := json.Unmarshal([]byte(tt.json), &w); err != nil {
				t.Fatalf("Unmarshal(%q) returned error: %v", tt.json, err)
			}
			if w.PollInterval != tt.want {
				t.Errorf("PollInterval = %d, want %d", w.PollInterval, tt.want)
			}
		})
	}
}

// TestWelcomeMessagePollIntervalInvalid verifies malformed pollInterval
// shapes fail decoding cleanly (an error, not a panic or silent zero).
func TestWelcomeMessagePollIntervalInvalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		json string
	}{
		{name: "non-numeric string", json: `{"pollInterval":"not-a-number"}`},
		{name: "boolean", json: `{"pollInterval":true}`},
		{name: "object", json: `{"pollInterval":{}}`},
		{name: "array", json: `{"pollInterval":[]}`},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var w WelcomeMessage
			if err := json.Unmarshal([]byte(tt.json), &w); err == nil {
				t.Errorf("Unmarshal(%q) = nil error, want an error", tt.json)
			}
		})
	}
}

// TestWelcomeMessageRoundTrip verifies a WelcomeMessage encoded by this
// package (always a JSON number) decodes back to the same value, so the
// custom UnmarshalJSON doesn't break the common/agent-facing path.
func TestWelcomeMessageRoundTrip(t *testing.T) {
	t.Parallel()

	want := WelcomeMessage{
		PollInterval: 300,
		Config:       map[string]string{"serverCompatLevel": "1.5.0"},
	}

	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got WelcomeMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.PollInterval != want.PollInterval {
		t.Errorf("PollInterval = %d, want %d", got.PollInterval, want.PollInterval)
	}
	if got.Config["serverCompatLevel"] != want.Config["serverCompatLevel"] {
		t.Errorf("Config[serverCompatLevel] = %q, want %q", got.Config["serverCompatLevel"], want.Config["serverCompatLevel"])
	}
}
