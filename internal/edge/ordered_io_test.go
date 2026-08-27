package edge

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/protocol"
)

// TestOrderedIOEarlyInputBufferedDuringBringUp is the core regression test for
// the ordered-exec-I/O fix. Input that arrives while the Docker exec is still
// being brought up must be buffered in order and delivered once the exec is live,
// not dropped because the session "isn't ready yet."
func TestOrderedIOEarlyInputBufferedDuringBringUp(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)

	// startGate holds StartExec open so we can send exec_input before the exec
	// connection is live. blockRead keeps the readLoop alive while inputWriter
	// drains, so done isn't raced closed before the write completes.
	gate := make(chan struct{})
	readBlock := make(chan struct{})
	execConn := &fakeConn{blockRead: readBlock}
	fd := &fakeDocker{
		createExecID: "docker-1",
		startConn:    execConn,
		startGate:    gate,
	}
	c.dockerClient = fd
	t.Cleanup(func() { close(readBlock) }) // unblock readLoop so it exits cleanly

	runReadPump(t, c)

	// Send exec_start. StartExec registers the session synchronously and spawns
	// bringUpExec, which will block on startGate.
	sendEnvelope(t, ctrl, protocol.TypeExecStart, protocol.ExecStartMessage{
		ExecID:      "ord-1",
		ContainerID: "ctr-1",
		Cmd:         []string{"sh"},
	})

	// Send exec_input while the exec is still being brought up. The session is
	// already registered, so HandleInput can enqueue it immediately.
	payload := "echo hi\n"
	sendEnvelope(t, ctrl, protocol.TypeExecInput, protocol.ExecInputMessage{
		ExecID: "ord-1",
		Data:   base64.StdEncoding.EncodeToString([]byte(payload)),
	})

	// Release the gate — bringUpExec can now complete.
	close(gate)

	// exec_ready must arrive once the exec is live.
	var ready protocol.ExecReadyMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeExecReady), &ready)
	if ready.ExecID != "ord-1" {
		t.Errorf("exec_ready ExecID = %q, want ord-1", ready.ExecID)
	}

	// The buffered input must have been written to the exec conn in order.
	waitFor(t, "early input to be written to exec conn", func() bool {
		return string(execConn.written()) == payload
	})
}

// TestOrderedIOFIFOMultipleEarlyInputs sends three input frames before the exec
// is live and verifies they arrive at the conn concatenated in the original order.
func TestOrderedIOFIFOMultipleEarlyInputs(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)

	gate := make(chan struct{})
	// blockRead keeps readLoop alive while inputWriter drains all three chunks so
	// done isn't closed mid-drain and the select in inputWriter picks the wrong branch.
	readBlock := make(chan struct{})
	execConn := &fakeConn{blockRead: readBlock}
	fd := &fakeDocker{
		createExecID: "docker-fifo",
		startConn:    execConn,
		startGate:    gate,
	}
	c.dockerClient = fd
	t.Cleanup(func() { close(readBlock) })

	runReadPump(t, c)

	sendEnvelope(t, ctrl, protocol.TypeExecStart, protocol.ExecStartMessage{
		ExecID:      "ord-fifo",
		ContainerID: "ctr-fifo",
		Cmd:         []string{"sh"},
	})

	// Three frames enqueued before the gate is released.
	for _, chunk := range []string{"a", "b", "c"} {
		sendEnvelope(t, ctrl, protocol.TypeExecInput, protocol.ExecInputMessage{
			ExecID: "ord-fifo",
			Data:   base64.StdEncoding.EncodeToString([]byte(chunk)),
		})
	}

	close(gate)

	var ready protocol.ExecReadyMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeExecReady), &ready)
	if ready.ExecID != "ord-fifo" {
		t.Errorf("exec_ready ExecID = %q, want ord-fifo", ready.ExecID)
	}

	waitFor(t, "all three chunks written in order", func() bool {
		return string(execConn.written()) == "abc"
	})
}

// TestOrderedIOInputAfterCloseIsDropped confirms that HandleInput on a closed
// session is a no-op: the conn's written bytes don't grow and no panic occurs.
func TestOrderedIOInputAfterCloseIsDropped(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	// blockRead so the readLoop doesn't interfere with the session state we're testing.
	readBlock := make(chan struct{})
	conn := &fakeConn{blockRead: readBlock}
	session := newReadySession(c, "ord-closed", conn)
	t.Cleanup(func() { close(readBlock) })

	// Close the session, then try to send more input.
	session.Close()

	// written() baseline before the post-close HandleInput.
	before := len(conn.written())

	c.HandleInput(protocol.ExecInputMessage{
		ExecID: "ord-closed",
		Data:   base64.StdEncoding.EncodeToString([]byte("should not arrive")),
	})

	// Session is deregistered, so HandleInput finds no session and is a no-op.
	after := len(conn.written())
	if after != before {
		t.Errorf("conn grew by %d bytes after close; want 0", after-before)
	}
}

// TestOrderedIOTeardownDuringBringUpCancelsStart verifies that tearing down a
// session cancels an in-flight Docker exec-start request promptly. The start
// gate is never released during the assertion: StartExec must return because
// its context was canceled, not because the simulated Docker daemon answered.
func TestOrderedIOTeardownDuringBringUpCancelsStart(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)

	gate := make(chan struct{})
	t.Cleanup(func() { close(gate) })
	startReturned := make(chan struct{})
	fd := &fakeDocker{
		createExecID:  "docker-orphan",
		startConn:     &fakeConn{},
		startGate:     gate,
		startReturned: startReturned,
	}
	c.dockerClient = fd

	// StartExec registers the session synchronously and spawns bringUpExec,
	// which will block inside fakeDocker.StartExec at the gate.
	c.StartExec(context.Background(), protocol.ExecStartMessage{
		ExecID:      "ord-orphan",
		ContainerID: "ctr-orphan",
		Cmd:         []string{"sh"},
	})
	waitFor(t, "Docker StartExec to reach its gate", func() bool {
		fd.mu.Lock()
		defer fd.mu.Unlock()
		return len(fd.startCalls) == 1
	})

	// Tear the session down while Docker is still waiting. Canceling the
	// session context must release StartExec without the test opening gate.
	c.EndExec(protocol.ExecEndMessage{ExecID: "ord-orphan"})

	// Confirm the session is deregistered (Close() ran its once.Do, which sets
	// closed=true before deleting from the map, so activate will see closed=true).
	if _, ok := c.execSessions.Load("ord-orphan"); ok {
		t.Fatal("session still registered after EndExec; cannot proceed with gate release")
	}

	select {
	case <-startReturned:
	case <-time.After(time.Second):
		t.Fatal("Docker StartExec did not return promptly after session teardown")
	}
}
