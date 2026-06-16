package edge

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strconv"
	"testing"

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
