// SPDX-License-Identifier: MIT

package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureLogger returns a Logger writing to an in-memory buffer for testing.
// It reuses the file-sink path via a temp file so the full code path is tested.
func captureLogger(t *testing.T) (*Logger, func() string) {
	t.Helper()
	tmp := filepath.Join(t.TempDir(), "audit.log")
	l, close, err := New(tmp, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(close)

	read := func() string {
		data, err := os.ReadFile(tmp)
		if err != nil {
			t.Fatalf("read log: %v", err)
		}
		return string(data)
	}
	return l, read
}

func decodeEvent(t *testing.T, line string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &m); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", line, err)
	}
	return m
}

func TestDisabledEmitsNothing(t *testing.T) {
	t.Parallel()
	l, _, err := New("", 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if l.Enabled() {
		t.Fatal("empty dest should be disabled")
	}
	// Calling methods on a disabled logger must not panic and must produce nothing.
	l.APIRequest("1.2.3.4", "GET", "/foo", OutcomeAllowed, 200, 1.5)
	l.AuthFailure("1.2.3.4", "GET", "/foo")
	l.RateLimited("1.2.3.4", "GET", "/foo")
	l.ComposeOp("1.2.3.4", "up", "mystack", OutcomeAllowed)
	l.ExecStart("1.2.3.4", "/exec/abc/start", "abc", OutcomeAllowed)
}

func TestAPIRequestAllowed(t *testing.T) {
	t.Parallel()
	l, read := captureLogger(t)

	l.APIRequest("10.0.0.1", "GET", "/_portwing/info", OutcomeAllowed, 200, 3.14)

	line := read()
	if line == "" {
		t.Fatal("expected a log line, got empty")
	}
	m := decodeEvent(t, line)
	if m["event"] != EventAPIRequest {
		t.Errorf("event = %q, want %q", m["event"], EventAPIRequest)
	}
	if m["outcome"] != OutcomeAllowed {
		t.Errorf("outcome = %q, want %q", m["outcome"], OutcomeAllowed)
	}
	if m["actor"] != "10.0.0.1" {
		t.Errorf("actor = %q, want 10.0.0.1", m["actor"])
	}
	if m["status"].(float64) != 200 {
		t.Errorf("status = %v, want 200", m["status"])
	}
}

func TestAuthFailureEvent(t *testing.T) {
	t.Parallel()
	l, read := captureLogger(t)

	l.AuthFailure("192.0.2.5", "GET", "/_portwing/info")

	m := decodeEvent(t, read())
	if m["event"] != EventAuthFailure {
		t.Errorf("event = %q, want %q", m["event"], EventAuthFailure)
	}
	if m["outcome"] != OutcomeDenied {
		t.Errorf("outcome = %q, want %q", m["outcome"], OutcomeDenied)
	}
}

func TestRateLimitedEvent(t *testing.T) {
	t.Parallel()
	l, read := captureLogger(t)

	l.RateLimited("192.0.2.9", "POST", "/_portwing/compose")

	m := decodeEvent(t, read())
	if m["event"] != EventRateLimited {
		t.Errorf("event = %q, want %q", m["event"], EventRateLimited)
	}
	if m["outcome"] != OutcomeDenied {
		t.Errorf("outcome = %q, want %q", m["outcome"], OutcomeDenied)
	}
}

func TestComposeOpEvent(t *testing.T) {
	t.Parallel()
	l, read := captureLogger(t)

	l.ComposeOp("10.1.2.3", "up", "nginx-stack", OutcomeAllowed)

	m := decodeEvent(t, read())
	if m["event"] != EventComposeOp {
		t.Errorf("event = %q, want %q", m["event"], EventComposeOp)
	}
	if m["operation"] != "up" {
		t.Errorf("operation = %q, want up", m["operation"])
	}
	if m["stack"] != "nginx-stack" {
		t.Errorf("stack = %q, want nginx-stack", m["stack"])
	}
}

func TestKeyRotationEvent(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		sink bool
		size int
	}{
		{name: "disabled"},
		{name: "sink", sink: true},
		{name: "buffer", size: 2},
		{name: "sink and buffer", sink: true, size: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dest := ""
			if tc.sink {
				dest = filepath.Join(t.TempDir(), "rotation.log")
			}
			l, closeLog, err := New(dest, tc.size)
			if err != nil {
				t.Fatal(err)
			}
			defer closeLog()
			l.KeyRotation("192.0.2.1", "commit", "public-key-id", OutcomeAllowed)
			if tc.sink {
				data, err := os.ReadFile(dest)
				if err != nil {
					t.Fatal(err)
				}
				event := decodeEvent(t, string(data))
				for field, want := range map[string]string{
					"event": EventKeyRotation, "actor": "192.0.2.1", "operation": "commit",
					"key_id": "public-key-id", "outcome": OutcomeAllowed,
				} {
					if event[field] != want {
						t.Errorf("%s = %v, want %s", field, event[field], want)
					}
				}
				if len(event) != 8 {
					t.Errorf("unexpected audit fields: %v", event)
				}
			}
			records := l.Records(0)
			if tc.size == 0 {
				if len(records) != 0 {
					t.Fatalf("disabled buffer retained %d records", len(records))
				}
				return
			}
			if len(records) != 1 {
				t.Fatalf("records = %d, want 1", len(records))
			}
			r := records[0]
			if r.Event != EventKeyRotation || r.Actor != "192.0.2.1" || r.Operation != "commit" ||
				r.KeyID != "public-key-id" || r.Outcome != OutcomeAllowed || r.Time.IsZero() {
				t.Errorf("unexpected rotation record: %+v", r)
			}
		})
	}
}

func TestFileSinkPermissions(t *testing.T) {
	t.Parallel()
	tmp := filepath.Join(t.TempDir(), "audit.log")
	l, close, err := New(tmp, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer close()

	l.AuthFailure("1.2.3.4", "GET", "/")

	info, err := os.Stat(tmp)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file perm = %o, want 0600", perm)
	}
}

func TestStdoutSink(t *testing.T) {
	t.Parallel()
	l, close, err := New("stdout", 0)
	if err != nil {
		t.Fatalf("New(stdout): %v", err)
	}
	defer close()
	if !l.Enabled() {
		t.Fatal("stdout sink should be enabled")
	}
}

func TestStderrSink(t *testing.T) {
	t.Parallel()
	l, close, err := New("stderr", 0)
	if err != nil {
		t.Fatalf("New(stderr): %v", err)
	}
	defer close()
	if !l.Enabled() {
		t.Fatal("stderr sink should be enabled")
	}
}
