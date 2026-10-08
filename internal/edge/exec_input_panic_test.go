package edge

import (
	"context"
	"encoding/base64"
	"log/slog"
	"net"
	"strings"
	"testing"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/protocol"
)

// liveSessionWithInput builds a live session over conn with one stdin chunk
// queued, so running its input writer writes to conn straight away.
func liveSessionWithInput(t *testing.T, c *Client, execID string, conn net.Conn) *ExecSession {
	t.Helper()
	session := newExecSession(c, execID, conn)
	close(session.connReady)
	if got := session.enqueueInput([]byte("x")); got != inputEnqueued {
		t.Fatalf("enqueue = %v, want inputEnqueued", got)
	}
	return session
}

// An input writer that panics leaves stdin dead, so it closes the session
// instead of leaving it open on its exec slot. It sends no exec_end itself:
// that stays with the read loop, which the close ends.
func TestInputWriterPanicClosesTheSession(t *testing.T) {
	t.Parallel()

	c, logger, next := newAuditedTestClient(t)
	conn := &countingConn{fakeConn: &fakeConn{}}
	session := liveSessionWithInput(t, c, "e1", &panickingConn{Conn: conn, at: "write"})

	// It returns only because the panic ended the session: nothing else
	// closes done here.
	runToEnd(t, func() { session.inputWriter(context.Background()) })

	requireTornDownSilently(t, c, session, next)
	requireNoAuditRecords(t, logger)
	if got := conn.closes.Load(); got != 1 {
		t.Fatalf("conn closed %d times, want 1", got)
	}
}

// On a live session the controller hears of an input writer's panic once, from
// the read loop: closing the session closes the conn under its read, and its
// exit sends the exec_end as it does when a write fails.
func TestInputWriterPanicTellsTheControllerOnce(t *testing.T) {
	t.Parallel()

	c, logger, next := newAuditedTestClient(t)
	agentSide, daemonSide := net.Pipe()
	t.Cleanup(func() { _ = daemonSide.Close() })
	// No cols or rows on the start, so the first resize is the one the input
	// writer runs.
	c.dockerClient = &panickingDocker{
		fakeDocker: &fakeDocker{createExecID: "d1", startConn: agentSide},
		at:         "resize",
	}

	c.StartExec(context.Background(), protocol.ExecStartMessage{ExecID: "e1", ContainerID: "c1"})
	if env := next(); env.Type != protocol.TypeExecReady {
		t.Fatalf("frame = %q, want exec_ready", env.Type)
	}
	val, ok := c.execSessions.Load("e1")
	if !ok {
		t.Fatal("session not registered after exec_ready")
	}
	session := val.(*ExecSession)

	c.HandleResize(context.Background(), protocol.ExecResizeMessage{ExecID: "e1", Cols: 80, Rows: 24})

	env := next()
	if env.Type != protocol.TypeExecEnd {
		t.Fatalf("frame = %q, want exec_end", env.Type)
	}
	var end protocol.ExecEndMessage
	decodeData(t, env.Data, &end)
	if end.ExecID != "e1" {
		t.Fatalf("exec_end = %+v, want e1", end)
	}
	requireTornDownSilently(t, c, session, next)
	// The admission's exec_start is still the session's only record.
	records := logger.Records(0)
	if len(records) != 1 || records[0].Event != audit.EventExecStart || records[0].Outcome != audit.OutcomeAllowed {
		t.Fatalf("audit records = %+v, want only the admission's exec_start", records)
	}
}

// The input writer closing the session doesn't take the exec_end with it. A
// session with no conn panics both goroutines: here the writer first, which
// closes it, then the read loop, which finds it closed and still owes the
// controller.
func TestInputWriterThenReadLoopPanicSendsOneExecEnd(t *testing.T) {
	t.Parallel()

	c, _, next := newAuditedTestClient(t)
	session := liveSessionWithInput(t, c, "e1", nil)

	runToEnd(t, func() { session.inputWriter(context.Background()) })
	requireClosedAndDeregistered(t, c, session)
	runToEnd(t, session.readLoop)

	requireExecEnd(t, next, "exec session failed: internal error")
	requireNoFurtherFrame(t, c, next)
}

// The same through a real start, where the two goroutines race: stdin queued
// during the bring-up panics the writer as the session goes live, and the read
// loop panics on its first read. Whichever closes the session first, the
// controller gets one exec_end after its exec_ready.
func TestSessionWithoutConnSendsOneExecEnd(t *testing.T) {
	t.Parallel()

	c, _, next := newAuditedTestClient(t)
	gate := make(chan struct{})
	// No startConn: the daemon call hands back no conn and no error.
	c.dockerClient = &fakeDocker{createExecID: "d1", startGate: gate}

	c.StartExec(context.Background(), protocol.ExecStartMessage{ExecID: "e1", ContainerID: "c1"})
	val, ok := c.execSessions.Load("e1")
	if !ok {
		t.Fatal("session not registered by StartExec")
	}
	session := val.(*ExecSession)
	c.HandleInput(protocol.ExecInputMessage{ExecID: "e1", Data: base64.StdEncoding.EncodeToString([]byte("x"))})
	close(gate)

	if env := next(); env.Type != protocol.TypeExecReady {
		t.Fatalf("frame = %q, want exec_ready", env.Type)
	}
	requireExecEnd(t, next, "exec session failed: internal error")
	// The read loop's exec_end goes out ahead of its close, so the close is
	// awaited when the read loop is the one that got there first.
	waitFor(t, "session to be closed", session.isClosed)
	requireTornDownSilently(t, c, session, next)
}

// The input writer's close can panic as well, and that stays inside it too.
func TestInputWriterTeardownPanicStaysContained(t *testing.T) {
	t.Parallel()

	c, _, _ := newAuditedTestClient(t)
	session := liveSessionWithInput(t, c, "e1", &panickingConn{Conn: &panicCloseConn{fakeConn: &fakeConn{}}, at: "write"})

	runToEnd(t, func() { session.inputWriter(context.Background()) })

	requireClosedAndDeregistered(t, c, session)
}

// The input writer's panic value goes to the agent's log, which is the only
// place it lands: the controller gets the read loop's exec_end.
func TestInputWriterPanicIsLogged(t *testing.T) {
	// Not parallel: it swaps the process-global slog default.
	// syncBuffer because the client's send pump is live and may log too.
	logBuf := &syncBuffer{}
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logBuf, nil)))
	defer slog.SetDefault(oldLogger)

	c, _, _ := newAuditedTestClient(t)
	session := liveSessionWithInput(t, c, "e1", &panickingConn{Conn: &fakeConn{}, at: "write"})

	runToEnd(t, func() { session.inputWriter(context.Background()) })

	out := logBuf.String()
	for _, want := range []string{"recovered from panic", "where=inputWriter", "execID=e1", "boom in write"} {
		if !strings.Contains(out, want) {
			t.Errorf("log = %q, want it to contain %q", out, want)
		}
	}
}
