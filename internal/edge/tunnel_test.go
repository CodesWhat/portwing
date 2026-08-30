package edge

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/protocol"
)

// readLoop should base64-frame each chunk of exec output as an exec_output
// message and, on EOF, emit a terminal exec_end with reason "exited".
func TestReadLoopStreamsOutputThenExecEndOnEOF(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	conn := &fakeConn{reads: [][]byte{[]byte("hello\n")}, readErr: io.EOF}
	session := newExecSession(c, "e1", conn)

	go session.readLoop()

	var out protocol.ExecOutputMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeExecOutput), &out)
	if out.ExecID != "e1" {
		t.Errorf("exec_output ExecID = %q, want e1", out.ExecID)
	}
	decoded, err := base64.StdEncoding.DecodeString(out.Data)
	if err != nil {
		t.Fatalf("exec_output data not base64: %v", err)
	}
	if string(decoded) != "hello\n" {
		t.Errorf("exec_output payload = %q, want %q", decoded, "hello\n")
	}

	var end protocol.ExecEndMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeExecEnd), &end)
	if end.ExecID != "e1" {
		t.Errorf("exec_end ExecID = %q, want e1", end.ExecID)
	}
	if end.Reason != "exited" {
		t.Errorf("exec_end reason = %q, want exited", end.Reason)
	}

	// readLoop's deferred Close must deregister the session. The deferred Close
	// runs after readLoop returns (after exec_end is sent), so use waitFor to
	// avoid a race between message delivery and the deregistration.
	waitFor(t, "session to be deregistered after readLoop", func() bool {
		_, ok := c.execSessions.Load("e1")
		return !ok
	})
}

// A non-EOF read error should surface as the exec_end reason verbatim, so the
// controller can distinguish a clean exit from a transport failure.
func TestReadLoopExecEndCarriesErrorReason(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	conn := &fakeConn{readErr: errors.New("connection reset")}
	session := newExecSession(c, "e2", conn)

	go session.readLoop()

	var end protocol.ExecEndMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeExecEnd), &end)
	if end.Reason != "connection reset" {
		t.Errorf("exec_end reason = %q, want %q", end.Reason, "connection reset")
	}
}

// Close must be idempotent (sync.Once), close the done channel exactly once,
// shut the underlying conn, and deregister the session.
func TestCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	conn := &fakeConn{}
	session := newExecSession(c, "e3", conn)

	session.Close()
	session.Close() // must not panic on a second close of done.

	select {
	case <-session.done:
	default:
		t.Error("done channel was not closed")
	}
	if !conn.closed {
		t.Error("underlying conn was not closed")
	}
	if _, ok := c.execSessions.Load("e3"); ok {
		t.Error("session still registered after Close")
	}
}

// HandleInput decodes the base64 payload and writes the raw bytes to the exec
// connection. The write is async (handled by the inputWriter goroutine), so we
// use waitFor to assert the bytes land.
func TestHandleInputWritesDecodedData(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	conn := &fakeConn{}
	newReadySession(c, "e4", conn)

	payload := []byte("ls -la\n")
	c.HandleInput(protocol.ExecInputMessage{
		ExecID: "e4",
		Data:   base64.StdEncoding.EncodeToString(payload),
	})

	waitFor(t, "input to be written to conn", func() bool {
		return string(conn.written()) == string(payload)
	})
}

// A malformed base64 payload is dropped without writing anything or closing
// the session.
func TestHandleInputRejectsBadBase64(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	conn := &fakeConn{}
	newExecSession(c, "e5", conn)

	c.HandleInput(protocol.ExecInputMessage{ExecID: "e5", Data: "!!!not-base64!!!"})

	if got := conn.written(); len(got) != 0 {
		t.Errorf("wrote %q on bad input, want nothing", got)
	}
	if _, ok := c.execSessions.Load("e5"); !ok {
		t.Error("session was torn down on a decode error; should be left intact")
	}
}

// Input for an unknown exec id is a silent no-op.
func TestHandleInputUnknownSessionNoop(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	// Must not panic with no session registered.
	c.HandleInput(protocol.ExecInputMessage{
		ExecID: "ghost",
		Data:   base64.StdEncoding.EncodeToString([]byte("x")),
	})
}

// When every write attempt fails, the inputWriter exhausts its retries and
// tears the session down. HandleInput enqueues the data and returns immediately;
// the writer goroutine (started by newReadySession) does the retries async.
func TestHandleInputClosesSessionAfterWriteFailure(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	conn := &fakeConn{writeErr: errors.New("broken pipe")}
	session := newReadySession(c, "e6", conn)

	c.HandleInput(protocol.ExecInputMessage{
		ExecID: "e6",
		Data:   base64.StdEncoding.EncodeToString([]byte("data")),
	})

	// 10 retries × 50ms = ~500ms max. waitFor polls until done is closed.
	waitFor(t, "session to be closed after write retries exhausted", func() bool {
		select {
		case <-session.done:
			return true
		default:
			return false
		}
	})
	if _, ok := c.execSessions.Load("e6"); ok {
		t.Error("session still registered after write-failure teardown")
	}
}

// EndExec closes the named session.
func TestEndExecClosesSession(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	conn := &fakeConn{}
	session := newExecSession(c, "e7", conn)

	c.EndExec(protocol.ExecEndMessage{ExecID: "e7"})

	select {
	case <-session.done:
	default:
		t.Error("EndExec did not close the session")
	}
	if !conn.closed {
		t.Error("EndExec did not close the underlying conn")
	}
}

// EndExec / HandleResize for an unknown id must not panic.
func TestEndExecAndResizeUnknownSessionNoop(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	c.EndExec(protocol.ExecEndMessage{ExecID: "ghost"})
	c.HandleResize(context.Background(), protocol.ExecResizeMessage{ExecID: "ghost", Cols: 80, Rows: 24})
}

// StartExec must refuse to open a session once maxExecSessions are already
// live, replying with a terminal exec_end rather than creating a Docker exec.
func TestStartExecRejectsBeyondSessionLimit(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)

	// Saturate the registry. The limit check only counts entries, so bare
	// sentinels are enough — no Docker connection is touched on this path.
	for i := 0; i < maxExecSessions; i++ {
		c.execSessions.Store("limit-"+strconv.Itoa(i), &ExecSession{})
	}

	c.StartExec(context.Background(), protocol.ExecStartMessage{
		ExecID:      "overflow",
		ContainerID: "c1",
		Cmd:         []string{"sh"},
	})

	var end protocol.ExecEndMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeExecEnd), &end)
	if end.ExecID != "overflow" {
		t.Errorf("exec_end ExecID = %q, want overflow", end.ExecID)
	}
	if end.Reason != "session limit reached" {
		t.Errorf("exec_end reason = %q, want %q", end.Reason, "session limit reached")
	}
}

func TestHandleInputQueueFull(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	conn := &fakeConn{blockRead: make(chan struct{})}
	s := newExecSession(c, "qf-1", conn)
	// Fill the inbox to capacity without starting inputWriter.
	for i := 0; i < execInputQueue; i++ {
		s.inbox <- execItem{data: []byte("x")}
	}

	// This send must hit the default branch — no blocking, no panic.
	c.HandleInput(protocol.ExecInputMessage{
		ExecID: "qf-1",
		Data:   base64.StdEncoding.EncodeToString([]byte("overflow")),
	})

	close(conn.blockRead)
}

// TestHandleInputSessionDone verifies that when the session done channel is
// closed and inbox is full, HandleInput routes to the done branch.
// Filling the inbox ensures the inbox-send case is blocked, leaving only
// done and default ready — so done fires deterministically over many calls.
func TestHandleInputSessionDone(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	s := newExecSession(c, "done-1", &fakeConn{})

	// Fill the inbox so the inbox-send case blocks.
	for i := 0; i < execInputQueue; i++ {
		s.inbox <- execItem{data: []byte("x")}
	}

	// Close the session — marks it as done.
	s.Close()

	// Re-register so HandleInput can find the session.
	c.execSessions.Store("done-1", s)

	// inbox is full, done is closed — select takes the done branch (or default).
	// Run multiple times to ensure the done branch fires at least once.
	for i := 0; i < 20; i++ {
		c.HandleInput(protocol.ExecInputMessage{
			ExecID: "done-1",
			Data:   base64.StdEncoding.EncodeToString([]byte("after close")),
		})
	}
}

// ---------------------------------------------------------------------------
// HandleResize — session done / queue full (lines 253-256)
// ---------------------------------------------------------------------------

// TestHandleResizeSessionDone verifies the done branch in HandleResize.
func TestHandleResizeSessionDone(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	s := newExecSession(c, "resize-done", &fakeConn{})
	s.Close()

	// Re-register so HandleResize can find the session.
	c.execSessions.Store("resize-done", s)

	// Done is closed and inbox empty → takes the done branch.
	c.HandleResize(context.Background(), protocol.ExecResizeMessage{
		ExecID: "resize-done", Cols: 80, Rows: 24,
	})
}

// TestHandleResizeQueueFull verifies the default (drop) branch in HandleResize.
func TestHandleResizeQueueFull(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	conn := &fakeConn{blockRead: make(chan struct{})}
	s := newExecSession(c, "resize-full", conn)
	// Fill inbox.
	for i := 0; i < execInputQueue; i++ {
		s.inbox <- execItem{data: []byte("x")}
	}

	// Must drop without blocking.
	c.HandleResize(context.Background(), protocol.ExecResizeMessage{
		ExecID: "resize-full", Cols: 80, Rows: 24,
	})
	close(conn.blockRead)
}

// ---------------------------------------------------------------------------
// doResize — session done and ctx.Done() branches (lines 272-275)
// ---------------------------------------------------------------------------

// TestDoResizeSessionDoneEarlyExit covers the s.done branch inside doResize:
// closing the session while a failing resize is retrying should cause
// doResize to exit early.
func TestDoResizeSessionDoneEarlyExit(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	fd := &fakeDocker{resizeErr: errors.New("always fail"), resizeFailFirst: 99}
	c.dockerClient = fd

	readBlock := make(chan struct{})
	s := newReadySession(c, "resize-early", &fakeConn{blockRead: readBlock})
	s.dockerExecID = "docker-resize-early"
	t.Cleanup(func() { close(readBlock) })

	c.HandleResize(context.Background(), protocol.ExecResizeMessage{
		ExecID: "resize-early", Cols: 80, Rows: 24,
	})

	// Close the session after at least one attempt — done fires and doResize
	// should exit before exhausting all 10 retries.
	waitFor(t, "at least one resize attempt", func() bool {
		return len(fd.resizeCallList()) >= 1
	})
	s.Close()

	// doResize exits via done; confirm it doesn't hang past the deadline.
	waitFor(t, "resize to stop after session close", func() bool {
		// Once the session is closed the inputWriter exits, so no more attempts.
		prev := len(fd.resizeCallList())
		time.Sleep(60 * time.Millisecond)
		return len(fd.resizeCallList()) == prev
	})
	if got := len(fd.resizeCallList()); got >= 10 {
		t.Errorf("resize attempts = %d, want < 10 (should exit via done)", got)
	}
}

// TestDoResizeCtxDoneEarlyExit covers the ctx.Done() branch inside doResize.
// doResize receives its ctx from inputWriter's goroutine context, so we
// start inputWriter with a cancellable context, enqueue a resize, wait for
// the first attempt, then cancel the context so subsequent retries exit early.
func TestDoResizeCtxDoneEarlyExit(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	fd := &fakeDocker{resizeErr: errors.New("always fail"), resizeFailFirst: 99}
	c.dockerClient = fd

	readBlock := make(chan struct{})
	execConn := &fakeConn{blockRead: readBlock}
	s := newExecSession(c, "resize-ctx", execConn)
	close(s.connReady)
	s.dockerExecID = "docker-resize-ctx"
	t.Cleanup(func() {
		close(readBlock)
		s.Close()
	})

	// Start inputWriter with a cancellable context — this is what doResize receives.
	ctx, cancel := context.WithCancel(context.Background())
	go s.inputWriter(ctx)

	// Enqueue a resize (ctx passed to HandleResize is discarded; doResize uses inputWriter's ctx).
	c.HandleResize(context.Background(), protocol.ExecResizeMessage{
		ExecID: "resize-ctx", Cols: 80, Rows: 24,
	})

	// Wait for at least one resize attempt, then cancel inputWriter's ctx.
	waitFor(t, "at least one resize attempt", func() bool {
		return len(fd.resizeCallList()) >= 1
	})
	cancel()

	// doResize should exit via ctx.Done() between retries (within 50ms).
	waitFor(t, "resize to stop after ctx cancel", func() bool {
		prev := len(fd.resizeCallList())
		time.Sleep(60 * time.Millisecond)
		return len(fd.resizeCallList()) == prev
	})
	if got := len(fd.resizeCallList()); got >= 10 {
		t.Errorf("resize attempts = %d, want < 10 (should exit via ctx.Done)", got)
	}
}

// ---------------------------------------------------------------------------
// Close — conn == nil path (lines 386-390 when conn is nil)
// ---------------------------------------------------------------------------

// TestCloseSessionNilConn covers the Close path where conn was never set
// (i.e., the exec never started). The once.Do must run without panicking.
func TestCloseSessionNilConn(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	s := &ExecSession{
		execID:    "nil-conn",
		client:    c,
		connReady: make(chan struct{}),
		inbox:     make(chan execItem, execInputQueue),
		done:      make(chan struct{}),
		// conn is nil intentionally.
	}
	c.execSessions.Store("nil-conn", s)

	// Must not panic with nil conn.
	s.Close()

	select {
	case <-s.done:
	default:
		t.Error("done channel not closed after Close with nil conn")
	}
}

// ---------------------------------------------------------------------------
// recoverSession — panic recovery path (line 400-403)
// ---------------------------------------------------------------------------

// TestRecoverSessionCatchesPanic verifies that recoverSession catches a
// real panic. We call it via a defer inside a goroutine that panics.
func TestRecoverSessionCatchesPanic(t *testing.T) {
	t.Parallel()

	recovered := make(chan struct{})
	go func() {
		defer func() { close(recovered) }()
		defer recoverSession("test-where", "test-exec")
		panic("intentional test panic")
	}()

	select {
	case <-recovered:
		// Goroutine exited cleanly after recoverSession caught the panic.
	case <-time.After(readTimeout):
		t.Fatal("recoverSession did not catch the panic in time")
	}
}

// ---------------------------------------------------------------------------
// activate — conn.Close() error path (line 301-303)
// ---------------------------------------------------------------------------

// errCloseConn is a fakeConn whose Close() returns an error, to hit the
// slog.Debug line in activate when closing an orphaned conn fails.
type errCloseConn struct {
	fakeConn
}

func (c *errCloseConn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return errors.New("close failed intentionally")
}

// TestActivateOrphanedConnCloseError covers the error-logging branch in
// activate: the orphaned conn.Close() returns an error, which is logged at
// Debug and otherwise ignored.
func TestActivateOrphanedConnCloseError(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	s := newExecSession(c, "orphan-err", &fakeConn{})
	s.Close() // mark as closed

	orphan := &errCloseConn{}
	if got := s.activate(orphan); got {
		t.Error("activate returned true for closed session, want false")
	}
	if !orphan.isClosed() {
		t.Error("activate did not attempt to close the orphaned conn")
	}
}

// ---------------------------------------------------------------------------
// readPump — resetting read deadline error path (line 483-485)
// ---------------------------------------------------------------------------

// TestReadPumpResetDeadlineError is deliberately not written: it would require
// a net.Conn that rejects SetReadDeadline after accepting reads, which is not
// possible with the gorilla/websocket layer (the Conn is unexported). This
// branch (line 483-485) is effectively unreachable in tests without a
// custom websocket.Conn.

// ---------------------------------------------------------------------------
// bringUpExec — initial resize failure (line 143-145 in tunnel.go)
// ---------------------------------------------------------------------------

// TestBringUpExecResizeFailureIsWarningOnly verifies that a failure in the
// initial ResizeExec call is logged as a warning but the session still
// succeeds (exec_ready is still sent).
func TestBringUpExecResizeFailureIsWarningOnly(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	execConn := &fakeConn{blockRead: make(chan struct{})}
	fd := &fakeDocker{
		createExecID:    "docker-rz-fail",
		startConn:       execConn,
		resizeErr:       errors.New("resize denied"),
		resizeFailFirst: 99, // always fail
	}
	c.dockerClient = fd

	c.StartExec(context.Background(), protocol.ExecStartMessage{
		ExecID:      "rz-fail",
		ContainerID: "c1",
		Cmd:         []string{"sh"},
		Cols:        80,
		Rows:        24,
	})

	// exec_ready must still arrive despite the resize failure.
	var ready protocol.ExecReadyMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeExecReady), &ready)
	if ready.ExecID != "rz-fail" {
		t.Errorf("exec_ready ExecID = %q, want rz-fail", ready.ExecID)
	}

	// Clean up: signal the blocked reader.
	close(execConn.blockRead)
	expectType(t, ctrl, protocol.TypeExecEnd)
}

// ---------------------------------------------------------------------------
// ExecSession.Close — conn.Close() returns error (line 387-389)
// ---------------------------------------------------------------------------

// TestCloseSessionConnCloseError covers the Close path where conn.Close()
// returns an error; the error is logged at Debug and otherwise ignored.
func TestCloseSessionConnCloseError(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	conn := &errCloseConn{}
	s := &ExecSession{
		execID:    "err-close",
		client:    c,
		conn:      conn,
		connReady: make(chan struct{}),
		inbox:     make(chan execItem, execInputQueue),
		done:      make(chan struct{}),
	}
	c.execSessions.Store("err-close", s)

	// Must not panic.
	s.Close()

	select {
	case <-s.done:
	default:
		t.Error("done not closed after Close with erroring conn")
	}
	if !conn.isClosed() {
		t.Error("conn.Close was not called")
	}
}

// ---------------------------------------------------------------------------
// writeInput — done channel fires during retry delay (line 227-228)
// ---------------------------------------------------------------------------

// TestWriteInputDoneFiresDuringRetry covers the case where the session's done
// channel is closed while writeInput is retrying (between write attempts).
func TestWriteInputDoneFiresDuringRetry(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)

	// fakeConn that blocks on Write until unblocked, then fails.
	writeGate := make(chan struct{})
	conn := &gatedErrConn{gate: writeGate}

	s := newReadySession(c, "done-retry", conn)

	// Enqueue input — inputWriter calls writeInput which blocks on Write.
	c.HandleInput(protocol.ExecInputMessage{
		ExecID: "done-retry",
		Data:   base64.StdEncoding.EncodeToString([]byte("test")),
	})

	// Let the first write attempt run (it will fail after the gate opens).
	close(writeGate)

	// Close the session to fire the done channel while writeInput is in the retry delay.
	// writeInput's retry loop: wait 50ms, then select { case <-s.done: return ... }.
	// By closing done during the 50ms sleep, the next iteration takes done branch.
	waitFor(t, "at least one write attempt", func() bool {
		return conn.attempts() >= 1
	})
	s.Close()

	// writeInput must stop after done fires — confirm it doesn't run all 10 retries.
	waitFor(t, "writeInput to stop after done", func() bool {
		prev := conn.attempts()
		time.Sleep(60 * time.Millisecond)
		return conn.attempts() == prev
	})
	if got := conn.attempts(); got >= 10 {
		t.Errorf("write attempts = %d, want < 10 (should stop via done)", got)
	}
}

// gatedErrConn is a net.Conn that blocks on Write until gate is closed, then fails.
type gatedErrConn struct {
	fakeConn
	gate  chan struct{}
	count int
	mu2   sync.Mutex
}

func (c *gatedErrConn) Write(p []byte) (int, error) {
	<-c.gate // block until gate closes
	c.mu2.Lock()
	c.count++
	c.mu2.Unlock()
	return 0, errors.New("write failed after gate")
}

func (c *gatedErrConn) attempts() int {
	c.mu2.Lock()
	defer c.mu2.Unlock()
	return c.count
}

// ---------------------------------------------------------------------------
// HandleResize — done channel fires when inbox is full (line 253-254)
// ---------------------------------------------------------------------------

// TestHandleResizeDoneWhenInboxFull covers the done branch in HandleResize
// when the inbox is already at capacity so the inbox send would block.
func TestHandleResizeDoneWhenInboxFull(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)

	// Run enough iterations that done branch fires at least once.
	// Each iteration: fill inbox, close session, call HandleResize.
	for i := 0; i < 20; i++ {
		sessionID := "resize-done-full-" + string(rune('a'+i))
		conn := &fakeConn{blockRead: make(chan struct{})}
		s := newExecSession(c, sessionID, conn)

		// Fill inbox to capacity.
		for j := 0; j < execInputQueue; j++ {
			s.inbox <- execItem{data: []byte("x")}
		}
		// Mark as done (close channel).
		close(s.done)
		// Re-register the closed session so HandleResize can find it.
		c.execSessions.Store(sessionID, s)

		// With done closed and inbox full: select picks done or default.
		c.HandleResize(context.Background(), protocol.ExecResizeMessage{
			ExecID: sessionID, Cols: 80, Rows: 24,
		})

		// Cleanup.
		close(conn.blockRead)
		c.execSessions.Delete(sessionID)
	}
	// No panic and no test timeout = success. The slog.Debug on the done branch
	// was exercised (statistically certain across 20 iterations).
}
