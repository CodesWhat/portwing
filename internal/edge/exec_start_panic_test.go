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
