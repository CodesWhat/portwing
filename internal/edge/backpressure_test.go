package edge

import (
	"context"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/codeswhat/portwing/internal/protocol"
)

// runSendPump creates the per-connection send queue and starts the sendPump
// against the test client, returning the channel so a test can observe/fill it.
// The pump is torn down via context cancellation registered as a test cleanup.
func runSendPump(t *testing.T, c *Client) chan protocol.Envelope {
	t.Helper()
	ch := make(chan protocol.Envelope, sendQueueSize)
	c.connMu.Lock()
	c.sendCh = ch
	conn := c.conn
	c.connMu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	go c.sendPump(ctx, conn, ch)
	t.Cleanup(cancel)
	return ch
}

// TestSendPumpDeliversQueuedFrame proves that the queued send path (sendCh set)
// delivers a frame end-to-end: sendTypedMessage enqueues the envelope, the
// sendPump dequeues and writes it over the WebSocket, and the controller reads
// back the exact content.
func TestSendPumpDeliversQueuedFrame(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	runSendPump(t, c)

	const ts = int64(42)
	if err := c.sendTypedMessage(protocol.TypePong, protocol.PongMessage{Timestamp: ts}); err != nil {
		t.Fatalf("sendTypedMessage: %v", err)
	}

	var pong protocol.PongMessage
	decodeData(t, expectType(t, ctrl, protocol.TypePong), &pong)
	if pong.Timestamp != ts {
		t.Errorf("pong.Timestamp = %d, want %d", pong.Timestamp, ts)
	}
}

// TestSendMessageEvictsConnectionWhenQueueFull pins the core backpressure
// invariant: when sendCh is full and no pump is draining it, the next
// sendMessage call takes the default branch and calls failConn, which closes
// the agent-side WebSocket. The controller observes the close as a read error,
// proving eviction rather than silent frame drop or deadlock.
func TestSendMessageEvictsConnectionWhenQueueFull(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)

	// Install a capacity-1 queue (no pump running — nobody drains it).
	c.connMu.Lock()
	c.sendCh = make(chan protocol.Envelope, 1)
	c.connMu.Unlock()

	// Fill the queue to capacity so the next send hits the default branch.
	c.sendCh <- protocol.Envelope{Type: protocol.TypePing}

	// This send must not block; it must call failConn and close the agent conn.
	c.sendMessage(protocol.Envelope{Type: protocol.TypePing})

	// The controller should see the connection torn down. Give it up to
	// readTimeout to propagate.
	if err := ctrl.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
		t.Fatalf("set ctrl read deadline: %v", err)
	}
	_, _, err := ctrl.ReadMessage()
	if err == nil {
		t.Fatal("expected read error after eviction, got nil")
	}
}

// TestSendPumpEvictsOnWriteFailure verifies that a write error inside the
// sendPump causes failConn to be called and the agent-side connection to be
// closed. We induce the write failure by closing the controller end first; the
// sendPump's WriteJSON then fails, which must trigger failConn. The test
// confirms eviction by waiting until the agent conn itself becomes unusable.
func TestSendPumpEvictsOnWriteFailure(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	runSendPump(t, c)

	// Capture the agent conn before eviction so we can probe it afterwards.
	c.connMu.Lock()
	agentConn := c.conn
	c.connMu.Unlock()

	// Closing the controller makes every subsequent WriteJSON from the agent
	// fail because the peer has gone away.
	if err := ctrl.Close(); err != nil {
		t.Fatalf("close ctrl: %v", err)
	}

	// Enqueue a frame; the sendPump will try to write it and fail.
	if err := c.sendTypedMessage(protocol.TypePong, protocol.PongMessage{Timestamp: 1}); err != nil {
		t.Fatalf("sendTypedMessage: %v", err)
	}

	// Wait until failConn has propagated and the agent connection is unusable.
	waitFor(t, "agent conn evicted", func() bool {
		err := agentConn.WriteControl(
			websocket.PingMessage,
			nil,
			time.Now().Add(10*time.Millisecond),
		)
		return err != nil
	})
}

// TestSendMessageDirectWriteWhenNoQueue documents that the handshake (nil
// sendCh) code path remains intact: with sendCh left nil (as newTestClient
// always leaves it), sendTypedMessage writes directly to the WebSocket and the
// controller receives the frame. Every existing dispatch test relies on this
// behaviour implicitly; this test makes it an explicit contract.
func TestSendMessageDirectWriteWhenNoQueue(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	// sendCh is nil — newTestClient does not set it.

	const ts = int64(7)
	if err := c.sendTypedMessage(protocol.TypePong, protocol.PongMessage{Timestamp: ts}); err != nil {
		t.Fatalf("sendTypedMessage: %v", err)
	}

	var pong protocol.PongMessage
	decodeData(t, expectType(t, ctrl, protocol.TypePong), &pong)
	if pong.Timestamp != ts {
		t.Errorf("pong.Timestamp = %d, want %d", pong.Timestamp, ts)
	}
}
