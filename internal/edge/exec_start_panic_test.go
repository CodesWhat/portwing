package edge

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/codeswhat/portwing/internal/protocol"
)

// panickingDocker panics from one daemon call, standing in for a bug under the
// bring-up that only a recover stops.
type panickingDocker struct {
	*fakeDocker
	at string // "create", "start" or "resize"
}

func (p *panickingDocker) CreateExec(ctx context.Context, containerID string, cmd []string, user string, tty bool) (string, error) {
	if p.at == "create" {
		panic("boom in create")
	}
	return p.fakeDocker.CreateExec(ctx, containerID, cmd, user, tty)
}

func (p *panickingDocker) StartExec(ctx context.Context, execID string, tty bool) (net.Conn, error) {
	if p.at == "start" {
		panic("boom in start")
	}
	return p.fakeDocker.StartExec(ctx, execID, tty)
}

func (p *panickingDocker) ResizeExec(ctx context.Context, execID string, cols, rows int) error {
	if p.at == "resize" {
		panic("boom in resize")
	}
	return p.fakeDocker.ResizeExec(ctx, execID, cols, rows)
}

// countingConn counts Close calls, which tells a conn closed once from one
// that leaked or was closed twice.
type countingConn struct {
	*fakeConn
	closes atomic.Int32
}

func (c *countingConn) Close() error {
	c.closes.Add(1)
	return c.fakeConn.Close()
}

func requireClosedAndDeregistered(t *testing.T, c *Client, session *ExecSession) {
	t.Helper()
	if !session.isClosed() {
		t.Fatal("session not closed")
	}
	if _, ok := c.execSessions.Load(session.execID); ok {
		t.Fatal("session still registered, so it still holds its exec slot")
	}
}

// A bring-up that panics is a failed start like any other: exactly one
// api_request error record under exec_start, the session closed and
// deregistered, and an exec_end so the controller isn't left waiting for an
// exec_ready. The hijacked conn is closed when the panic lands after the daemon
// handed it over and before the session took it.
func TestBringUpExecPanicFailsTheStart(t *testing.T) {
	t.Parallel()

	msg := protocol.ExecStartMessage{ExecID: "e1", ContainerID: "c1", Cols: 80, Rows: 24}
	for _, tc := range []struct {
		at         string
		wantCloses int32
	}{
		{"create", 0},
		{"start", 0},
		{"resize", 1},
	} {
		t.Run(tc.at, func(t *testing.T) {
			t.Parallel()
			c, logger, next := newAuditedTestClient(t)
			conn := &countingConn{fakeConn: &fakeConn{}}
			c.dockerClient = &panickingDocker{
				fakeDocker: &fakeDocker{createExecID: "d1", startConn: conn},
				at:         tc.at,
			}
			sessionCtx, session := registeredSession(t, c, context.Background(), msg)

			// Returning at all is the agent surviving the panic.
			c.bringUpExec(sessionCtx, msg, session)

			requireOneStartError(t, logger)
			requireClosedAndDeregistered(t, c, session)
			if got := conn.closes.Load(); got != tc.wantCloses {
				t.Fatalf("hijacked conn closed %d times, want %d", got, tc.wantCloses)
			}
			env := next()
			if env.Type != protocol.TypeExecEnd {
				t.Fatalf("frame = %q, want exec_end", env.Type)
			}
			var end protocol.ExecEndMessage
			decodeData(t, env.Data, &end)
			if end.ExecID != "e1" || end.Reason != "exec start failed: internal error" {
				t.Fatalf("exec_end = %+v, want e1 with the fixed internal-error reason", end)
			}
			requireNoFurtherFrame(t, c, next)
		})
	}
}

// A panic after the controller's exec_end or a tunnel drop already closed the
// session still writes the record, and sends no exec_end of its own: whoever
// closed the session owns that.
func TestBringUpExecPanicAfterCloseSendsNoExecEnd(t *testing.T) {
	t.Parallel()

	msg := protocol.ExecStartMessage{ExecID: "e1", ContainerID: "c1"}
	c, logger, next := newAuditedTestClient(t)
	var session *ExecSession
	c.dockerClient = &fakeDocker{createHook: func() {
		session.Close()
		panic("boom after close")
	}}
	sessionCtx, registered := registeredSession(t, c, context.Background(), msg)
	session = registered

	c.bringUpExec(sessionCtx, msg, session)

	requireOneStartError(t, logger)
	requireTornDownSilently(t, c, session, next)
}

// A panic after a tunnel drop ended the bring-up's context, with nothing having
// closed the session yet, writes the record and sends no exec_end either:
// nobody is left to tell.
func TestBringUpExecPanicAfterCancelSendsNoExecEnd(t *testing.T) {
	t.Parallel()

	msg := protocol.ExecStartMessage{ExecID: "e1", ContainerID: "c1"}
	c, logger, next := newAuditedTestClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	c.dockerClient = &fakeDocker{createHook: func() {
		cancel()
		panic("boom after cancel")
	}}
	sessionCtx, session := registeredSession(t, c, ctx, msg)

	c.bringUpExec(sessionCtx, msg, session)

	requireOneStartError(t, logger)
	requireTornDownSilently(t, c, session, next)
}

// brokenTarget is an outbound target whose every send panics (a queue with no
// state behind it), standing in for a bug in the send path.
func brokenTarget() outboundTarget {
	return outboundTarget{ch: make(chan protocol.Envelope, 1)}
}

// A panic after the exit already wrote its record adds no second one. Here the
// create fails, failStart writes the record and closes, and its exec_end is
// what panics.
func TestBringUpExecPanicAfterRecordWritesNoSecond(t *testing.T) {
	t.Parallel()

	msg := protocol.ExecStartMessage{ExecID: "e1", ContainerID: "c1"}
	c, logger, _ := newAuditedTestClient(t)
	c.dockerClient = &fakeDocker{createExecErr: errors.New("boom")}
	sessionCtx, session := registeredSession(t, c, context.Background(), msg)
	session.target = brokenTarget()

	c.bringUpExec(sessionCtx, msg, session)

	requireOneStartError(t, logger)
	requireClosedAndDeregistered(t, c, session)
}

// A panic after activate handed the conn to the session closes it once,
// through the session, and the teardown's own exec_end panicking on the same
// broken send path still doesn't escape the bring-up.
func TestBringUpExecPanicAfterActivateClosesConnOnce(t *testing.T) {
	t.Parallel()

	msg := protocol.ExecStartMessage{ExecID: "e1", ContainerID: "c1"}
	c, logger, _ := newAuditedTestClient(t)
	conn := &countingConn{fakeConn: &fakeConn{}}
	c.dockerClient = &fakeDocker{createExecID: "d1", startConn: conn}
	sessionCtx, session := registeredSession(t, c, context.Background(), msg)
	session.target = brokenTarget()

	c.bringUpExec(sessionCtx, msg, session)

	requireOneStartError(t, logger)
	requireClosedAndDeregistered(t, c, session)
	if got := conn.closes.Load(); got != 1 {
		t.Fatalf("hijacked conn closed %d times, want 1", got)
	}
}

// The panic value goes to the agent's log, which is the only place it lands:
// the controller gets the fixed reason.
func TestBringUpExecPanicIsLogged(t *testing.T) {
	// Not parallel: it swaps the process-global slog default.
	// syncBuffer because the client's send pump is live and may log too.
	logBuf := &syncBuffer{}
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logBuf, nil)))
	defer slog.SetDefault(oldLogger)

	msg := protocol.ExecStartMessage{ExecID: "e1", ContainerID: "c1"}
	c, _, _ := newAuditedTestClient(t)
	c.dockerClient = &panickingDocker{fakeDocker: &fakeDocker{}, at: "create"}
	sessionCtx, session := registeredSession(t, c, context.Background(), msg)

	c.bringUpExec(sessionCtx, msg, session)

	out := logBuf.String()
	for _, want := range []string{"recovered from panic", "where=bringUpExec", "execID=e1", "boom in create"} {
		if !strings.Contains(out, want) {
			t.Errorf("log = %q, want it to contain %q", out, want)
		}
	}
}

// panicCloseConn is a conn whose Close panics, standing in for a bug under the
// teardown of a conn the session or its bring-up holds.
type panicCloseConn struct {
	*fakeConn
}

func (*panicCloseConn) Close() error { panic("boom in close") }

// requireExecEnd asserts the next frame is e1's exec_end with the given reason.
func requireExecEnd(t *testing.T, next func() protocol.Envelope, reason string) {
	t.Helper()
	env := next()
	if env.Type != protocol.TypeExecEnd {
		t.Fatalf("frame = %q, want exec_end", env.Type)
	}
	var end protocol.ExecEndMessage
	decodeData(t, env.Data, &end)
	if end.ExecID != "e1" || end.Reason != reason {
		t.Fatalf("exec_end = %+v, want e1 with reason %q", end, reason)
	}
}

// The panic exit's own steps can panic too. Whichever one does, the start still
// ends failed: the session closed and deregistered and the exec_end sent, with
// the record written unless the record is what panicked.
func TestBringUpExecPanicExitSurvivesItsOwnPanic(t *testing.T) {
	t.Parallel()

	msg := protocol.ExecStartMessage{ExecID: "e1", ContainerID: "c1", Cols: 80, Rows: 24}

	t.Run("closing the unwired conn panics", func(t *testing.T) {
		t.Parallel()
		c, logger, next := newAuditedTestClient(t)
		c.dockerClient = &panickingDocker{
			fakeDocker: &fakeDocker{createExecID: "d1", startConn: &panicCloseConn{fakeConn: &fakeConn{}}},
			at:         "resize",
		}
		sessionCtx, session := registeredSession(t, c, context.Background(), msg)

		c.bringUpExec(sessionCtx, msg, session)

		requireOneStartError(t, logger)
		requireClosedAndDeregistered(t, c, session)
		requireExecEnd(t, next, "exec start failed: internal error")
		requireNoFurtherFrame(t, c, next)
	})

	t.Run("writing the record panics", func(t *testing.T) {
		t.Parallel()
		c, _, next := newAuditedTestClient(t)
		// No auditor at all, so the record write dereferences nil.
		c.auditor = nil
		c.dockerClient = &panickingDocker{fakeDocker: &fakeDocker{}, at: "create"}
		sessionCtx, session := registeredSession(t, c, context.Background(), msg)

		c.bringUpExec(sessionCtx, msg, session)

		requireClosedAndDeregistered(t, c, session)
		requireExecEnd(t, next, "exec start failed: internal error")
		requireNoFurtherFrame(t, c, next)
	})

	// A sink that always panics takes two writes down: the failed create's own,
	// which is what sends the bring-up to its panic exit, and that exit's retry.
	t.Run("writing the record panics every time", func(t *testing.T) {
		t.Parallel()
		c, _, next := newAuditedTestClient(t)
		c.auditor = nil
		c.dockerClient = &fakeDocker{createExecErr: errors.New("boom")}
		sessionCtx, session := registeredSession(t, c, context.Background(), msg)

		c.bringUpExec(sessionCtx, msg, session)

		requireClosedAndDeregistered(t, c, session)
		requireExecEnd(t, next, "exec start failed: internal error")
		requireNoFurtherFrame(t, c, next)
	})
}

// A record write that panicked doesn't count as written, so the next attempt
// writes it. One that returned does, so nothing after it writes a second.
func TestStartErrorRecordIsRetriedAfterItsWritePanics(t *testing.T) {
	t.Parallel()

	c, logger, _ := newAuditedTestClient(t)
	_, session := registeredSession(t, c, context.Background(), protocol.ExecStartMessage{ExecID: "e1", ContainerID: "c1"})

	// No auditor: the write dereferences nil before it records anything.
	c.auditor = nil
	func() {
		defer func() {
			if recover() == nil {
				t.Error("the write with no auditor did not panic")
			}
		}()
		session.auditStartError()
	}()

	c.auditor = logger
	session.auditStartError()
	requireOneStartError(t, logger)

	session.auditStartError()
	requireOneStartError(t, logger)
}

// closeHookConn runs a hook on each Close, numbered from one.
type closeHookConn struct {
	*fakeConn
	closes  int
	onClose func(n int)
}

func (c *closeHookConn) Close() error {
	c.closes++
	c.onClose(c.closes)
	return c.fakeConn.Close()
}

// An exit whose record write panics still leaves the record owed, and the
// panic exit that follows writes it. Here the sink panics on its first call
// and records on its second: there is no auditor until the conn's second
// close. Its first close is activate's, for a session closed under it, and
// that exit's write is the one that panics. Its second is the panic exit's,
// just ahead of the retry.
func TestBringUpExecRetriesARecordWhoseWritePanicked(t *testing.T) {
	t.Parallel()

	msg := protocol.ExecStartMessage{ExecID: "e1", ContainerID: "c1", Cols: 80, Rows: 24}
	c, logger, next := newAuditedTestClient(t)
	c.auditor = nil
	conn := &closeHookConn{fakeConn: &fakeConn{}}
	conn.onClose = func(n int) {
		if n == 2 {
			c.auditor = logger
		}
	}
	rc := &resizeClosing{fakeDocker: &fakeDocker{createExecID: "d1", startConn: conn}}
	c.dockerClient = rc
	sessionCtx, session := registeredSession(t, c, context.Background(), msg)
	rc.session = session

	c.bringUpExec(sessionCtx, msg, session)

	requireOneStartError(t, logger)
	requireTornDownSilently(t, c, session, next)
	if conn.closes != 2 {
		t.Fatalf("conn closed %d times, want 2 (activate's and the panic exit's)", conn.closes)
	}
}
