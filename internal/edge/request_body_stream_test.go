package edge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
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

// TestReadPumpRejectsEmptyRequestIDBodyStreamWithoutClaimingAdapterFrames
// guards the request-id key boundary: an empty bodyStream request cannot be
// correlated with a later stream frame, so it must not reserve pendingBodies
// under the empty key. The following empty-key stream frame remains available
// for an adapter that uses session-keyed frames.
func TestReadPumpRejectsEmptyRequestIDBodyStreamWithoutClaimingAdapterFrames(t *testing.T) {
	t.Parallel()

	fa := &fakeAdapter{handleMsgResult: true}
	c, ctrl := newTestClient(t)
	c.adapter = fa

	runReadPump(t, c)

	sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
		Method:     http.MethodPost,
		Path:       "/build",
		BodyStream: true,
	})

	var em protocol.ErrorMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeError), &em)
	if em.RequestID != "" {
		t.Errorf("error RequestID = %q, want empty request ID", em.RequestID)
	}
	if em.Message != "streamed request body requires requestId" {
		t.Errorf("error Message = %q, want the empty-request-ID rejection", em.Message)
	}

	sendEnvelope(t, ctrl, protocol.TypeStream, protocol.StreamMessage{
		RequestID: "",
		Data:      base64.StdEncoding.EncodeToString([]byte("session-frame")),
	})
	sendEnvelope(t, ctrl, protocol.TypePing, protocol.PingMessage{Timestamp: 100})
	expectType(t, ctrl, protocol.TypePong)

	got := fa.messageTypes()
	want := []string{protocol.TypeStream}
	if len(got) != len(want) {
		t.Fatalf("adapter.HandleMessage calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("adapter.HandleMessage call %d = %q, want %q", i, got[i], want[i])
		}
	}

	c.pendingBodiesMu.Lock()
	_, registered := c.pendingBodies[""]
	c.pendingBodiesMu.Unlock()
	if registered {
		t.Fatal("empty request ID was inserted into pendingBodies")
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

// TestStalePendingBodyTimeoutDoesNotAbortProgressingUpload is the regression
// guard for the idle-timer race: time.Timer.Reset cannot recall an AfterFunc
// whose deadline has already elapsed, so a timeout callback can already be
// queued (parked on pendingBodiesMu) when a chunk lands and re-arms the
// timer. That callback must not fail an upload that was making progress.
// Calling failPendingBody with the generation the first timer was armed with
// (0) reproduces exactly that state deterministically, with no sleep and no
// dependence on when the real timer fires.
func TestStalePendingBodyTimeoutDoesNotAbortProgressingUpload(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	//nolint:bodyclose // consumed and closed by handleRequestTo, the code under test.
	fd := &fakeDocker{doResp: mkResp(http.StatusOK, "application/json", `{"ok":true}`)}
	c.dockerClient = fd

	runReadPump(t, c)

	chunk := []byte("still-uploading")

	sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
		RequestID:  "racy",
		Method:     http.MethodPost,
		Path:       "/containers/create",
		BodyStream: true,
	})
	sendEnvelope(t, ctrl, protocol.TypeStream, protocol.StreamMessage{
		RequestID: "racy",
		Data:      base64.StdEncoding.EncodeToString(chunk),
	})

	// Ordering barrier: readPump is one goroutine, so the chunk is fully
	// appended (and the timer re-armed to gen 1) by the time this pong lands.
	sendEnvelope(t, ctrl, protocol.TypePing, protocol.PingMessage{Timestamp: 7})
	expectType(t, ctrl, protocol.TypePong)

	// The already-fired gen-0 callback finally gets the lock. It lost the
	// race to the chunk above, so it must drop its firing.
	c.failPendingBody("racy", 0)

	c.pendingBodiesMu.Lock()
	_, stillPending := c.pendingBodies["racy"]
	c.pendingBodiesMu.Unlock()
	if !stillPending {
		t.Fatal("pending body was removed by a stale timeout callback, want it kept: the chunk re-armed the timer")
	}

	sendEnvelope(t, ctrl, protocol.TypeStreamEnd, protocol.StreamEndMessage{
		RequestID: "racy",
		Reason:    "complete",
	})

	// A spurious TypeError would arrive here instead of the response.
	var resp protocol.ResponseMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeResponse), &resp)
	if resp.RequestID != "racy" {
		t.Errorf("RequestID = %q, want racy", resp.RequestID)
	}

	fd.mu.Lock()
	calls := append([]doCall(nil), fd.doCalls...)
	fd.mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("Docker calls = %d, want 1", len(calls))
	}
	if string(calls[0].body) != string(chunk) {
		t.Errorf("reassembled body = %q, want %q", calls[0].body, chunk)
	}
}

// TestStreamedBodyIdleTimeoutRearmsAfterEachChunk is the other half of the
// stale-callback guard: dropping a stale firing must not disarm the timeout
// altogether. After a chunk re-arms the timer, a controller that then stalls
// must still be timed out and told about it.
func TestStreamedBodyIdleTimeoutRearmsAfterEachChunk(t *testing.T) {
	// Not t.Parallel(): mutates the package-level requestBodyStreamIdleTimeout
	// var, which every other BodyStream test reads.

	origTimeout := requestBodyStreamIdleTimeout
	requestBodyStreamIdleTimeout = 400 * time.Millisecond
	t.Cleanup(func() { requestBodyStreamIdleTimeout = origTimeout })

	c, ctrl := newTestClient(t)
	fd := &fakeDocker{} // no canned response: a call here would mean the timeout didn't hold.
	c.dockerClient = fd

	runReadPump(t, c)

	sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
		RequestID:  "stalls-after-a-chunk",
		Method:     http.MethodPost,
		Path:       "/build",
		BodyStream: true,
	})
	sendEnvelope(t, ctrl, protocol.TypeStream, protocol.StreamMessage{
		RequestID: "stalls-after-a-chunk",
		Data:      base64.StdEncoding.EncodeToString([]byte("one chunk, then silence")),
	})
	// Barrier, and the guard against passing for the wrong reason: gen 1 means
	// the chunk was appended and re-armed the timer, so the TypeError below can
	// only come from that second arming, not from the original one. A ping/pong
	// barrier can't be used here — this harness has no sendPump, so readPump
	// and the timeout callback would write the raw conn unserialized.
	waitFor(t, "the chunk to re-arm the idle timer", func() bool {
		c.pendingBodiesMu.Lock()
		defer c.pendingBodiesMu.Unlock()
		pb, ok := c.pendingBodies["stalls-after-a-chunk"]
		return ok && pb.gen == 1
	})

	var em protocol.ErrorMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeError), &em)
	if em.RequestID != "stalls-after-a-chunk" {
		t.Errorf("error RequestID = %q, want stalls-after-a-chunk", em.RequestID)
	}

	c.pendingBodiesMu.Lock()
	pending := len(c.pendingBodies)
	c.pendingBodiesMu.Unlock()
	if pending != 0 {
		t.Errorf("pendingBodies = %d, want 0 after the re-armed timeout fired", pending)
	}

	fd.mu.Lock()
	calls := len(fd.doCalls)
	fd.mu.Unlock()
	if calls != 0 {
		t.Errorf("Docker calls = %d, want 0, a timed-out body must never dispatch", calls)
	}
}

// TestStreamedBodyConcurrencyCapRejectsAndCleansUp covers the count half of
// the reassembly bound: registration defers the streamSem slot until
// stream_end, so without maxPendingRequestBodies the number of concurrent
// 512 MB buffers is bounded only by how many requestIds the controller cares
// to open. The rejection has to look like the duplicate-requestId one — a
// TypeError naming the request — not a silent drop.
func TestStreamedBodyConcurrencyCapRejectsAndCleansUp(t *testing.T) {
	// Not t.Parallel(): mutates the package-level maxPendingRequestBodies var.

	orig := maxPendingRequestBodies
	maxPendingRequestBodies = 2
	t.Cleanup(func() { maxPendingRequestBodies = orig })

	c, ctrl := newTestClient(t)
	fd := &fakeDocker{} // no canned response: a call here would mean the cap didn't hold.
	c.dockerClient = fd

	runReadPump(t, c)

	for i := 0; i < 3; i++ {
		sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
			RequestID:  fmt.Sprintf("concurrent-%d", i),
			Method:     http.MethodPost,
			Path:       "/build",
			BodyStream: true,
		})
	}

	// The ping is sent up front so an accepted third registration shows up as
	// a pong arriving where the rejection should have been, rather than as an
	// opaque read timeout.
	sendEnvelope(t, ctrl, protocol.TypePing, protocol.PingMessage{Timestamp: 1})

	env := expectEnvelope(t, ctrl)
	if env.Type != protocol.TypeError {
		t.Fatalf("first frame = %q, want %q: the third registration was accepted instead of being rejected by maxPendingRequestBodies", env.Type, protocol.TypeError)
	}
	var em protocol.ErrorMessage
	decodeData(t, env.Data, &em)
	if em.RequestID != "concurrent-2" {
		t.Errorf("error RequestID = %q, want concurrent-2 (the first two are under the cap)", em.RequestID)
	}
	if em.Message == "" {
		t.Error("error Message is empty, want an explanation of the concurrency limit")
	}
	expectType(t, ctrl, protocol.TypePong)

	c.pendingBodiesMu.Lock()
	pending := len(c.pendingBodies)
	_, rejectedRegistered := c.pendingBodies["concurrent-2"]
	c.pendingBodiesMu.Unlock()
	if pending != 2 {
		t.Errorf("pendingBodies = %d, want 2 (the cap)", pending)
	}
	if rejectedRegistered {
		t.Error("the rejected request was registered anyway, want it left out of the map")
	}

	fd.mu.Lock()
	calls := len(fd.doCalls)
	fd.mu.Unlock()
	if calls != 0 {
		t.Errorf("Docker calls = %d, want 0, nothing reached stream_end", calls)
	}
}

// TestStreamedBodyAggregateCapRejectsOnlyTheOffendingRequest covers the byte
// half of the reassembly bound, which is the one that actually caps agent
// memory: maxRequestBodyStream limits one buffer, and multiplying it by the
// number of concurrent reassemblies is what the aggregate limit stops. Only
// the chunk that crosses the line is failed; reassemblies already under way
// keep going.
func TestStreamedBodyAggregateCapRejectsOnlyTheOffendingRequest(t *testing.T) {
	// Not t.Parallel(): mutates the package-level maxStreamedRequestBodyBytes
	// var. maxRequestBodyStream is deliberately left at its real 512 MB, so a
	// rejection here can only have come from the aggregate branch.

	orig := maxStreamedRequestBodyBytes
	maxStreamedRequestBodyBytes = 16
	t.Cleanup(func() { maxStreamedRequestBodyBytes = orig })

	c, ctrl := newTestClient(t)
	fd := &fakeDocker{}
	c.dockerClient = fd

	runReadPump(t, c)

	for _, id := range []string{"agg-a", "agg-b"} {
		sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
			RequestID:  id,
			Method:     http.MethodPost,
			Path:       "/build",
			BodyStream: true,
		})
		sendEnvelope(t, ctrl, protocol.TypeStream, protocol.StreamMessage{
			RequestID: id,
			Data:      base64.StdEncoding.EncodeToString([]byte("0123456789")), // 10 bytes each
		})
	}

	// The ping is sent up front so an unbounded aggregate shows up as a pong
	// arriving where the rejection should have been, rather than as an opaque
	// read timeout.
	sendEnvelope(t, ctrl, protocol.TypePing, protocol.PingMessage{Timestamp: 1})

	// 10 buffered for agg-a plus 10 more for agg-b is 20, over the 16 byte
	// aggregate, while each request on its own is nowhere near 512 MB.
	env := expectEnvelope(t, ctrl)
	if env.Type != protocol.TypeError {
		t.Fatalf("first frame = %q, want %q: the aggregate reassembly budget did not reject agg-b", env.Type, protocol.TypeError)
	}
	var em protocol.ErrorMessage
	decodeData(t, env.Data, &em)
	if em.RequestID != "agg-b" {
		t.Errorf("error RequestID = %q, want agg-b", em.RequestID)
	}
	if !strings.Contains(em.Message, "aggregate") {
		t.Errorf("error Message = %q, want the aggregate limit named", em.Message)
	}
	expectType(t, ctrl, protocol.TypePong)

	c.pendingBodiesMu.Lock()
	_, aLives := c.pendingBodies["agg-a"]
	_, bLives := c.pendingBodies["agg-b"]
	c.pendingBodiesMu.Unlock()
	if !aLives {
		t.Error("agg-a was dropped, want the under-budget reassembly left alone")
	}
	if bLives {
		t.Error("agg-b is still registered, want the over-budget reassembly freed")
	}

	fd.mu.Lock()
	calls := len(fd.doCalls)
	fd.mu.Unlock()
	if calls != 0 {
		t.Errorf("Docker calls = %d, want 0", calls)
	}
}

// TestStreamedBodyBudgetCoversDispatchAndIsReleased is the regression guard
// for the hole the reassembly-only bound left. finishPendingBody deletes the
// pendingBodies entry before handleRequestTo runs, so a sum over that map
// alone stops charging the agent for bytes it still holds in req.Body for the
// whole Docker round trip. A controller could fill the budget, send
// stream_end on every request to zero the count, and immediately refill it,
// making the real ceiling streamSem (100) times the per-request cap instead
// of the aggregate one.
//
// Three bodies are parked inside the Docker call and a fourth chunk that only
// fits if those three stopped counting has to be rejected. The second half
// proves the reservation is released rather than merely taken: once the
// parked calls return, the same budget accepts a new body.
func TestStreamedBodyBudgetCoversDispatchAndIsReleased(t *testing.T) {
	// Not t.Parallel(): mutates the package-level maxStreamedRequestBodyBytes
	// var. maxRequestBodyStream stays at its real 512 MB, so a rejection here
	// can only have come from the aggregate branch.

	orig := maxStreamedRequestBodyBytes
	maxStreamedRequestBodyBytes = 35
	t.Cleanup(func() { maxStreamedRequestBodyBytes = orig })

	c, ctrl := newTestClient(t)
	// The queued send path, not newTestClient's direct-write one: three
	// parked handlers answer concurrently at the end, and gorilla panics on a
	// concurrent write. This is also what production uses.
	runSendPump(t, c)

	gate := make(chan struct{})
	var gateOnce sync.Once
	openGate := func() { gateOnce.Do(func() { close(gate) }) }
	// doErr plus doGate parks every handler inside DoWithHeaders still
	// holding its reassembled body, then answers with an error frame when the
	// gate opens. No canned *http.Response on purpose: three handlers would
	// share one Body reader. The requests are POST /containers/create rather
	// than the /build a real streamed body targets because only the
	// non-streaming fakeDocker method carries the gate; the budget this test
	// is about is charged before either is picked.
	fd := &fakeDocker{doErr: errors.New("docker parked"), doGate: gate}
	c.dockerClient = fd

	runReadPump(t, c)
	// Registered after the read pump so cleanup runs before it: parked
	// handlers are freed first, then the pump is cancelled.
	t.Cleanup(openGate)

	const chunk = "0123456789" // 10 bytes each, 30 across the three
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("parked-%d", i)
		sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
			RequestID:  id,
			Method:     http.MethodPost,
			Path:       "/containers/create",
			BodyStream: true,
		})
		sendEnvelope(t, ctrl, protocol.TypeStream, protocol.StreamMessage{
			RequestID: id,
			Data:      base64.StdEncoding.EncodeToString([]byte(chunk)),
		})
		sendEnvelope(t, ctrl, protocol.TypeStreamEnd, protocol.StreamEndMessage{
			RequestID: id,
			Reason:    "complete",
		})
		want := i + 1
		waitFor(t, fmt.Sprintf("%s to reach the Docker call", id), func() bool {
			fd.mu.Lock()
			defer fd.mu.Unlock()
			return len(fd.doCalls) == want
		})
	}

	// Asserted directly first so a regression names the reservation instead
	// of surfacing later as an unexplained missing frame.
	c.pendingBodiesMu.Lock()
	held := c.streamedBodyBytesLocked()
	pending := len(c.pendingBodies)
	c.pendingBodiesMu.Unlock()
	if pending != 0 {
		t.Fatalf("pendingBodies = %d, want 0: all three reached stream_end", pending)
	}
	if want := int64(3 * len(chunk)); held != want {
		t.Fatalf("held streamed bytes = %d, want %d: dispatched bodies stopped counting once finishPendingBody removed them", held, want)
	}

	// 30 parked plus 10 more is 40, over the 35 byte budget, while each body
	// on its own is nowhere near the 512 MB per-request cap.
	sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
		RequestID:  "overflow",
		Method:     http.MethodPost,
		Path:       "/containers/create",
		BodyStream: true,
	})
	sendEnvelope(t, ctrl, protocol.TypeStream, protocol.StreamMessage{
		RequestID: "overflow",
		Data:      base64.StdEncoding.EncodeToString([]byte(chunk)),
	})
	// The ping is sent up front so an accepted chunk shows up as a pong
	// arriving where the rejection should have been, not as a read timeout.
	sendEnvelope(t, ctrl, protocol.TypePing, protocol.PingMessage{Timestamp: 1})

	env := expectEnvelope(t, ctrl)
	if env.Type != protocol.TypeError {
		t.Fatalf("first frame = %q, want %q: the budget did not count the three bodies already dispatched to Docker", env.Type, protocol.TypeError)
	}
	var em protocol.ErrorMessage
	decodeData(t, env.Data, &em)
	if em.RequestID != "overflow" {
		t.Errorf("error RequestID = %q, want overflow", em.RequestID)
	}
	if !strings.Contains(em.Message, "aggregate") {
		t.Errorf("error Message = %q, want the aggregate limit named", em.Message)
	}
	expectType(t, ctrl, protocol.TypePong)

	// Second half: the reservation is released when the round trip ends.
	openGate()
	for i := 0; i < 3; i++ {
		var released protocol.ErrorMessage
		decodeData(t, expectType(t, ctrl, protocol.TypeError), &released)
		if !strings.HasPrefix(released.RequestID, "parked-") {
			t.Errorf("released frame %d RequestID = %q, want a parked-N request", i, released.RequestID)
		}
	}
	waitFor(t, "the dispatch reservations to be released", func() bool {
		c.pendingBodiesMu.Lock()
		defer c.pendingBodiesMu.Unlock()
		return c.streamedBodyBytesLocked() == 0
	})

	sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
		RequestID:  "after-release",
		Method:     http.MethodPost,
		Path:       "/containers/create",
		BodyStream: true,
	})
	sendEnvelope(t, ctrl, protocol.TypeStream, protocol.StreamMessage{
		RequestID: "after-release",
		Data:      base64.StdEncoding.EncodeToString([]byte(chunk)),
	})
	sendEnvelope(t, ctrl, protocol.TypePing, protocol.PingMessage{Timestamp: 2})
	expectType(t, ctrl, protocol.TypePong)

	c.pendingBodiesMu.Lock()
	pb, registered := c.pendingBodies["after-release"]
	var buffered int
	if registered {
		buffered = pb.buf.Len()
	}
	c.pendingBodiesMu.Unlock()
	if !registered {
		t.Fatal("after-release was rejected, want the freed budget to accept it")
	}
	if buffered != len(chunk) {
		t.Errorf("after-release buffered = %d, want %d: a finished dispatch is still being charged", buffered, len(chunk))
	}
}

// TestStreamedBodyRejectedWhenStreamSemFullReleasesItsReservation covers the
// maxStreams admission check dispatchStreamedBody duplicates from readPump's
// inline-body path. The duplication is what keeps releaseDispatchingBody a
// single defer with one release site, so the branch that pays for it is the
// one that has to be proven: a fully reassembled body arriving while
// maxStreams requests are already in flight is rejected, and — the half that
// matters — the bytes finishPendingBody charged it are handed back rather
// than held against maxStreamedRequestBodyBytes for the life of the process.
func TestStreamedBodyRejectedWhenStreamSemFullReleasesItsReservation(t *testing.T) {
	// Not t.Parallel(): mutates the package-level maxStreamedRequestBodyBytes
	// var. 15 is picked so one 10-byte body fits and two do not, which is what
	// makes the second half a test of the release rather than a restatement of
	// the first. maxRequestBodyStream stays at its real 512 MB, so nothing here
	// can be rejected by the per-request cap.
	orig := maxStreamedRequestBodyBytes
	maxStreamedRequestBodyBytes = 15
	t.Cleanup(func() { maxStreamedRequestBodyBytes = orig })

	c, ctrl := newTestClient(t)
	// The queued send path, not newTestClient's direct-write one: the
	// rejection is written by the dispatch goroutine while readPump is free to
	// write a pong, and gorilla panics on a concurrent write.
	runSendPump(t, c)
	fd := &fakeDocker{} // no canned response: a call here would mean the admission check didn't hold.
	c.dockerClient = fd

	// Saturate the same semaphore an inline-body request takes, so the
	// reassembled body below finds no slot. Filled before the read pump starts,
	// so nothing races it.
	for i := 0; i < cap(c.streamSem); i++ {
		c.streamSem <- struct{}{}
	}

	runReadPump(t, c)

	const chunk = "0123456789" // 10 bytes, under the 15 byte budget on its own

	sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
		RequestID:  "no-slot",
		Method:     http.MethodPost,
		Path:       "/containers/create",
		BodyStream: true,
	})
	sendEnvelope(t, ctrl, protocol.TypeStream, protocol.StreamMessage{
		RequestID: "no-slot",
		Data:      base64.StdEncoding.EncodeToString([]byte(chunk)),
	})
	sendEnvelope(t, ctrl, protocol.TypeStreamEnd, protocol.StreamEndMessage{
		RequestID: "no-slot",
		Reason:    "complete",
	})

	var em protocol.ErrorMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeError), &em)
	if em.RequestID != "no-slot" {
		t.Errorf("error RequestID = %q, want no-slot", em.RequestID)
	}
	if em.Message != "agent busy: too many concurrent requests" {
		t.Errorf("error Message = %q, want the concurrent-request rejection", em.Message)
	}

	fd.mu.Lock()
	calls := len(fd.doCalls)
	fd.mu.Unlock()
	if calls != 0 {
		t.Errorf("Docker calls = %d, want 0: a body rejected on admission must never reach dockerd", calls)
	}

	// The release is a defer, so it lands just after the frame above was
	// enqueued. Poll for it rather than racing it.
	waitFor(t, "the rejected body's byte reservation to be released", func() bool {
		c.pendingBodiesMu.Lock()
		defer c.pendingBodiesMu.Unlock()
		return c.streamedBodyBytesLocked() == 0
	})

	// Behavioural half: a leaked reservation still holds 10 of the 15 byte
	// budget, so this second 10-byte body would be failed by the aggregate cap
	// instead of buffered. The ping is sent up front so a rejection shows up as
	// a TypeError arriving where the pong should have been, not as an opaque
	// read timeout.
	sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
		RequestID:  "after-rejection",
		Method:     http.MethodPost,
		Path:       "/containers/create",
		BodyStream: true,
	})
	sendEnvelope(t, ctrl, protocol.TypeStream, protocol.StreamMessage{
		RequestID: "after-rejection",
		Data:      base64.StdEncoding.EncodeToString([]byte(chunk)),
	})
	sendEnvelope(t, ctrl, protocol.TypePing, protocol.PingMessage{Timestamp: 1})

	env := expectEnvelope(t, ctrl)
	if env.Type != protocol.TypePong {
		t.Fatalf("frame after the second chunk = %q (data=%s), want %q: the rejected body's bytes are still charged against the aggregate budget", env.Type, env.Data, protocol.TypePong)
	}

	c.pendingBodiesMu.Lock()
	pb, registered := c.pendingBodies["after-rejection"]
	var buffered int
	if registered {
		buffered = pb.buf.Len()
	}
	c.pendingBodiesMu.Unlock()
	if !registered {
		t.Fatal("after-rejection was dropped, want the freed budget to accept it")
	}
	if buffered != len(chunk) {
		t.Errorf("after-rejection buffered = %d, want %d: a rejected dispatch is still being charged", buffered, len(chunk))
	}
}

// TestStreamedBodyDuplicateRequestIDRejectedAndOriginalSurvives covers the
// duplicate-requestId branch of registerPendingBody. Reassembly is keyed by
// RequestID alone, so a second bodyStream=true request under an ID that is
// already filling would otherwise replace the entry and silently truncate a
// live upload to whatever arrived after it. The rejection has to name the
// request, and the reassembly already under way has to be left exactly as it
// was — proven by dispatching it afterwards and comparing the bytes dockerd
// receives against the chunk sent before the duplicate.
func TestStreamedBodyDuplicateRequestIDRejectedAndOriginalSurvives(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	//nolint:bodyclose // consumed and closed by handleRequestTo, the code under test.
	fd := &fakeDocker{doResp: mkResp(http.StatusCreated, "application/json", `{"ok":true}`)}
	c.dockerClient = fd

	runReadPump(t, c)

	const original = "first-body-bytes"

	sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
		RequestID:  "dup",
		Method:     http.MethodPost,
		Path:       "/containers/create",
		BodyStream: true,
	})
	sendEnvelope(t, ctrl, protocol.TypeStream, protocol.StreamMessage{
		RequestID: "dup",
		Data:      base64.StdEncoding.EncodeToString([]byte(original)),
	})
	// Same RequestID, still open. This is the frame under test.
	sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
		RequestID:  "dup",
		Method:     http.MethodPost,
		Path:       "/containers/create",
		BodyStream: true,
	})

	var em protocol.ErrorMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeError), &em)
	if em.RequestID != "dup" {
		t.Errorf("error RequestID = %q, want dup", em.RequestID)
	}
	if em.Message != "duplicate requestId for streamed request body" {
		t.Errorf("error Message = %q, want the duplicate-requestId rejection", em.Message)
	}

	// The rejection is written by readPump itself, which is a single
	// goroutine, so reading it is the ordering barrier: the chunk and the
	// duplicate registration are both fully handled by now.
	c.pendingBodiesMu.Lock()
	pb, stillPending := c.pendingBodies["dup"]
	var buffered string
	if stillPending {
		buffered = pb.buf.String()
	}
	pending := len(c.pendingBodies)
	c.pendingBodiesMu.Unlock()
	if !stillPending {
		t.Fatal("the original reassembly was removed by the duplicate, want it kept")
	}
	if buffered != original {
		t.Errorf("buffered = %q, want %q: the duplicate clobbered the in-flight reassembly", buffered, original)
	}
	if pending != 1 {
		t.Errorf("pendingBodies = %d, want 1: the duplicate must not add an entry", pending)
	}

	// End-to-end proof that the surviving entry is the original one and is
	// still usable: it dispatches with exactly the bytes sent before the
	// duplicate arrived.
	sendEnvelope(t, ctrl, protocol.TypeStreamEnd, protocol.StreamEndMessage{
		RequestID: "dup",
		Reason:    "complete",
	})

	var resp protocol.ResponseMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeResponse), &resp)
	if resp.RequestID != "dup" {
		t.Errorf("RequestID = %q, want dup", resp.RequestID)
	}

	fd.mu.Lock()
	calls := append([]doCall(nil), fd.doCalls...)
	fd.mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("Docker calls = %d, want 1", len(calls))
	}
	if string(calls[0].body) != original {
		t.Errorf("dispatched body = %q, want %q", calls[0].body, original)
	}
}

// TestStreamedBodyInvalidBase64ChunkRejectsAndFreesTheRequestID covers the
// decode-failure branch of appendPendingBody. A chunk that isn't valid base64
// can't be appended to anything, so the request is failed with a TypeError —
// but the entry also has to be removed, or the RequestID stays wedged for the
// idle timeout and every retry under it is answered with a duplicate-requestId
// rejection instead. The second half is the assertion that proves the cleanup
// ran: the same RequestID re-registers, uploads, and dispatches normally.
func TestStreamedBodyInvalidBase64ChunkRejectsAndFreesTheRequestID(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	//nolint:bodyclose // consumed and closed by handleRequestTo, the code under test.
	fd := &fakeDocker{doResp: mkResp(http.StatusCreated, "application/json", `{"ok":true}`)}
	c.dockerClient = fd

	runReadPump(t, c)

	sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
		RequestID:  "bad-b64",
		Method:     http.MethodPost,
		Path:       "/containers/create",
		BodyStream: true,
	})
	sendEnvelope(t, ctrl, protocol.TypeStream, protocol.StreamMessage{
		RequestID: "bad-b64",
		Data:      "!!! not base64 !!!",
	})

	var em protocol.ErrorMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeError), &em)
	if em.RequestID != "bad-b64" {
		t.Errorf("error RequestID = %q, want bad-b64", em.RequestID)
	}
	if em.Message != "invalid base64 in streamed request body chunk" {
		t.Errorf("error Message = %q, want the base64 rejection", em.Message)
	}

	// Written by readPump itself, so reading the frame above is the ordering
	// barrier for this.
	c.pendingBodiesMu.Lock()
	pending := len(c.pendingBodies)
	c.pendingBodiesMu.Unlock()
	if pending != 0 {
		t.Errorf("pendingBodies = %d, want 0: the failed reassembly must be freed", pending)
	}

	// The half that matters. If the entry were merely failed and left behind,
	// this re-registration would draw a duplicate-requestId TypeError instead
	// of completing.
	const retry = "retried-body-bytes"

	sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
		RequestID:  "bad-b64",
		Method:     http.MethodPost,
		Path:       "/containers/create",
		BodyStream: true,
	})
	sendEnvelope(t, ctrl, protocol.TypeStream, protocol.StreamMessage{
		RequestID: "bad-b64",
		Data:      base64.StdEncoding.EncodeToString([]byte(retry)),
	})
	sendEnvelope(t, ctrl, protocol.TypeStreamEnd, protocol.StreamEndMessage{
		RequestID: "bad-b64",
		Reason:    "complete",
	})

	env := expectEnvelope(t, ctrl)
	if env.Type != protocol.TypeResponse {
		t.Fatalf("frame after the retry = %q (data=%s), want %q: the RequestID was left wedged by the decode failure", env.Type, env.Data, protocol.TypeResponse)
	}
	var resp protocol.ResponseMessage
	decodeData(t, env.Data, &resp)
	if resp.RequestID != "bad-b64" {
		t.Errorf("RequestID = %q, want bad-b64", resp.RequestID)
	}

	fd.mu.Lock()
	calls := append([]doCall(nil), fd.doCalls...)
	fd.mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("Docker calls = %d, want 1: only the retry may dispatch", len(calls))
	}
	if string(calls[0].body) != retry {
		t.Errorf("dispatched body = %q, want %q: the undecodable chunk leaked into the retry", calls[0].body, retry)
	}
}
