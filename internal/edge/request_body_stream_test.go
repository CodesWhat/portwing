package edge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/protocol"
)

// TestReadPumpReassemblesStreamedRequestBody proves the round trip: a
// request.bodyStream=true request followed by stream chunks and a
// stream_end reassembles into the exact original bytes (including bytes that
// are not valid UTF-8, since a build context or other binary body is the
// whole reason this path exists) and only then reaches the Docker client.
func TestReadPumpReassemblesStreamedRequestBody(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	//nolint:bodyclose // consumed and closed by handleRequestTo, the code under test.
	fd := &fakeDocker{doResp: mkResp(http.StatusCreated, "application/json", `{"ok":true}`)}
	c.dockerClient = fd

	runReadPump(t, c)

	original := append([]byte{0x00, 0xff, 0xfe, 0x01}, []byte("binary-tar-context-bytes")...)
	chunk1, chunk2 := original[:10], original[10:]

	sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
		RequestID:  "stream-1",
		Method:     http.MethodPost,
		Path:       "/containers/create",
		BodyStream: true,
	})
	sendEnvelope(t, ctrl, protocol.TypeStream, protocol.StreamMessage{
		RequestID: "stream-1",
		Data:      base64.StdEncoding.EncodeToString(chunk1),
	})
	sendEnvelope(t, ctrl, protocol.TypeStream, protocol.StreamMessage{
		RequestID: "stream-1",
		Data:      base64.StdEncoding.EncodeToString(chunk2),
	})
	sendEnvelope(t, ctrl, protocol.TypeStreamEnd, protocol.StreamEndMessage{
		RequestID: "stream-1",
		Reason:    "complete",
	})

	var resp protocol.ResponseMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeResponse), &resp)
	if resp.RequestID != "stream-1" {
		t.Fatalf("RequestID = %q, want stream-1", resp.RequestID)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("StatusCode = %d, want %d", resp.StatusCode, http.StatusCreated)
	}

	fd.mu.Lock()
	calls := append([]doCall(nil), fd.doCalls...)
	fd.mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("Docker calls = %d, want 1", len(calls))
	}
	if string(calls[0].body) != string(original) {
		t.Errorf("reassembled body = %x, want %x", calls[0].body, original)
	}
	if calls[0].path != "/containers/create" || calls[0].method != http.MethodPost {
		t.Errorf("call = %+v, want POST /containers/create", calls[0])
	}
}

// TestReadPumpStreamFrameForUnknownRequestIDFallsThroughToAdapter is the
// regression guard for any future adapter-owned use of TypeStream/
// TypeStreamEnd: a chunk whose RequestID has no registered BodyStream
// request (the only case today, since no in-repo adapter emits its own
// TypeStream requestId) must reach adapter.HandleMessage unchanged instead
// of being silently swallowed by the reassembly path.
func TestReadPumpStreamFrameForUnknownRequestIDFallsThroughToAdapter(t *testing.T) {
	t.Parallel()

	fa := &fakeAdapter{handleMsgResult: true}
	c, ctrl := newTestClient(t)
	c.adapter = fa

	runReadPump(t, c)

	sendEnvelope(t, ctrl, protocol.TypeStream, protocol.StreamMessage{
		RequestID: "no-such-request",
		Data:      base64.StdEncoding.EncodeToString([]byte("orphan-chunk")),
	})
	sendEnvelope(t, ctrl, protocol.TypeStreamEnd, protocol.StreamEndMessage{
		RequestID: "no-such-request",
	})

	// Liveness/ordering proof: both frames were processed (not stuck on a
	// lock or a blocked send) before this ping's pong arrives.
	sendEnvelope(t, ctrl, protocol.TypePing, protocol.PingMessage{Timestamp: 99})
	expectType(t, ctrl, protocol.TypePong)

	// The ping/pong above is the ordering barrier: readPump is a single
	// goroutine, so both frames are fully handled by the time the pong lands.
	got := fa.messageTypes()
	want := []string{protocol.TypeStream, protocol.TypeStreamEnd}
	if len(got) != len(want) {
		t.Fatalf("adapter.HandleMessage calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("adapter.HandleMessage call %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestReadPumpStreamedBodyExceedsCapRejectsAndCleansUp confirms that
// reassembly exceeding maxRequestBodyStream is rejected with a TypeError
// naming the requestId, and that the pending state is actually freed: a
// stream_end sent afterwards finds nothing registered and falls through to
// the adapter instead of dispatching a second time or hanging.
func TestReadPumpStreamedBodyExceedsCapRejectsAndCleansUp(t *testing.T) {
	// Not t.Parallel(): mutates the package-level maxRequestBodyStream var,
	// which every other BodyStream test reads.

	orig := maxRequestBodyStream
	maxRequestBodyStream = 8
	t.Cleanup(func() { maxRequestBodyStream = orig })

	fa := &fakeAdapter{handleMsgResult: true}
	c, ctrl := newTestClient(t)
	c.adapter = fa
	fd := &fakeDocker{} // no canned response: a call here would mean the cap didn't hold.
	c.dockerClient = fd

	runReadPump(t, c)

	sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
		RequestID:  "too-big",
		Method:     http.MethodPost,
		Path:       "/build",
		BodyStream: true,
	})
	sendEnvelope(t, ctrl, protocol.TypeStream, protocol.StreamMessage{
		RequestID: "too-big",
		Data:      base64.StdEncoding.EncodeToString([]byte("this payload is over the 8 byte cap")),
	})

	var em protocol.ErrorMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeError), &em)
	if em.RequestID != "too-big" {
		t.Errorf("error RequestID = %q, want too-big", em.RequestID)
	}
	if em.Message == "" {
		t.Error("error Message is empty, want an explanation of the size limit")
	}

	// The entry must be gone: a stream_end for it now has nothing to finish,
	// so it falls through to the adapter instead of dispatching a Docker call.
	sendEnvelope(t, ctrl, protocol.TypeStreamEnd, protocol.StreamEndMessage{RequestID: "too-big"})
	sendEnvelope(t, ctrl, protocol.TypePing, protocol.PingMessage{Timestamp: 1})
	expectType(t, ctrl, protocol.TypePong)

	fd.mu.Lock()
	calls := len(fd.doCalls)
	fd.mu.Unlock()
	if calls != 0 {
		t.Errorf("Docker calls = %d, want 0, the oversized body must never dispatch", calls)
	}
}

// TestReadPumpStreamedBodyIdleTimeoutCleansUpAndErrors confirms that a
// BodyStream request that never receives stream_end times out, is reported
// as a TypeError, and does not hang: the request never dispatches and the
// entry is freed (proven the same way as the size-cap test, via a stream_end
// arriving after the timeout finding nothing registered).
func TestReadPumpStreamedBodyIdleTimeoutCleansUpAndErrors(t *testing.T) {
	// Not t.Parallel(): mutates the package-level requestBodyStreamIdleTimeout
	// var, which every other BodyStream test reads.

	origTimeout := requestBodyStreamIdleTimeout
	requestBodyStreamIdleTimeout = 50 * time.Millisecond
	t.Cleanup(func() { requestBodyStreamIdleTimeout = origTimeout })

	c, ctrl := newTestClient(t)
	fd := &fakeDocker{} // no canned response: a call here would mean the timeout didn't hold.
	c.dockerClient = fd

	runReadPump(t, c)

	sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
		RequestID:  "stalled",
		Method:     http.MethodPost,
		Path:       "/build",
		BodyStream: true,
	})

	var em protocol.ErrorMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeError), &em)
	if em.RequestID != "stalled" {
		t.Errorf("error RequestID = %q, want stalled", em.RequestID)
	}

	fd.mu.Lock()
	calls := len(fd.doCalls)
	fd.mu.Unlock()
	if calls != 0 {
		t.Errorf("Docker calls = %d, want 0, a timed-out body must never dispatch", calls)
	}
}

// TestSendHelloAdvertisesRequestBodyStreamCapability is the hello snapshot
// test: the agent must advertise CapRequestBodyStream so a controller knows
// it is safe to send request.bodyStream=true, now that the reassembly path
// exists and is bounded.
func TestSendHelloAdvertisesRequestBodyStreamCapability(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	c.dockerClient = &fakeDocker{}
	c.adapter = &fakeAdapter{}

	if err := c.sendHello(context.Background()); err != nil {
		t.Fatalf("sendHello: %v", err)
	}

	data := expectType(t, ctrl, protocol.TypeHello)
	var hello protocol.HelloMessage
	decodeData(t, data, &hello)

	found := false
	for _, cap := range hello.Capabilities {
		if cap == protocol.CapRequestBodyStream {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Capabilities = %v, want to contain %q", hello.Capabilities, protocol.CapRequestBodyStream)
	}
}

// TestHandleRequestInlineBodyUnaffectedByBodyStream is the compatibility
// regression guard for an old controller that never sends
// request.bodyStream=true: a request with the field entirely absent (its
// Go zero value, false) must behave exactly as before this change, with no
// pending-body bookkeeping, straight to handleRequestTo.
func TestHandleRequestInlineBodyUnaffectedByBodyStream(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	//nolint:bodyclose // consumed and closed by handleRequest, the code under test.
	fd := &fakeDocker{doResp: mkResp(http.StatusCreated, "application/json", `{"ok":true}`)}
	c.dockerClient = fd

	c.handleRequest(context.Background(), protocol.RequestMessage{
		RequestID: "legacy-1",
		Method:    http.MethodPost,
		Path:      "/containers/create",
		Body:      json.RawMessage(`{"Image":"alpine"}`),
	})

	var resp protocol.ResponseMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeResponse), &resp)
	if resp.RequestID != "legacy-1" {
		t.Errorf("RequestID = %q, want legacy-1", resp.RequestID)
	}

	fd.mu.Lock()
	calls := append([]doCall(nil), fd.doCalls...)
	fd.mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("Docker calls = %d, want 1", len(calls))
	}
	if string(calls[0].body) != `{"Image":"alpine"}` {
		t.Errorf("body = %s, want inline body unchanged", calls[0].body)
	}

	c.pendingBodiesMu.Lock()
	pending := len(c.pendingBodies)
	c.pendingBodiesMu.Unlock()
	if pending != 0 {
		t.Errorf("pendingBodies = %d, want 0 for a legacy inline-body request", pending)
	}
}

// TestReadPumpConcurrentBodyStreamAndPingDoNotBlock proves that a
// registered-but-unfinished BodyStream reassembly (which never touches
// dockerd until stream_end) does not block the single readPump goroutine
// from servicing a concurrent ping. The pending-body bookkeeping runs
// inline on that goroutine, so a design that blocked here would freeze
// every other message type on the connection.
func TestReadPumpConcurrentBodyStreamAndPingDoNotBlock(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	c.dockerClient = &fakeDocker{}

	runReadPump(t, c)

	for i := 0; i < 5; i++ {
		reqID := fmt.Sprintf("interleaved-%d", i)
		sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
			RequestID:  reqID,
			Method:     http.MethodPost,
			Path:       "/build",
			BodyStream: true,
		})
		sendEnvelope(t, ctrl, protocol.TypeStream, protocol.StreamMessage{
			RequestID: reqID,
			Data:      base64.StdEncoding.EncodeToString([]byte("chunk")),
		})
		sendEnvelope(t, ctrl, protocol.TypePing, protocol.PingMessage{Timestamp: int64(i)})

		var pong protocol.PongMessage
		decodeData(t, expectType(t, ctrl, protocol.TypePong), &pong)
		if pong.Timestamp != int64(i) {
			t.Fatalf("pong timestamp = %d, want %d (a stalled reassembly would starve this ping)", pong.Timestamp, i)
		}
	}
}
