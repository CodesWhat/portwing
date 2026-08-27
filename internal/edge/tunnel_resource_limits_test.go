package edge

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/protocol"
)

const (
	execInputFrameLimitForTest  = 64 << 10
	execInputQueuedLimitForTest = 1 << 20
)

// gatedExecWriteConn holds the first write in flight until the test releases
// it. That keeps the consumer blocked while concurrent HandleInput calls race
// to reserve queue space.
type gatedExecWriteConn struct {
	*fakeConn
	entered     chan struct{}
	release     chan struct{}
	blockOnce   sync.Once
	releaseOnce sync.Once
}

func newGatedExecWriteConn() *gatedExecWriteConn {
	return &gatedExecWriteConn{
		fakeConn: &fakeConn{},
		entered:  make(chan struct{}),
		release:  make(chan struct{}),
	}
}

func (c *gatedExecWriteConn) Write(p []byte) (int, error) {
	c.blockOnce.Do(func() {
		close(c.entered)
		<-c.release
	})
	return c.fakeConn.Write(p)
}

func (c *gatedExecWriteConn) unblock() {
	c.releaseOnce.Do(func() { close(c.release) })
}

func TestHandleInputRejectsDecodedFrameOverLimit(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	conn := &fakeConn{}
	session := newReadySession(c, "frame-limit", conn)
	t.Cleanup(session.Close)

	oversized := make([]byte, execInputFrameLimitForTest+1)
	c.HandleInput(protocol.ExecInputMessage{
		ExecID: "frame-limit",
		Data:   base64.StdEncoding.EncodeToString(oversized),
	})

	// A small sentinel makes the absence assertion deterministic: once it is
	// written, every earlier accepted frame must already have been written.
	sentinel := []byte("accepted-after-oversized-frame")
	c.HandleInput(protocol.ExecInputMessage{
		ExecID: "frame-limit",
		Data:   base64.StdEncoding.EncodeToString(sentinel),
	})

	waitFor(t, "sentinel input after oversized frame", func() bool {
		got := conn.written()
		return len(got) >= len(sentinel)
	})
	if got := conn.written(); string(got) != string(sentinel) {
		t.Fatalf("exec input wrote %d bytes, want only the %d-byte sentinel; decoded frames over %d bytes must be rejected",
			len(got), len(sentinel), execInputFrameLimitForTest)
	}
}

func TestHandleInputQueuedByteBudgetIsRaceSafeAndReleasedAfterDrain(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	conn := newGatedExecWriteConn()
	t.Cleanup(conn.unblock)
	session := newReadySession(c, "queued-limit", conn)
	t.Cleanup(session.Close)

	frame := make([]byte, execInputFrameLimitForTest)
	encoded := base64.StdEncoding.EncodeToString(frame)
	c.HandleInput(protocol.ExecInputMessage{ExecID: "queued-limit", Data: encoded})

	select {
	case <-conn.entered:
	case <-time.After(readTimeout):
		t.Fatal("first exec input never reached the blocked writer")
	}

	// Submit more frames concurrently than either the byte budget or the
	// channel's count limit can retain. The in-flight blocked write must keep
	// its reservation, and concurrent admissions must not oversubscribe the
	// aggregate budget.
	var senders sync.WaitGroup
	for range execInputQueue {
		senders.Add(1)
		go func() {
			defer senders.Done()
			c.HandleInput(protocol.ExecInputMessage{ExecID: "queued-limit", Data: encoded})
		}()
	}
	senders.Wait()
	conn.unblock()

	// Wait until the full allowed budget has drained, then enqueue a sentinel.
	// Its successful delivery proves drained reservations are released.
	waitFor(t, "allowed exec input budget to drain", func() bool {
		return len(conn.written()) >= execInputQueuedLimitForTest
	})
	sentinel := []byte("reservation-released")
	c.HandleInput(protocol.ExecInputMessage{
		ExecID: "queued-limit",
		Data:   base64.StdEncoding.EncodeToString(sentinel),
	})
	waitFor(t, "input accepted after queued budget drained", func() bool {
		got := conn.written()
		return len(got) >= len(sentinel) && string(got[len(got)-len(sentinel):]) == string(sentinel)
	})

	if got := len(conn.written()); got != execInputQueuedLimitForTest+len(sentinel) {
		t.Fatalf("exec input wrote %d bytes under a blocked consumer, want %d-byte budget plus %d-byte post-drain sentinel",
			got, execInputQueuedLimitForTest, len(sentinel))
	}
}

func TestStartExecRejectsEmptyExecIDBeforeDockerAdmission(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	fd := &fakeDocker{createExecErr: errors.New("empty exec ID reached Docker")}
	c.dockerClient = fd

	c.StartExec(context.Background(), protocol.ExecStartMessage{
		ContainerID: "container-1",
		Cmd:         []string{"sh"},
	})

	var end protocol.ExecEndMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeExecEnd), &end)
	if end.Reason == "" {
		t.Fatal("empty exec ID rejection did not include a reason")
	}

	fd.mu.Lock()
	createCalls := len(fd.createCalls)
	fd.mu.Unlock()
	if createCalls != 0 {
		t.Fatalf("Docker CreateExec calls = %d, want 0 for an empty exec ID", createCalls)
	}
	if _, ok := c.execSessions.Load(""); ok {
		t.Fatal("empty exec ID was registered as a live session")
	}
}

func TestStartExecRejectsDuplicateWithoutReplacingOriginal(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	fd := &fakeDocker{createExecErr: errors.New("duplicate exec ID reached Docker")}
	c.dockerClient = fd
	original := newExecSession(c, "duplicate", &fakeConn{})
	t.Cleanup(original.Close)

	c.StartExec(context.Background(), protocol.ExecStartMessage{
		ExecID:      "duplicate",
		ContainerID: "container-2",
		Cmd:         []string{"sh"},
	})

	var end protocol.ExecEndMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeExecEnd), &end)
	if end.Reason == "" {
		t.Fatal("duplicate exec ID rejection did not include a reason")
	}

	got, ok := c.execSessions.Load("duplicate")
	if !ok {
		t.Fatal("duplicate admission removed the original exec session")
	}
	if got != original {
		t.Fatal("duplicate admission replaced the original exec session")
	}
	select {
	case <-original.done:
		t.Fatal("duplicate admission closed the original exec session")
	default:
	}

	fd.mu.Lock()
	createCalls := len(fd.createCalls)
	fd.mu.Unlock()
	if createCalls != 0 {
		t.Fatalf("Docker CreateExec calls = %d, want 0 for a duplicate exec ID", createCalls)
	}
}

func TestStaleExecCleanupCannotDeleteReplacement(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	stale := newExecSession(c, "reused", &fakeConn{})
	replacement := newExecSession(c, "reused", &fakeConn{})
	t.Cleanup(replacement.Close)

	stale.Close()

	got, ok := c.execSessions.Load("reused")
	if !ok {
		t.Fatal("stale exec cleanup deleted the replacement session")
	}
	if got != replacement {
		t.Fatal("stale exec cleanup changed the replacement session")
	}
}
