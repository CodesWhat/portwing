package edge

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/codeswhat/portwing/internal/protocol"
)

// runReadPump starts the read pump against the test client and returns a cancel
// func. The pump exits when the context is cancelled or the conn closes (test
// cleanup closes both ends).
func runReadPump(t *testing.T, c *Client) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = c.readPump(ctx) }()
	t.Cleanup(cancel)
	return cancel
}

// sendEnvelope marshals data into an envelope and sends it from the controller
// to the agent.
func sendEnvelope(t *testing.T, ctrl *websocket.Conn, msgType string, data any) {
	t.Helper()
	env := protocol.Envelope{Type: msgType}
	if data != nil {
		raw, err := json.Marshal(data)
		if err != nil {
			t.Fatalf("marshal %s: %v", msgType, err)
		}
		env.Data = raw
	}
	if err := ctrl.WriteJSON(env); err != nil {
		t.Fatalf("write %s: %v", msgType, err)
	}
}

// A ping from the controller is answered with a pong that echoes the timestamp.
func TestReadPumpAnswersPingWithPong(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	runReadPump(t, c)

	sendEnvelope(t, ctrl, protocol.TypePing, protocol.PingMessage{Timestamp: 12345})

	var pong protocol.PongMessage
	decodeData(t, expectType(t, ctrl, protocol.TypePong), &pong)
	if pong.Timestamp != 12345 {
		t.Errorf("pong timestamp = %d, want 12345", pong.Timestamp)
	}
}

// A malformed envelope is skipped and the read loop keeps serving — proven by a
// subsequent ping still drawing a pong.
func TestReadPumpSkipsMalformedEnvelopeAndKeepsServing(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	runReadPump(t, c)

	if err := ctrl.WriteMessage(websocket.TextMessage, []byte("{ not valid json")); err != nil {
		t.Fatalf("write garbage: %v", err)
	}

	sendEnvelope(t, ctrl, protocol.TypePing, protocol.PingMessage{Timestamp: 7})
	expectType(t, ctrl, protocol.TypePong)
}

// Once the in-flight request semaphore is saturated, further requests are
// rejected with an error rather than blocking the read loop — the backpressure
// guarantee for tunneled request fan-out.
func TestReadPumpRejectsRequestsWhenStreamLimitReached(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)

	// Saturate the stream semaphore so the dispatch hits the default (reject)
	// branch without ever reaching the Docker-backed request handler.
	for i := 0; i < maxStreams; i++ {
		c.streamSem <- struct{}{}
	}

	runReadPump(t, c)

	sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
		RequestID: "req-1",
		Method:    "GET",
		Path:      "/containers/json",
	})

	var errMsg protocol.ErrorMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeError), &errMsg)
	if errMsg.RequestID != "req-1" {
		t.Errorf("error RequestID = %q, want req-1", errMsg.RequestID)
	}
	if errMsg.Message == "" {
		t.Error("rejection carried no message")
	}
}

// Exec control messages for an unknown session are dispatched without crashing
// the read loop; liveness is confirmed by a following ping/pong.
func TestReadPumpDispatchesExecControlForUnknownSession(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	runReadPump(t, c)

	sendEnvelope(t, ctrl, protocol.TypeExecInput, protocol.ExecInputMessage{ExecID: "ghost", Data: "Zm9v"})
	sendEnvelope(t, ctrl, protocol.TypeExecResize, protocol.ExecResizeMessage{ExecID: "ghost", Cols: 80, Rows: 24})
	sendEnvelope(t, ctrl, protocol.TypeExecEnd, protocol.ExecEndMessage{ExecID: "ghost"})

	sendEnvelope(t, ctrl, protocol.TypePing, protocol.PingMessage{Timestamp: 99})
	var pong protocol.PongMessage
	decodeData(t, expectType(t, ctrl, protocol.TypePong), &pong)
	if pong.Timestamp != 99 {
		t.Errorf("pong timestamp = %d, want 99", pong.Timestamp)
	}
}
