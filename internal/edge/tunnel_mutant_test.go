package edge

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/protocol"
)

// ---------------------------------------------------------------------------
// bringUpExec — initial resize is gated by both Cols>0 and Rows>0
// (tunnel.go:191)
// ---------------------------------------------------------------------------

// TestBringUpExecInitialResizeGatedByColsAndRows proves the exact boundary of
// `msg.Cols > 0 && msg.Rows > 0`: a zero on either side must suppress the
// initial ResizeExec call, and positive values on both sides must trigger it.
// CONDITIONALS_BOUNDARY (`>` -> `>=`) on either operand would call ResizeExec
// with a zero dimension.
func TestBringUpExecInitialResizeGatedByColsAndRows(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		cols, rows int
		wantResize bool
	}{
		{"cols zero", 0, 24, false},
		{"rows zero", 80, 0, false},
		{"both positive", 80, 24, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, ctrl := newTestClient(t)
			execConn := &fakeConn{blockRead: make(chan struct{})}
			fd := &fakeDocker{
				createExecID: "docker-" + tc.name,
				startConn:    execConn,
			}
			c.dockerClient = fd

			c.StartExec(context.Background(), protocol.ExecStartMessage{
				ExecID:      "rz-" + tc.name,
				ContainerID: "c1",
				Cmd:         []string{"sh"},
				Cols:        tc.cols,
				Rows:        tc.rows,
			})

			// exec_ready always arrives (the resize is best-effort and never
			// blocks bring-up); it's a reliable barrier proving bringUpExec has
			// already decided whether to call ResizeExec.
			expectType(t, ctrl, protocol.TypeExecReady)

			fd.mu.Lock()
			gotResize := len(fd.resizeCalls) > 0
			fd.mu.Unlock()
			if gotResize != tc.wantResize {
				t.Errorf("ResizeExec called = %v, want %v (cols=%d rows=%d)", gotResize, tc.wantResize, tc.cols, tc.rows)
			}

			close(execConn.blockRead)
			expectType(t, ctrl, protocol.TypeExecEnd)
		})
	}
}

// ---------------------------------------------------------------------------
// writeInput — fixed retry budget and backoff (tunnel.go:329, 333, 338)
// ---------------------------------------------------------------------------

// TestWriteInputRetriesExactlyTenTimesThenCloses proves writeInput makes
// exactly 10 write attempts, spaced by real 50ms waits, before giving up and
// closing the session. This pins CONDITIONALS_BOUNDARY (`attempt < 10` ->
// `<= 10`, which would allow an 11th attempt) and the retry-delay
// ARITHMETIC_BASE (`50 * time.Millisecond` -> `/`, which would make every
// retry near-instant instead of ~50ms apart) via the elapsed-time floor.
func TestWriteInputRetriesExactlyTenTimesThenCloses(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	conn := &gatedErrConn{gate: make(chan struct{})}
	close(conn.gate) // never block: every Write fails immediately
	s := newExecSession(c, "wi-retry-count", conn)

	// Safety net: if a mutant makes the retry loop not terminate on its own
	// (e.g. a decrementing attempt counter), force it closed so the test
	// fails on the assertions below instead of hanging the suite.
	safety := time.AfterFunc(5*time.Second, func() { s.Close() })
	defer safety.Stop()

	start := time.Now()
	s.writeInput([]byte("x"))
	elapsed := time.Since(start)

	if got := conn.attempts(); got != 10 {
		t.Errorf("write attempts = %d, want exactly 10", got)
	}
	// 9 inter-attempt waits of ~50ms real delay; a generous floor well below
	// the real ~450ms avoids flakiness while still failing hard against a
	// near-instant mutant.
	if elapsed < 200*time.Millisecond {
		t.Errorf("writeInput returned after %v, want at least ~200ms (9 x 50ms retry waits)", elapsed)
	}
	select {
	case <-s.done:
	default:
		t.Error("session was not closed after exhausting retries")
	}
}

// TestWriteInputRetryLogsOneBasedAttemptNumber covers the ARITHMETIC_BASE on
// the retry log's `attempt+1` (tunnel.go:333): the loop variable is
// zero-based, so the first logged attempt must read 1, not -1 or 0.
func TestWriteInputRetryLogsOneBasedAttemptNumber(t *testing.T) {
	// Deliberately NOT t.Parallel(): captures the process-global slog default.

	c, _ := newTestClient(t)
	conn := &gatedErrConn{gate: make(chan struct{})}
	close(conn.gate)
	s := newExecSession(c, "wi-retry-log", conn)
	defer s.Close()

	logBuf := &bytes.Buffer{}
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(oldLogger)

	// Stop after the first attempt is logged instead of running all 10.
	go func() {
		waitFor(t, "first write attempt", func() bool { return conn.attempts() >= 1 })
		s.Close()
	}()
	s.writeInput([]byte("x"))

	if !strings.Contains(logBuf.String(), "attempt=1") {
		t.Errorf("retry log = %q, want it to contain attempt=1 (1-based)", logBuf.String())
	}
}

// ---------------------------------------------------------------------------
// doResize — fixed retry budget, backoff, and logging (tunnel.go:374, 378, 385)
// ---------------------------------------------------------------------------

// TestDoResizeRetriesExactlyTenTimesThenGivesUp is the doResize counterpart of
// TestWriteInputRetriesExactlyTenTimesThenCloses: it pins the exact 10-attempt
// budget (CONDITIONALS_BOUNDARY, INCREMENT_DECREMENT on the loop) and the
// ~50ms real retry spacing (ARITHMETIC_BASE on tunnel.go:385) via an
// elapsed-time floor. A context far longer than the expected ~450ms real
// runtime bounds the worst case (a decrementing attempt counter looping
// indefinitely) instead of hanging the suite.
func TestDoResizeRetriesExactlyTenTimesThenGivesUp(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	fd := &fakeDocker{resizeFailFirst: 99} // always fails
	c.dockerClient = fd
	s := &ExecSession{execID: "rz-count", client: c, dockerExecID: "docker-rz-count", done: make(chan struct{})}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()
	s.doResize(ctx, resizeOp{cols: 80, rows: 24})
	elapsed := time.Since(start)

	fd.mu.Lock()
	attempts := fd.resizeAttempts
	fd.mu.Unlock()
	if attempts != 10 {
		t.Errorf("ResizeExec attempts = %d, want exactly 10", attempts)
	}
	if elapsed < 200*time.Millisecond {
		t.Errorf("doResize returned after %v, want at least ~200ms (9 x 50ms retry waits)", elapsed)
	}
}

// TestDoResizeRetryLogsOneBasedAttemptNumber covers the ARITHMETIC_BASE on
// doResize's retry log `attempt+1` (tunnel.go:378).
func TestDoResizeRetryLogsOneBasedAttemptNumber(t *testing.T) {
	// Deliberately NOT t.Parallel(): captures the process-global slog default.

	c, _ := newTestClient(t)
	fd := &fakeDocker{resizeFailFirst: 1} // fails once, then succeeds
	c.dockerClient = fd
	s := &ExecSession{execID: "rz-log", client: c, dockerExecID: "docker-rz-log", done: make(chan struct{})}

	logBuf := &bytes.Buffer{}
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(oldLogger)

	s.doResize(context.Background(), resizeOp{cols: 1, rows: 1})

	if !strings.Contains(logBuf.String(), "attempt=1") {
		t.Errorf("retry log = %q, want it to contain attempt=1 (1-based)", logBuf.String())
	}
}

// ---------------------------------------------------------------------------
// recoverSession — panic is logged (tunnel.go:539)
// ---------------------------------------------------------------------------

// TestRecoverSessionLogsRecoveredPanic covers the CONDITIONALS_NEGATION on
// `r != nil` after recover(): recover() itself already stops the panic from
// propagating regardless of what's done with its result, so the log is the
// only observable difference between the real code and the mutant.
func TestRecoverSessionLogsRecoveredPanic(t *testing.T) {
	// Deliberately NOT t.Parallel(): captures the process-global slog default.

	logBuf := &bytes.Buffer{}
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logBuf, nil)))
	defer slog.SetDefault(oldLogger)

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer recoverSession("test-goroutine", "panic-eid")
		panic("boom")
	}()
	<-done

	out := logBuf.String()
	if !strings.Contains(out, "recovered from panic") {
		t.Errorf("log = %q, want the panic-recovery message", out)
	}
	if !strings.Contains(out, "panic-eid") {
		t.Errorf("log = %q, want it to contain the execID", out)
	}
}
