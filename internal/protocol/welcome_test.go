package protocol

import (
	"encoding/base64"
	"encoding/json"
	"strings"
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

// TestWelcomeMessageCapabilities verifies WelcomeMessage.Capabilities decodes
// the CapResponseBodyBase64 token when a controller advertises it, and comes
// back empty when the frame omits the field entirely (an older controller's
// welcome) — the two shapes hasControllerCap in internal/edge must tell
// apart to negotiate the bodyBase64 response path.
func TestWelcomeMessageCapabilities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		json string
		want []string
	}{
		{
			name: "advertises the response-body-b64 token",
			json: `{"pollInterval":60,"capabilities":["edge-response-body-b64"]}`,
			want: []string{"edge-response-body-b64"},
		},
		{
			name: "older controller omits capabilities entirely",
			json: `{"pollInterval":60}`,
			want: nil,
		},
		{
			name: "empty capabilities array",
			json: `{"pollInterval":60,"capabilities":[]}`,
			want: nil,
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
			if len(w.Capabilities) != len(tt.want) {
				t.Fatalf("Capabilities = %v, want %v", w.Capabilities, tt.want)
			}
			for i, c := range tt.want {
				if w.Capabilities[i] != c {
					t.Errorf("Capabilities[%d] = %q, want %q", i, w.Capabilities[i], c)
				}
			}
		})
	}
}

// TestResponseMessageBodyBase64RoundTrip verifies ResponseMessage.BodyBase64
// carries arbitrary non-JSON bytes across a marshal/unmarshal round trip,
// which is the whole point of the field: unlike Body (json.RawMessage), it
// doesn't require the payload to itself be valid JSON.
func TestResponseMessageBodyBase64RoundTrip(t *testing.T) {
	t.Parallel()

	want := ResponseMessage{
		RequestID:  "r1",
		StatusCode: 200,
		BodyBase64: base64.StdEncoding.EncodeToString([]byte("OK")),
	}

	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), `"body"`) {
		t.Errorf("marshaled envelope %s carries legacy body alongside bodyBase64, want it omitted", data)
	}

	var got ResponseMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(got.BodyBase64)
	if err != nil {
		t.Fatalf("decode BodyBase64: %v", err)
	}
	if string(decoded) != "OK" {
		t.Errorf("decoded body = %q, want %q", decoded, "OK")
	}
}
