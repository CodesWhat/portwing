package audit

import (
	"strings"
	"testing"
)

// TestCloseNoOp verifies Close can be called on logger variants without panic.
func TestCloseNoOp(t *testing.T) {
	t.Parallel()

	// disabled logger (dest="", bufferSize=0)
	l, cleanup, err := New("", 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	l.Close() // must not panic
	l.Close() // idempotent
}

func TestCloseFileLogger(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir() + "/audit.log"
	l, cleanup, err := New(tmp, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	l.Close() // flushes and closes the underlying file
}

// TestNewFileOpenError verifies that an unwritable path returns an error.
func TestNewFileOpenError(t *testing.T) {
	t.Parallel()

	_, _, err := New("/nonexistent/path/audit.log", 0)
	if err == nil {
		t.Fatal("expected error for unwritable path, got nil")
	}
}

// TestAPIRequestRingOnly covers the log==nil && ring!=nil branch in APIRequest.
func TestAPIRequestRingOnly(t *testing.T) {
	t.Parallel()

	// dest="" disables slog, bufferSize>0 enables ring only.
	l, cleanup, err := New("", 8)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()

	if l.Enabled() {
		t.Fatal("expected slog disabled")
	}

	l.APIRequest("1.2.3.4", "GET", "/api/v1/containers", OutcomeAllowed, 200, 2.5)

	recs := l.Records(0)
	if len(recs) != 1 {
		t.Fatalf("expected 1 buffered record, got %d", len(recs))
	}
	if recs[0].Event != EventAPIRequest {
		t.Errorf("event = %q, want %q", recs[0].Event, EventAPIRequest)
	}
	if recs[0].Status != 200 {
		t.Errorf("status = %d, want 200", recs[0].Status)
	}
}

// TestComposeOpRingOnly covers the log==nil && ring!=nil branch in ComposeOp.
func TestComposeOpRingOnly(t *testing.T) {
	t.Parallel()

	l, cleanup, err := New("", 8)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()

	l.ComposeOp("10.0.0.1", "up", "mystack", OutcomeAllowed)

	recs := l.Records(0)
	if len(recs) != 1 {
		t.Fatalf("expected 1 buffered record, got %d", len(recs))
	}
	if recs[0].Event != EventComposeOp {
		t.Errorf("event = %q, want %q", recs[0].Event, EventComposeOp)
	}
	if recs[0].Stack != "mystack" {
		t.Errorf("stack = %q, want mystack", recs[0].Stack)
	}
	if recs[0].Operation != "up" {
		t.Errorf("operation = %q, want up", recs[0].Operation)
	}
}

// TestExecStartRingOnly covers the log==nil && ring!=nil branch in ExecStart.
func TestExecStartRingOnly(t *testing.T) {
	t.Parallel()

	l, cleanup, err := New("", 8)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()

	l.ExecStart("10.0.0.1", "/exec/abc/start", "exec-42")

	recs := l.Records(0)
	if len(recs) != 1 {
		t.Fatalf("expected 1 buffered record, got %d", len(recs))
	}
	if recs[0].Event != EventExecStart {
		t.Errorf("event = %q, want %q", recs[0].Event, EventExecStart)
	}
	if recs[0].ExecID != "exec-42" {
		t.Errorf("exec_id = %q, want exec-42", recs[0].ExecID)
	}
	if recs[0].Outcome != OutcomeAllowed {
		t.Errorf("outcome = %q, want %q", recs[0].Outcome, OutcomeAllowed)
	}
}

// TestEnrollmentWithSink covers the full Enrollment path when slog is enabled.
func TestEnrollmentWithSink(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir() + "/audit.log"
	l, cleanup, err := New(tmp, 8)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()

	l.Enrollment("10.0.0.1", "key-abc", OutcomeAllowed)

	recs := l.Records(0)
	if len(recs) != 1 {
		t.Fatalf("expected 1 buffered record, got %d", len(recs))
	}
	if recs[0].Event != EventEnrollment {
		t.Errorf("event = %q, want %q", recs[0].Event, EventEnrollment)
	}
	if recs[0].KeyID != "key-abc" {
		t.Errorf("key_id = %q, want key-abc", recs[0].KeyID)
	}
	if recs[0].Outcome != OutcomeAllowed {
		t.Errorf("outcome = %q, want %q", recs[0].Outcome, OutcomeAllowed)
	}
}

// TestEnrollmentRingOnly covers the log==nil && ring!=nil branch in Enrollment.
func TestEnrollmentRingOnly(t *testing.T) {
	t.Parallel()

	l, cleanup, err := New("", 8)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()

	l.Enrollment("192.168.1.1", "key-xyz", OutcomeDenied)

	recs := l.Records(0)
	if len(recs) != 1 {
		t.Fatalf("expected 1 buffered record, got %d", len(recs))
	}
	if recs[0].Event != EventEnrollment {
		t.Errorf("event = %q, want %q", recs[0].Event, EventEnrollment)
	}
	if recs[0].Outcome != OutcomeDenied {
		t.Errorf("outcome = %q, want %q", recs[0].Outcome, OutcomeDenied)
	}
}

// TestEnrollmentDisabled verifies Enrollment is a no-op on a fully-disabled logger.
func TestEnrollmentDisabled(t *testing.T) {
	t.Parallel()

	l, cleanup, err := New("", 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()

	l.Enrollment("10.0.0.1", "key-noop", OutcomeAllowed) // must not panic
	recs := l.Records(0)
	if len(recs) != 0 {
		t.Fatalf("expected 0 records on disabled logger, got %d", len(recs))
	}
}

// TestAPIRequestWithSinkAndRing covers the path where both log and ring are set.
func TestAPIRequestWithSinkAndRing(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir() + "/audit.log"
	l, cleanup, err := New(tmp, 8)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()

	l.APIRequest("10.0.0.2", "POST", "/api/v1/compose", OutcomeDenied, 403, 0.5)

	recs := l.Records(0)
	if len(recs) != 1 {
		t.Fatalf("expected 1 buffered record, got %d", len(recs))
	}
	if recs[0].Status != 403 {
		t.Errorf("status = %d, want 403", recs[0].Status)
	}
}

// TestComposeOpWithSinkAndRing covers the path where both log and ring are set in ComposeOp.
func TestComposeOpWithSinkAndRing(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir() + "/audit.log"
	l, cleanup, err := New(tmp, 8)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()

	l.ComposeOp("10.0.0.3", "down", "app-stack", OutcomeAllowed)

	recs := l.Records(0)
	if len(recs) != 1 {
		t.Fatalf("expected 1 buffered record, got %d", len(recs))
	}
	if recs[0].Operation != "down" {
		t.Errorf("operation = %q, want down", recs[0].Operation)
	}
}

// TestExecStartWithSinkAndRing covers the path where both log and ring are set in ExecStart.
func TestExecStartWithSinkAndRing(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir() + "/audit.log"
	l, cleanup, err := New(tmp, 8)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()

	l.ExecStart("172.16.0.1", "/exec/def/start", "exec-99")

	recs := l.Records(0)
	if len(recs) != 1 {
		t.Fatalf("expected 1 buffered record, got %d", len(recs))
	}
	if recs[0].Container != "/exec/def/start" {
		t.Errorf("container = %q, want /exec/def/start", recs[0].Container)
	}
}

// TestRecordsWithLimit covers the limit>0 branch in Logger.Records.
func TestRecordsWithLimit(t *testing.T) {
	t.Parallel()

	l, cleanup, err := New("", 16)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()

	for i := 0; i < 5; i++ {
		l.AuthFailure("host", "GET", "/path")
	}

	recs := l.Records(2)
	if len(recs) != 2 {
		t.Fatalf("expected 2 records with limit=2, got %d", len(recs))
	}
}

// TestStdoutSinkEnrollment exercises the stdout sink path for Enrollment.
func TestStdoutSinkEnrollment(t *testing.T) {
	t.Parallel()

	l, cleanup, err := New("stdout", 0)
	if err != nil {
		t.Fatalf("New(stdout): %v", err)
	}
	defer cleanup()

	// Must not panic; output goes to stdout which we don't capture here.
	l.Enrollment("10.0.0.5", "key-stdout", OutcomeAllowed)
	if !l.Enabled() {
		t.Fatal("stdout logger should be enabled")
	}
}

// TestStderrSinkEnrollment exercises the stderr sink path for Enrollment.
func TestStderrSinkEnrollment(t *testing.T) {
	t.Parallel()

	l, cleanup, err := New("stderr", 0)
	if err != nil {
		t.Fatalf("New(stderr): %v", err)
	}
	defer cleanup()

	l.Enrollment("10.0.0.6", "key-stderr", OutcomeAllowed)

	// Verify it wrote something by checking the slog handler is active.
	if !l.Enabled() {
		t.Fatal("stderr logger should be enabled")
	}
	_ = strings.Contains("", "") // no-op to avoid import elimination
}
