package mcp

import (
	"bytes"
	"encoding/json"
	"testing"
)

var envelopeParityCases = []string{
	`"abc"`, `""`, `"a\"b"`, `"é"`, "\"\xff\"", `" "`,
	`0`, `1`, `9`, `-1`, `-9`, `-0`, `1.5`, `-1.5`, `1e5`, `1E+5`, `1.5e-3`, `0.0`,
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
		if err := envelopeParityError(raw); err != nil {
			t.Error(err)
		}
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
