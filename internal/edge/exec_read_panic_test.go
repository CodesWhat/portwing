package edge

import (
	"log/slog"
	"net"
	"strings"
	"testing"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/protocol"
)

// panickingConn panics from one conn call, standing in for a bug under a live
// session's I/O that only a recover stops.
type panickingConn struct {
	net.Conn
	at string // "read" or "write"
	// before runs ahead of a read's panic, for a test that has something else
	// end the session first.
	before func()
}

func (p *panickingConn) Read(b []byte) (int, error) {
	if p.at == "read" {
		if p.before != nil {
			p.before()
		}
		panic("boom in read")
	}
	return p.Conn.Read(b)
}

func (p *panickingConn) Write(b []byte) (int, error) {
	if p.at == "write" {
		panic("boom in write")
	}
	return p.Conn.Write(b)
}

// requireNoFurtherFrame asserts nothing else is queued for the controller: a
// pong sent now has to be the next frame it sees.
func requireNoFurtherFrame(t *testing.T, c *Client, next func() protocol.Envelope) {
	t.Helper()
	if err := c.sendTypedMessage(protocol.TypePong, struct{}{}); err != nil {
		t.Fatal(err)
	}
	if env := next(); env.Type != protocol.TypePong {
		t.Fatalf("next frame = %q, want the pong sentinel", env.Type)
	}
}

// requireNoAuditRecords asserts a session's end left the audit ring empty. The
// exec_start record written at admission is the only one a session gets, and
// these tests build their sessions past admission.
func requireNoAuditRecords(t *testing.T, logger *audit.Logger) {
	t.Helper()
	if got := logger.Records(0); len(got) != 0 {
		t.Fatalf("audit records = %+v, want none for a session ending", got)
	}
}

// requireNoEscape runs f and fails the test if a panic gets out of it, which
// in the agent would be the process going down.
func requireNoEscape(t *testing.T, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic escaped the session goroutine: %v", r)
		}
	}()
	f()
}

// A read loop that returns sends its one exec_end, closes the session and
// writes no audit record. It is what the panic exit below is held to.
func TestReadLoopReturnSendsOneExecEnd(t *testing.T) {
	t.Parallel()

	c, logger, next := newAuditedTestClient(t)
	conn := &countingConn{fakeConn: &fakeConn{}}
	session := newExecSession(c, "e1", conn)

	session.readLoop()

	requireClosedAndDeregistered(t, c, session)
	requireExecEnd(t, next, "exited")
	requireNoFurtherFrame(t, c, next)
	requireNoAuditRecords(t, logger)
	if got := conn.closes.Load(); got != 1 {
		t.Fatalf("conn closed %d times, want 1", got)
	}
}

// A read loop that panics ends the session the way one that returns does:
// closed and deregistered, one exec_end for the controller, no audit record.
// Before, it closed the session and said nothing, so the controller kept a
// session the agent had already dropped.
func TestReadLoopPanicEndsTheSession(t *testing.T) {
	t.Parallel()

	// A session wired with no conn at all panics on its first read.
	t.Run("no conn", func(t *testing.T) {
		t.Parallel()
		c, logger, next := newAuditedTestClient(t)
		session := newExecSession(c, "e1", nil)

		// Returning at all is the agent surviving the panic.
		session.readLoop()

		requireClosedAndDeregistered(t, c, session)
		requireExecEnd(t, next, "exec session failed: internal error")
		requireNoFurtherFrame(t, c, next)
		requireNoAuditRecords(t, logger)
	})

	t.Run("read panics", func(t *testing.T) {
		t.Parallel()
		c, logger, next := newAuditedTestClient(t)
		conn := &countingConn{fakeConn: &fakeConn{}}
		session := newExecSession(c, "e1", &panickingConn{Conn: conn, at: "read"})

		session.readLoop()

		requireClosedAndDeregistered(t, c, session)
		requireExecEnd(t, next, "exec session failed: internal error")
		requireNoFurtherFrame(t, c, next)
		requireNoAuditRecords(t, logger)
		if got := conn.closes.Load(); got != 1 {
			t.Fatalf("conn closed %d times, want 1", got)
		}
	})
}

// A read loop that panics after the controller's exec_end or a tunnel drop
// already ended the session sends no exec_end of its own: whoever ended the
// session owns that.
func TestReadLoopPanicAfterPeerEndedSendsNoExecEnd(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		end  func(c *Client)
	}{
		{"controller exec_end", func(c *Client) { c.EndExec(protocol.ExecEndMessage{ExecID: "e1"}) }},
		{"tunnel drop", func(c *Client) { c.closeAllExecSessions() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, logger, next := newAuditedTestClient(t)
			conn := &countingConn{fakeConn: &fakeConn{}}
			session := newExecSession(c, "e1", &panickingConn{Conn: conn, at: "read", before: func() { tc.end(c) }})

			session.readLoop()

			requireTornDownSilently(t, c, session, next)
			requireNoAuditRecords(t, logger)
			if got := conn.closes.Load(); got != 1 {
				t.Fatalf("conn closed %d times, want 1", got)
			}
		})
	}
}

// Closed is not the same as ended by the controller. The input writer closes a
// session it can't write to and leaves the exec_end to the read loop, so a read
// loop that panics on a session its own side closed still has to send it.
func TestReadLoopPanicAfterOwnCloseStillSendsExecEnd(t *testing.T) {
	t.Parallel()

	c, logger, next := newAuditedTestClient(t)
	conn := &countingConn{fakeConn: &fakeConn{}}
	reads := &panickingConn{Conn: conn, at: "read"}
	session := newExecSession(c, "e1", reads)
	reads.before = session.Close

	session.readLoop()

	requireClosedAndDeregistered(t, c, session)
	requireExecEnd(t, next, "exec session failed: internal error")
	requireNoFurtherFrame(t, c, next)
	requireNoAuditRecords(t, logger)
	if got := conn.closes.Load(); got != 1 {
		t.Fatalf("conn closed %d times, want 1", got)
	}
}

// The read loop's teardown can panic too, on either exit, and none of it
// reaches the agent.
func TestReadLoopTeardownPanicStaysContained(t *testing.T) {
	t.Parallel()

	// The send path is what panicked, so the panic exit's exec_end panics on
	// it again. The session is closed all the same.
	t.Run("panic exit, exec_end panics", func(t *testing.T) {
		t.Parallel()
		c, _, _ := newAuditedTestClient(t)
		conn := &countingConn{fakeConn: &fakeConn{reads: [][]byte{[]byte("x")}}}
		session := newExecSession(c, "e1", conn)
		session.target = brokenTarget()

		requireNoEscape(t, session.readLoop)

		requireClosedAndDeregistered(t, c, session)
		if got := conn.closes.Load(); got != 1 {
			t.Fatalf("conn closed %d times, want 1", got)
		}
	})

	// The exec_end is out before the close, so a close that panics costs the
	// controller nothing.
	t.Run("panic exit, close panics", func(t *testing.T) {
		t.Parallel()
		c, _, next := newAuditedTestClient(t)
		session := newExecSession(c, "e1", &panickingConn{Conn: &panicCloseConn{fakeConn: &fakeConn{}}, at: "read"})

		requireNoEscape(t, session.readLoop)

		requireClosedAndDeregistered(t, c, session)
		requireExecEnd(t, next, "exec session failed: internal error")
		requireNoFurtherFrame(t, c, next)
	})

	// The close deferred for a loop that returned is inside the recover as
	// well. It used to sit outside it, where a panic took the agent down.
	t.Run("normal exit, close panics", func(t *testing.T) {
		t.Parallel()
		c, _, next := newAuditedTestClient(t)
		session := newExecSession(c, "e1", &panicCloseConn{fakeConn: &fakeConn{}})

		requireNoEscape(t, session.readLoop)

		requireClosedAndDeregistered(t, c, session)
		requireExecEnd(t, next, "exited")
		requireNoFurtherFrame(t, c, next)
	})
}

// The read loop's panic value goes to the agent's log, which is the only place
// it lands: the controller gets the fixed reason.
func TestReadLoopPanicIsLogged(t *testing.T) {
	// Not parallel: it swaps the process-global slog default.
	// syncBuffer because the client's send pump is live and may log too.
	logBuf := &syncBuffer{}
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logBuf, nil)))
	defer slog.SetDefault(oldLogger)

	c, _, _ := newAuditedTestClient(t)
	session := newExecSession(c, "e1", &panickingConn{Conn: &fakeConn{}, at: "read"})

	session.readLoop()

	out := logBuf.String()
	for _, want := range []string{"recovered from panic", "where=readLoop", "execID=e1", "boom in read"} {
		if !strings.Contains(out, want) {
			t.Errorf("log = %q, want it to contain %q", out, want)
		}
	}
}
