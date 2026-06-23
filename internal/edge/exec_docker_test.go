package edge

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/codeswhat/portwing/internal/protocol"
)

// On a successful start, StartExec creates and starts the Docker exec, applies
// the requested terminal size, announces exec_ready, then streams output and a
// terminal exec_end as the session drains.
func TestStartExecSuccessStreamsOutput(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	execConn := &fakeConn{reads: [][]byte{[]byte("hi\n")}, readErr: io.EOF}
	fd := &fakeDocker{createExecID: "docker-1", startConn: execConn}
	c.dockerClient = fd

	c.StartExec(context.Background(), protocol.ExecStartMessage{
		ExecID:      "e1",
		ContainerID: "c1",
		Cmd:         []string{"sh", "-c", "echo hi"},
		User:        "root",
		Cols:        120,
		Rows:        40,
	})

	var ready protocol.ExecReadyMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeExecReady), &ready)
	if ready.ExecID != "e1" {
		t.Errorf("exec_ready ExecID = %q, want e1", ready.ExecID)
	}

	var out protocol.ExecOutputMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeExecOutput), &out)
	if decoded, _ := base64.StdEncoding.DecodeString(out.Data); string(decoded) != "hi\n" {
		t.Errorf("streamed output = %q, want %q", decoded, "hi\n")
	}

	var end protocol.ExecEndMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeExecEnd), &end)
	if end.Reason != "exited" {
		t.Errorf("exec_end reason = %q, want exited", end.Reason)
	}

	// The exec was created with the requested command/user, and the initial
	// resize targeted the Docker-issued exec id (not the protocol exec id).
	create := fd.createCallList()
	if len(create) != 1 || create[0].containerID != "c1" || create[0].user != "root" ||
		!reflect.DeepEqual(create[0].cmd, []string{"sh", "-c", "echo hi"}) {
		t.Errorf("CreateExec calls = %+v, want one call for c1/root/[sh -c echo hi]", create)
	}
	resizes := fd.resizeCallList()
	if len(resizes) != 1 || resizes[0] != (resizeCall{"docker-1", 120, 40}) {
		t.Errorf("resize calls = %+v, want one {docker-1 120 40}", resizes)
	}
}

// A zero-size terminal request skips the initial resize entirely.
func TestStartExecSkipsResizeWhenNoSize(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	execConn := &fakeConn{readErr: io.EOF}
	fd := &fakeDocker{createExecID: "docker-1", startConn: execConn}
	c.dockerClient = fd

	c.StartExec(context.Background(), protocol.ExecStartMessage{
		ExecID: "e1", ContainerID: "c1", Cmd: []string{"sh"},
	})

	// Drain the lifecycle so the session goroutine completes.
	expectType(t, ctrl, protocol.TypeExecReady)
	expectType(t, ctrl, protocol.TypeExecEnd)

	if got := fd.resizeCallList(); len(got) != 0 {
		t.Errorf("resize calls = %+v, want none for a zero-size request", got)
	}
}

// A CreateExec failure is reported as a terminal exec_end and no session is
// registered or started.
func TestStartExecCreateFailure(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	fd := &fakeDocker{createExecErr: errors.New("boom")}
	c.dockerClient = fd

	c.StartExec(context.Background(), protocol.ExecStartMessage{ExecID: "e1", ContainerID: "c1"})

	var end protocol.ExecEndMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeExecEnd), &end)
	if end.ExecID != "e1" || end.Reason != "create exec failed: boom" {
		t.Errorf("exec_end = %+v, want e1 / create exec failed: boom", end)
	}
	if _, ok := c.execSessions.Load("e1"); ok {
		t.Error("a session was registered despite CreateExec failing")
	}
}

// A StartExec failure (after a successful create) is likewise terminal.
func TestStartExecStartFailure(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	fd := &fakeDocker{createExecID: "docker-1", startExecErr: errors.New("nope")}
	c.dockerClient = fd

	c.StartExec(context.Background(), protocol.ExecStartMessage{ExecID: "e1", ContainerID: "c1"})

	var end protocol.ExecEndMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeExecEnd), &end)
	if end.Reason != "start exec failed: nope" {
		t.Errorf("exec_end reason = %q, want start exec failed: nope", end.Reason)
	}
	if _, ok := c.execSessions.Load("e1"); ok {
		t.Error("a session was registered despite StartExec failing")
	}
}

// HandleResize forwards a resize to the Docker client, targeting the
// Docker-issued exec id (dockerExecID) rather than the controller's protocol
// exec id. The resize is drained asynchronously by the session's inputWriter,
// off the read pump, so the assertion polls.
func TestHandleResizeForwardsToDocker(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	fd := &fakeDocker{}
	c.dockerClient = fd
	s := newReadySession(c, "e1", &fakeConn{})
	s.dockerExecID = "docker-e1" // distinct from the controller execID "e1"

	c.HandleResize(context.Background(), protocol.ExecResizeMessage{ExecID: "e1", Cols: 100, Rows: 30})

	waitFor(t, "resize forwarded to docker", func() bool { return len(fd.resizeCallList()) == 1 })
	if got := fd.resizeCallList(); got[0] != (resizeCall{"docker-e1", 100, 30}) {
		t.Errorf("resize call = %+v, want {docker-e1 100 30} (the Docker exec id, not the controller id)", got)
	}
}

// A transient resize error is retried until it succeeds, on the session's
// inputWriter goroutine (never the read pump). resizeCalls is appended on every
// attempt, so its length is the attempt count.
func TestHandleResizeRetriesUntilSuccess(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	fd := &fakeDocker{resizeFailFirst: 1}
	c.dockerClient = fd
	s := newReadySession(c, "e1", &fakeConn{})
	s.dockerExecID = "docker-e1"

	c.HandleResize(context.Background(), protocol.ExecResizeMessage{ExecID: "e1", Cols: 80, Rows: 24})

	waitFor(t, "resize retried to success", func() bool { return len(fd.resizeCallList()) >= 2 })
	if got := len(fd.resizeCallList()); got != 2 {
		t.Errorf("resize attempts = %d, want 2 (one failure then success)", got)
	}
}
