package mcp

import (
	"bytes"
	"encoding/json"
	"testing"
)

// referenceValidRequestID is the decoder-based implementation that
// validRequestID replaced. It stays here as the oracle for the differential
// checks. It agrees with validRequestID on every valid JSON value, which is
// all the handler ever passes (values come out of a parent Unmarshal).
func referenceValidRequestID(raw json.RawMessage) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var id any
	if err := decoder.Decode(&id); err != nil {
		return false
	}
	switch id.(type) {
	case string, json.Number:
		return true
	default:
		return false
	}
}

// referenceMethodName is the any-based method decode that the string-prefix
// check plus direct Unmarshal replaced.
func referenceMethodName(raw json.RawMessage) (string, bool) {
	var method any
	_ = json.Unmarshal(raw, &method)
	name, ok := method.(string)
	return name, ok
}

func methodName(raw json.RawMessage) (string, bool) {
	var name string
	if !isJSONString(raw) || json.Unmarshal(raw, &name) != nil {
		return "", false
	}
	return name, true
}

// assertEnvelopeParity fails when the production checks and their references
// disagree on raw. Callers must pass valid JSON.
func assertEnvelopeParity(t *testing.T, raw json.RawMessage) {
	t.Helper()
	if got, want := validRequestID(raw), referenceValidRequestID(raw); got != want {
		t.Errorf("validRequestID(%q) = %v, reference = %v", raw, got, want)
	}
	gotName, gotOK := methodName(raw)
	wantName, wantOK := referenceMethodName(raw)
	if gotOK != wantOK || gotName != wantName {
		t.Errorf("method(%q) = (%q, %v), reference = (%q, %v)", raw, gotName, gotOK, wantName, wantOK)
	}
}

var envelopeParityCases = []string{
	`"abc"`, `""`, `"a\"b"`, `"é"`, "\"\xff\"", `" "`,
	`0`, `1`, `-1`, `-0`, `1.5`, `-1.5`, `1e5`, `1E+5`, `1.5e-3`, `0.0`,
	`123456789012345678901234567890`,
	`null`, `true`, `false`, `{}`, `{"a":1}`, `[]`, `[1]`,
	` "abc"`, "\t1", "\n\"x\"", "\r\n -1", ` null`, ` true`, ` {}`, ` []`,
	`"abc" `, `1 `,
	``, ` `, "\n",
}

func TestEnvelopeParityTable(t *testing.T) {
	for _, c := range envelopeParityCases {
		raw := json.RawMessage(c)
		// Only valid JSON ever reaches these checks in the handler.
		if len(bytes.TrimSpace(raw)) > 0 && !json.Valid(raw) {
			t.Fatalf("case %q is not valid JSON", c)
		}
		assertEnvelopeParity(t, raw)
	}
}

func TestValidRequestIDAcceptSet(t *testing.T) {
	accept := []string{`"abc"`, `""`, `0`, `-1`, `1.5`, `1e5`, ` "x"`, "\n7"}
	reject := []string{`null`, `true`, `false`, `{}`, `[]`, ``, ` `, ` null`, ` {}`}
	for _, c := range accept {
		if !validRequestID(json.RawMessage(c)) {
			t.Errorf("validRequestID(%q) = false, want true", c)
		}
	}
	for _, c := range reject {
		if validRequestID(json.RawMessage(c)) {
			t.Errorf("validRequestID(%q) = true, want false", c)
		}
	}
}

func TestMethodNameAcceptSet(t *testing.T) {
	if name, ok := methodName(json.RawMessage(`"ping"`)); !ok || name != "ping" {
		t.Errorf("methodName string = (%q, %v)", name, ok)
	}
	if name, ok := methodName(json.RawMessage(` "ping"`)); !ok || name != "ping" {
		t.Errorf("methodName with leading space = (%q, %v)", name, ok)
	}
	for _, c := range []string{`null`, `1`, `true`, `{}`, `[]`, ``, ` null`, `"abc`} {
		if _, ok := methodName(json.RawMessage(c)); ok {
			t.Errorf("methodName(%q) accepted, want rejected", c)
		}
	}
}
