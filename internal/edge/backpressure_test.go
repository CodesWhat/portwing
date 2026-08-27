package edge

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/codeswhat/portwing/internal/protocol"
)

const outboundQueuedByteLimitForTest = 128 << 20

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

// A frame-count limit alone still permits a blocked writer to retain several
// gigabytes when queued envelopes are large. Crossing the aggregate byte
// budget must evict the slow connection before the count limit is reached.
func TestSendMessageEvictsConnectionWhenQueuedBytesExceedLimit(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	c.connMu.Lock()
	c.sendCh = make(chan protocol.Envelope, sendQueueSize)
	agentConn := c.conn
	c.connMu.Unlock()

	const frameBytes = 4 << 20
	data := make(json.RawMessage, frameBytes)
	data[0] = '"'
	copy(data[1:], bytes.Repeat([]byte{'x'}, frameBytes-2))
	data[len(data)-1] = '"'

	// No sendPump is running, which models a writer blocked indefinitely.
	// This many frames crosses 128 MiB while remaining far below 256 entries.
	for i := 0; i <= outboundQueuedByteLimitForTest/frameBytes; i++ {
		c.sendMessage(protocol.Envelope{Type: protocol.TypeStream, Data: data})
	}

	if err := agentConn.WriteControl(
		websocket.PingMessage,
		nil,
		time.Now().Add(100*time.Millisecond),
	); err == nil {
		t.Fatal("controller connection remained open after queued outbound bytes exceeded the aggregate limit")
	}
}

func TestOutboundByteReservationIncludesDequeuedFrameUntilRelease(t *testing.T) {
	t.Parallel()

	ch := make(chan protocol.Envelope, sendQueueSize)
	state := &outboundQueueState{}
	data := make(json.RawMessage, 4<<20)
	env := protocol.Envelope{Data: data}

	for i := 0; i < outboundQueuedByteLimitForTest/len(data); i++ {
		if got := state.enqueue(ch, env); got != outboundEnqueued {
			t.Fatalf("enqueue %d = %d, want outboundEnqueued", i, got)
		}
	}
	inFlight := <-ch
	if got := state.enqueue(ch, protocol.Envelope{Data: json.RawMessage(`x`)}); got != outboundByteLimitExceeded {
		t.Fatalf("enqueue while one frame is in flight = %d, want outboundByteLimitExceeded", got)
	}

	state.release(inFlight)
	if got := state.enqueue(ch, protocol.Envelope{Data: json.RawMessage(`x`)}); got != outboundEnqueued {
		t.Fatalf("enqueue after in-flight release = %d, want outboundEnqueued", got)
	}
}

// A sender may retain a reference to a connection queue while its pump is
// exiting. Closing the queue state must remain authoritative for that old
// generation; a late send cannot recreate an open accounting state and leave
// an unbounded, undrained queue behind.
func TestSendMessageCannotResurrectClosedQueueGeneration(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	ch := make(chan protocol.Envelope, sendQueueSize)
	state := &outboundQueueState{}
	c.connMu.Lock()
	c.sendCh = ch
	c.sendState = state
	c.connMu.Unlock()

	state.closeAndDiscard(ch)
	c.sendMessage(protocol.Envelope{Type: protocol.TypePong, Data: json.RawMessage(`{}`)})

	if got := len(ch); got != 0 {
		t.Fatalf("closed queue retained %d late frames, want 0", got)
	}
	if err := ctrl.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
		t.Fatalf("set controller read deadline: %v", err)
	}
	if _, _, err := ctrl.ReadMessage(); err == nil {
		t.Fatal("connection remained open after a late send targeted its closed queue generation")
	}
}

func TestFailConnCannotEvictReplacementGeneration(t *testing.T) {
	t.Parallel()

	c, oldController := newTestClient(t)
	c.connMu.Lock()
	oldAgent := c.conn
	c.connMu.Unlock()

	newAgent, newController := newWSPair(t)
	c.connMu.Lock()
	c.conn = newAgent
	c.connMu.Unlock()

	c.failConn(oldAgent, "late failure from old queue")

	if err := oldController.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
		t.Fatalf("set old controller read deadline: %v", err)
	}
	if _, _, err := oldController.ReadMessage(); err == nil {
		t.Fatal("old connection remained open after its generation failed")
	}
	if err := newAgent.SetWriteDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatalf("set replacement write deadline: %v", err)
	}
	if err := newAgent.WriteJSON(protocol.Envelope{Type: protocol.TypePong}); err != nil {
		t.Fatalf("replacement connection was evicted by old generation failure: %v", err)
	}
	if err := newController.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatalf("set replacement controller read deadline: %v", err)
	}
	var got protocol.Envelope
	if err := newController.ReadJSON(&got); err != nil || got.Type != protocol.TypePong {
		t.Fatalf("replacement connection frame = type %q, error %v", got.Type, err)
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
