package edge

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/protocol"
)

// registeredSession builds and registers a typed exec session the way
// StartExec does, so a test can run bringUpExec synchronously and read the
// audit ring the moment it returns.
func registeredSession(t *testing.T, c *Client, ctx context.Context, msg protocol.ExecStartMessage) (context.Context, *ExecSession) {
	t.Helper()
	sessionCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	session := &ExecSession{
		execID:      msg.ExecID,
		containerID: msg.ContainerID,
		client:      c,
		target:      c.currentOutboundTarget(),
		tty:         true,
		connReady:   make(chan struct{}),
		inbox:       make(chan execItem, execInputQueue),
		done:        make(chan struct{}),
		cancel:      cancel,
	}
	if got := c.admitTypedExec(msg.ExecID, session); got != execAdmitted {
		t.Fatalf("admission = %v, want execAdmitted", got)
	}
	t.Cleanup(session.Close)
	return sessionCtx, session
}

func startErrorRecords(logger *audit.Logger) []audit.Record {
	var out []audit.Record
	for _, r := range recordsOfEvent(logger, audit.EventAPIRequest) {
		if r.Method == protocol.TypeExecStart {
			out = append(out, r)
		}
	}
	return out
}

func requireOneStartError(t *testing.T, logger *audit.Logger) {
	t.Helper()
	got := startErrorRecords(logger)
	if len(got) != 1 || got[0].Outcome != audit.OutcomeError || got[0].Path != "c1" {
		t.Fatalf("exec_start error records = %+v, want exactly one error record for c1", got)
	}
}

// requireTornDownSilently asserts the session is closed and deregistered and
// that no exec_end went out: a pong queued after bringUpExec returned has to be
// the next frame the controller sees.
func requireTornDownSilently(t *testing.T, c *Client, session *ExecSession, next func() protocol.Envelope) {
	t.Helper()
	if !session.isClosed() {
		t.Fatal("session not closed")
	}
	if _, ok := c.execSessions.Load("e1"); ok {
		t.Fatal("session still registered")
	}
	if err := c.sendTypedMessage(protocol.TypePong, struct{}{}); err != nil {
		t.Fatal(err)
	}
	if env := next(); env.Type != protocol.TypePong {
		t.Fatalf("next frame = %q, want the pong sentinel (no exec_end)", env.Type)
	}
}

// resizeClosing closes the session from inside the initial resize, the last
// daemon call before activate, so activate finds the session already closed.
type resizeClosing struct {
	*fakeDocker
	session *ExecSession
}

func (r *resizeClosing) ResizeExec(ctx context.Context, execID string, cols, rows int) error {
	r.session.Close()
	return r.fakeDocker.ResizeExec(ctx, execID, cols, rows)
}

// Every bringUpExec exit where an admitted typed exec does not come up writes
// exactly one api_request error record under exec_start.
func TestBringUpExecExitsWriteOneErrorRecord(t *testing.T) {
	t.Parallel()

	msg := protocol.ExecStartMessage{ExecID: "e1", ContainerID: "c1"}

	t.Run("cancelled before create", func(t *testing.T) {
		t.Parallel()
		c, logger, next := newAuditedTestClient(t)
		fd := &fakeDocker{}
		c.dockerClient = fd
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		sessionCtx, session := registeredSession(t, c, ctx, msg)

		c.bringUpExec(sessionCtx, msg, session)

		requireOneStartError(t, logger)
		if len(fd.createCalls) != 0 {
			t.Fatalf("create calls = %d, want 0", len(fd.createCalls))
		}
		requireTornDownSilently(t, c, session, next)
	})

	t.Run("cancelled after create", func(t *testing.T) {
		t.Parallel()
		c, logger, next := newAuditedTestClient(t)
		ctx, cancel := context.WithCancel(context.Background())
		fd := &fakeDocker{createExecID: "d1", createHook: cancel}
		c.dockerClient = fd
		sessionCtx, session := registeredSession(t, c, ctx, msg)

		c.bringUpExec(sessionCtx, msg, session)

		requireOneStartError(t, logger)
		if len(fd.startCalls) != 0 {
			t.Fatalf("start calls = %d, want 0", len(fd.startCalls))
		}
		requireTornDownSilently(t, c, session, next)
	})

	t.Run("create fails", func(t *testing.T) {
		t.Parallel()
		c, logger, next := newAuditedTestClient(t)
		c.dockerClient = &fakeDocker{createExecErr: errors.New("boom")}
		sessionCtx, session := registeredSession(t, c, context.Background(), msg)

		c.bringUpExec(sessionCtx, msg, session)

		if env := next(); env.Type != protocol.TypeExecEnd {
			t.Fatalf("frame = %q, want exec_end", env.Type)
		}
		requireOneStartError(t, logger)
	})

	t.Run("start fails", func(t *testing.T) {
		t.Parallel()
		c, logger, next := newAuditedTestClient(t)
		c.dockerClient = &fakeDocker{createExecID: "d1", startExecErr: errors.New("boom")}
		sessionCtx, session := registeredSession(t, c, context.Background(), msg)

		c.bringUpExec(sessionCtx, msg, session)

		if env := next(); env.Type != protocol.TypeExecEnd {
			t.Fatalf("frame = %q, want exec_end", env.Type)
		}
		requireOneStartError(t, logger)
	})

	t.Run("closed before activate", func(t *testing.T) {
		t.Parallel()
		c, logger, next := newAuditedTestClient(t)
		agentSide, daemonSide := net.Pipe()
		t.Cleanup(func() { _ = daemonSide.Close() })
		fd := &fakeDocker{createExecID: "d1", startConn: agentSide}
		rc := &resizeClosing{fakeDocker: fd}
		c.dockerClient = rc
		sessionCtx, session := registeredSession(t, c, context.Background(), protocol.ExecStartMessage{ExecID: "e1", ContainerID: "c1", Cols: 80, Rows: 24})
		rc.session = session

		c.bringUpExec(sessionCtx, protocol.ExecStartMessage{ExecID: "e1", ContainerID: "c1", Cols: 80, Rows: 24}, session)

		requireOneStartError(t, logger)
		if len(fd.startCalls) != 1 {
			t.Fatalf("start calls = %d, want 1", len(fd.startCalls))
		}
		requireTornDownSilently(t, c, session, next)
	})
}

// A bring-up that comes up writes no error record.
func TestBringUpExecSuccessWritesNoErrorRecord(t *testing.T) {
	t.Parallel()

	c, logger, next := newAuditedTestClient(t)
	agentSide, daemonSide := net.Pipe()
	t.Cleanup(func() { _ = daemonSide.Close() })
	c.dockerClient = &fakeDocker{createExecID: "d1", startConn: agentSide}
	msg := protocol.ExecStartMessage{ExecID: "e1", ContainerID: "c1"}
	sessionCtx, session := registeredSession(t, c, context.Background(), msg)

	c.bringUpExec(sessionCtx, msg, session)

	if env := next(); env.Type != protocol.TypeExecReady {
		t.Fatalf("frame = %q, want exec_ready", env.Type)
	}
	if got := startErrorRecords(logger); len(got) != 0 {
		t.Fatalf("exec_start error records = %+v, want none", got)
	}
}
