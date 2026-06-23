package audit

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestRingPushAndOrder verifies that records come back newest-first.
func TestRingPushAndOrder(t *testing.T) {
	t.Parallel()

	rb := newRing(4)
	for i := 0; i < 3; i++ {
		rb.push(Record{Event: string(rune('A' + i))})
	}

	got := rb.records(0)
	if len(got) != 3 {
		t.Fatalf("expected 3 records, got %d", len(got))
	}
	// Newest-first: C, B, A
	want := []string{"C", "B", "A"}
	for i, r := range got {
		if r.Event != want[i] {
			t.Errorf("records[%d].Event = %q, want %q", i, r.Event, want[i])
		}
	}
}

// TestRingOverwrite verifies that oldest entries are dropped when the buffer is full.
func TestRingOverwrite(t *testing.T) {
	t.Parallel()

	rb := newRing(3)
	// Push 5 events; only last 3 should remain.
	for i := 0; i < 5; i++ {
		rb.push(Record{Event: string(rune('A' + i))})
	}

	got := rb.records(0)
	if len(got) != 3 {
		t.Fatalf("expected 3 records (capacity), got %d", len(got))
	}
	// Newest-first: E, D, C; A and B were overwritten.
	want := []string{"E", "D", "C"}
	for i, r := range got {
		if r.Event != want[i] {
			t.Errorf("records[%d].Event = %q, want %q", i, r.Event, want[i])
		}
	}
}

// TestRingLimitCapping verifies that limit caps the returned slice.
func TestRingLimitCapping(t *testing.T) {
	t.Parallel()

	rb := newRing(10)
	for i := 0; i < 6; i++ {
		rb.push(Record{Event: string(rune('A' + i))})
	}

	got := rb.records(2)
	if len(got) != 2 {
		t.Fatalf("expected 2 records with limit=2, got %d", len(got))
	}
	// Should be the two newest: F, E
	if got[0].Event != "F" || got[1].Event != "E" {
		t.Errorf("unexpected records: %v", got)
	}
}

// TestRingEmptyBuffer verifies that an empty ring returns an empty slice.
func TestRingEmptyBuffer(t *testing.T) {
	t.Parallel()

	rb := newRing(8)
	got := rb.records(0)
	if len(got) != 0 {
		t.Fatalf("expected 0 records from empty ring, got %d", len(got))
	}
}

// TestLoggerBufferWithoutSink verifies that events are captured in the buffer
// even when no slog sink is configured.
func TestLoggerBufferWithoutSink(t *testing.T) {
	t.Parallel()

	l, cleanup, err := New("", 4)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()

	if l.Enabled() {
		t.Error("Enabled() should be false when dest is empty")
	}

	l.AuthFailure("1.2.3.4", "GET", "/secret")
	l.RateLimited("1.2.3.4", "POST", "/api")
	l.ExecStart("1.2.3.4", "/exec/abc/start", "abc123")

	recs := l.Records(0)
	if len(recs) != 3 {
		t.Fatalf("expected 3 buffered records, got %d", len(recs))
	}
	// Newest-first: ExecStart, RateLimited, AuthFailure
	if recs[0].Event != EventExecStart {
		t.Errorf("records[0].Event = %q, want %q", recs[0].Event, EventExecStart)
	}
	if recs[1].Event != EventRateLimited {
		t.Errorf("records[1].Event = %q, want %q", recs[1].Event, EventRateLimited)
	}
	if recs[2].Event != EventAuthFailure {
		t.Errorf("records[2].Event = %q, want %q", recs[2].Event, EventAuthFailure)
	}
}

// TestLoggerDisabledBuffer verifies that Records returns empty slice when
// bufferSize is 0.
func TestLoggerDisabledBuffer(t *testing.T) {
	t.Parallel()

	l, cleanup, err := New("", 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()

	l.AuthFailure("x", "GET", "/y")
	l.APIRequest("x", "GET", "/y", OutcomeAllowed, 200, 1.0)

	recs := l.Records(0)
	if len(recs) != 0 {
		t.Errorf("expected 0 records from disabled buffer, got %d", len(recs))
	}
}

// TestRecordJSONMarshal verifies JSON field names and omitempty behavior.
func TestRecordJSONMarshal(t *testing.T) {
	t.Parallel()

	ts := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	r := Record{
		Time:  ts,
		Event: EventAuthFailure,
		Actor: "10.0.0.1",
	}

	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	s := string(b)

	if !strings.Contains(s, `"ts"`) {
		t.Errorf("expected ts field, got: %s", s)
	}
	if !strings.Contains(s, `"event"`) {
		t.Errorf("expected event field, got: %s", s)
	}
	if !strings.Contains(s, `"actor"`) {
		t.Errorf("expected actor field, got: %s", s)
	}
	// omitempty fields with zero values must not appear
	for _, absent := range []string{`"method"`, `"path"`, `"outcome"`, `"status"`, `"duration_ms"`, `"operation"`, `"stack"`, `"container"`, `"exec_id"`, `"key_id"`} {
		if strings.Contains(s, absent) {
			t.Errorf("expected %q to be omitted, got: %s", absent, s)
		}
	}
}
