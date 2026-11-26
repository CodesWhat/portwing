// SPDX-License-Identifier: MIT

package edge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/docker"
	"github.com/codeswhat/portwing/internal/protocol"
)

// blockingDocker blocks every Docker call on /blocked paths until the request
// context is cancelled, the way a real round trip aborts on cancel. Any other
// path answers from the embedded fakeDocker. streamBody, when set, is served
// for streaming calls on /blocked paths: it yields one chunk and then blocks
// on the request context.
type blockingDocker struct {
	*fakeDocker
	entered atomic.Int32
	stream  bool
}

type ctxBody struct {
	ctx   context.Context
	first atomic.Bool
}

func (b *ctxBody) Read(p []byte) (int, error) {
	if b.first.CompareAndSwap(false, true) {
		return copy(p, "chunk"), nil
	}
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}
func (b *ctxBody) Close() error { return nil }

func (d *blockingDocker) DoWithHeaders(ctx context.Context, method, path string, h http.Header, body io.Reader) (*http.Response, error) {
	if !strings.HasPrefix(path, "/blocked") {
		return d.fakeDocker.DoWithHeaders(ctx, method, path, h, body)
	}
	d.entered.Add(1)
	<-ctx.Done()
	return nil, ctx.Err()
}

// DoWithDeadline routes through this wrapper's DoWithHeaders; the embedded
// fakeDocker's version would call its own and skip the blocking.
func (d *blockingDocker) DoWithDeadline(ctx context.Context, method, path string, h http.Header, body io.Reader, _ time.Duration) (*http.Response, error) {
	return d.DoWithHeaders(ctx, method, path, h, body)
}

func (d *blockingDocker) DoStreamWithHeaders(ctx context.Context, method, path string, h http.Header, body io.Reader) (*http.Response, error) {
	if !strings.HasPrefix(path, "/blocked") {
		return d.fakeDocker.DoStreamWithHeaders(ctx, method, path, h, body)
	}
	d.entered.Add(1)
	if d.stream {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: &ctxBody{ctx: ctx}}, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

type wsCtrl struct {
	t *testing.T
	c *websocket.Conn
}

func newCancelClient(t *testing.T) (*Client, *blockingDocker, *wsCtrl) {
	t.Helper()
	c, ctrl := newTestClient(t)
	bd := &blockingDocker{fakeDocker: &fakeDocker{doResp: mkResp(200, "application/json", `{"ok":true}`)}} //nolint:bodyclose // handleRequestTo closes the body
	c.dockerClient = bd
	c.adapter = &fakeAdapter{}
	auditor, closeAudit, err := audit.New("", 32)
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}
	t.Cleanup(closeAudit)
	c.auditor = auditor
	runReadPump(t, c)
	return c, bd, &wsCtrl{t: t, c: ctrl}
}

func sendCancel(t *testing.T, w *wsCtrl, id, reason string) {
	t.Helper()
	sendEnvelope(t, w.c, protocol.TypeDDCancel, protocol.DDCancelMessage{RequestID: id, Reason: reason})
}

// pingBarrier proves the read pump has processed everything sent before it and
// that no frame was queued ahead of the pong.
func pingBarrier(t *testing.T, w *wsCtrl) {
	t.Helper()
	sendEnvelope(t, w.c, protocol.TypePing, protocol.PingMessage{Timestamp: 99})
	expectType(t, w.c, protocol.TypePong)
}

func lastAudit(t *testing.T, c *Client) audit.Record {
	t.Helper()
	recs := c.auditor.Records(10)
	if len(recs) == 0 {
		t.Fatal("no audit records")
	}
	return recs[0] // Records is newest first
}

func TestCancelBufferedRequestEmitsCancelledErrorAfterSlotRelease(t *testing.T) {
	t.Parallel()
	c, bd, w := newCancelClient(t)

	sendEnvelope(t, w.c, protocol.TypeRequest, protocol.RequestMessage{RequestID: "r1", Method: "GET", Path: "/blocked/x"})
	waitFor(t, "docker call entered", func() bool { return bd.entered.Load() == 1 })
	sendCancel(t, w, "r1", "timeout")

	var em protocol.ErrorMessage
	decodeData(t, expectType(t, w.c, protocol.TypeError), &em)
	if em.RequestID != "r1" || em.Code != protocol.ErrorCodeCancelled || em.Message != "request cancelled" {
		t.Errorf("error = %+v, want cancelled for r1", em)
	}
	// The terminal frame is sent after release, so seeing it means capacity.
	if n := len(c.streamSem); n != 0 {
		t.Errorf("streamSem held = %d when cancelled frame observed, want 0", n)
	}
	rec := lastAudit(t, c)
	if rec.Outcome != audit.OutcomeCancelled || rec.Reason != "timeout" {
		t.Errorf("audit = %+v, want cancelled with reason timeout", rec)
	}
	pingBarrier(t, w) // exactly one terminal frame
}

func TestCancelStreamMidBodyEmitsStreamEndCancelled(t *testing.T) {
	t.Parallel()
	c, bd, w := newCancelClient(t)
	bd.stream = true

	sendEnvelope(t, w.c, protocol.TypeRequest, protocol.RequestMessage{RequestID: "s1", Method: "GET", Path: "/blocked/events"})
	var rm protocol.ResponseMessage
	decodeData(t, expectType(t, w.c, protocol.TypeResponse), &rm)
	if !rm.IsStream {
		t.Fatalf("response = %+v, want stream header", rm)
	}
	expectType(t, w.c, protocol.TypeStream)

	sendCancel(t, w, "s1", "client-abort")
	var se protocol.StreamEndMessage
	decodeData(t, expectType(t, w.c, protocol.TypeStreamEnd), &se)
	if se.RequestID != "s1" || se.Reason != "cancelled" {
		t.Errorf("stream_end = %+v, want cancelled for s1", se)
	}
	if n := len(c.streamSem); n != 0 {
		t.Errorf("streamSem held = %d when stream_end observed, want 0", n)
	}
	if rec := lastAudit(t, c); rec.Outcome != audit.OutcomeCancelled || rec.Reason != "client-abort" {
		t.Errorf("audit = %+v, want cancelled with reason client-abort", rec)
	}
	pingBarrier(t, w) // no stream frame or error after the terminal
}

func TestCancelAfterCompletionIsNoOp(t *testing.T) {
	t.Parallel()
	c, _, w := newCancelClient(t)

	sendEnvelope(t, w.c, protocol.TypeRequest, protocol.RequestMessage{RequestID: "done", Method: "GET", Path: "/containers/json"})
	expectType(t, w.c, protocol.TypeResponse)
	waitFor(t, "slot released", func() bool { return len(c.streamSem) == 0 })
	waitFor(t, "registry drained", func() bool {
		c.inflightMu.Lock()
		defer c.inflightMu.Unlock()
		return len(c.inflight) == 0
	})

	sendCancel(t, w, "done", "timeout")
	pingBarrier(t, w) // the pong arrives first, so no frame followed the cancel
}

func TestCancelUnknownAndInvalidIDsAreHarmless(t *testing.T) {
	t.Parallel()
	_, _, w := newCancelClient(t)

	sendCancel(t, w, "never-existed", "timeout")
	sendCancel(t, w, "", "timeout")
	sendCancel(t, w, strings.Repeat("x", protocol.MaxCancelRequestIDLen+1), "timeout")
	sendCancel(t, w, "id", strings.Repeat("r", 500))
	sendEnvelope(t, w.c, protocol.TypeDDCancel, json.RawMessage(`"not an object"`))
	pingBarrier(t, w)
}

func TestCancelPendingUploadActsLikeRequestCancelled(t *testing.T) {
	t.Parallel()
	c, bd, w := newCancelClient(t)

	sendEnvelope(t, w.c, protocol.TypeRequest, protocol.RequestMessage{RequestID: "up", Method: "POST", Path: "/build", BodyStream: true})
	sendEnvelope(t, w.c, protocol.TypeStream, protocol.StreamMessage{RequestID: "up", Data: base64.StdEncoding.EncodeToString([]byte("part"))})
	pingBarrier(t, w)
	sendCancel(t, w, "up", "client-abort")
	pingBarrier(t, w)

	c.pendingBodiesMu.Lock()
	n := len(c.pendingBodies)
	c.pendingBodiesMu.Unlock()
	if n != 0 {
		t.Errorf("pendingBodies = %d, want 0 after cancel", n)
	}
	// A late stream_end must not dispatch a partial body.
	sendEnvelope(t, w.c, protocol.TypeStreamEnd, protocol.StreamEndMessage{RequestID: "up"})
	pingBarrier(t, w)
	bd.mu.Lock()
	calls := len(bd.doCalls)
	bd.mu.Unlock()
	if calls != 0 {
		t.Errorf("docker calls = %d, want 0", calls)
	}
}

// Cancelling a dispatched streamed-body request frees the byte reservation
// and the slot, same as an inline one.
func TestCancelDispatchedStreamedBodyReleasesBudget(t *testing.T) {
	t.Parallel()
	c, bd, w := newCancelClient(t)

	sendEnvelope(t, w.c, protocol.TypeRequest, protocol.RequestMessage{RequestID: "ub", Method: "POST", Path: "/blocked/build", BodyStream: true})
	sendEnvelope(t, w.c, protocol.TypeStream, protocol.StreamMessage{RequestID: "ub", Data: base64.StdEncoding.EncodeToString([]byte("payload"))})
	sendEnvelope(t, w.c, protocol.TypeStreamEnd, protocol.StreamEndMessage{RequestID: "ub"})
	waitFor(t, "docker call entered", func() bool { return bd.entered.Load() == 1 })

	sendCancel(t, w, "ub", "timeout")
	var em protocol.ErrorMessage
	decodeData(t, expectType(t, w.c, protocol.TypeError), &em)
	if em.Code != protocol.ErrorCodeCancelled {
		t.Errorf("error = %+v, want cancelled", em)
	}
	c.pendingBodiesMu.Lock()
	held := len(c.dispatchingBodies)
	c.pendingBodiesMu.Unlock()
	if held != 0 || len(c.streamSem) != 0 {
		t.Errorf("dispatchingBodies = %d, streamSem = %d, want both 0 at the terminal frame", held, len(c.streamSem))
	}
}

func TestCancelledRequestsFreeSlotsForFollowUp(t *testing.T) {
	t.Parallel()
	c, bd, w := newCancelClient(t)

	for i := 0; i < maxStreams; i++ {
		sendEnvelope(t, w.c, protocol.TypeRequest, protocol.RequestMessage{RequestID: reqID(i), Method: "GET", Path: "/blocked/x"})
	}
	waitFor(t, "all admitted", func() bool { return bd.entered.Load() == int32(maxStreams) })

	sendEnvelope(t, w.c, protocol.TypeRequest, protocol.RequestMessage{RequestID: "extra", Method: "GET", Path: "/containers/json"})
	var busy protocol.ErrorMessage
	decodeData(t, expectType(t, w.c, protocol.TypeError), &busy)
	if busy.RequestID != "extra" || busy.Code == protocol.ErrorCodeCancelled {
		t.Fatalf("101st request = %+v, want a busy rejection", busy)
	}

	for i := 0; i < maxStreams; i++ {
		sendCancel(t, w, reqID(i), "timeout")
	}
	for i := 0; i < maxStreams; i++ {
		var em protocol.ErrorMessage
		decodeData(t, expectType(t, w.c, protocol.TypeError), &em)
		if em.Code != protocol.ErrorCodeCancelled {
			t.Fatalf("frame %d = %+v, want cancelled", i, em)
		}
	}
	waitFor(t, "slots free", func() bool { return len(c.streamSem) == 0 })

	sendEnvelope(t, w.c, protocol.TypeRequest, protocol.RequestMessage{RequestID: "extra2", Method: "GET", Path: "/containers/json"})
	var rm protocol.ResponseMessage
	decodeData(t, expectType(t, w.c, protocol.TypeResponse), &rm)
	if rm.RequestID != "extra2" || rm.StatusCode != 200 {
		t.Errorf("follow-up = %+v, want admitted and answered", rm)
	}
}

func reqID(i int) string { return fmt.Sprintf("abandoned-%d", i) }

func TestCancelRawExecStartReleasesExecSlot(t *testing.T) {
	t.Parallel()
	c, bd, w := newCancelClient(t)

	execDocker := &execBlockingDocker{blockingDocker: bd}
	c.dockerClient = execDocker
	sendEnvelope(t, w.c, protocol.TypeRequest, protocol.RequestMessage{RequestID: "ex2", Method: "POST", Path: "/exec/abc/start"})
	waitFor(t, "exec call entered", func() bool { return execDocker.execEntered.Load() == 1 })
	c.execAdmissionMu.Lock()
	held := c.rawExecStarts
	c.execAdmissionMu.Unlock()
	if held != 1 {
		t.Fatalf("rawExecStarts = %d before cancel, want 1", held)
	}
	sendCancel(t, w, "ex2", "timeout")
	var em protocol.ErrorMessage
	decodeData(t, expectType(t, w.c, protocol.TypeError), &em)
	if em.Code != protocol.ErrorCodeCancelled {
		t.Errorf("error = %+v, want cancelled", em)
	}
	c.execAdmissionMu.Lock()
	held = c.rawExecStarts
	c.execAdmissionMu.Unlock()
	if held != 0 {
		t.Errorf("rawExecStarts = %d at the terminal frame, want 0", held)
	}
}

type execBlockingDocker struct {
	*blockingDocker
	execEntered atomic.Int32
}

func (d *execBlockingDocker) DoWithHeaders(ctx context.Context, method, path string, h http.Header, body io.Reader) (*http.Response, error) {
	return d.blockExec(ctx)
}

func (d *execBlockingDocker) DoWithDeadline(ctx context.Context, method, path string, h http.Header, body io.Reader, _ time.Duration) (*http.Response, error) {
	return d.DoWithHeaders(ctx, method, path, h, body)
}

func (d *execBlockingDocker) DoStreamWithHeaders(ctx context.Context, method, path string, h http.Header, body io.Reader) (*http.Response, error) {
	return d.blockExec(ctx)
}

func (d *execBlockingDocker) blockExec(ctx context.Context) (*http.Response, error) {
	d.execEntered.Add(1)
	<-ctx.Done()
	return nil, ctx.Err()
}

type blockingCompose struct {
	entered atomic.Int32
}

func (b *blockingCompose) Execute(ctx context.Context, _ docker.ComposeRequest) (*docker.ComposeResponse, error) {
	b.entered.Add(1)
	<-ctx.Done()
	return &docker.ComposeResponse{Cancelled: true}, nil
}

func TestCancelComposeRecordsCancelledOutcome(t *testing.T) {
	t.Parallel()
	c, _, w := newCancelClient(t)
	bc := &blockingCompose{}
	c.compose = bc

	body, _ := json.Marshal(docker.ComposeRequest{Operation: "up", StackName: "web"})
	sendEnvelope(t, w.c, protocol.TypeRequest, protocol.RequestMessage{RequestID: "cp", Method: "POST", Path: composeRequestPrefix, Body: body})
	waitFor(t, "compose entered", func() bool { return bc.entered.Load() == 1 })
	sendCancel(t, w, "cp", "timeout")

	var em protocol.ErrorMessage
	decodeData(t, expectType(t, w.c, protocol.TypeError), &em)
	if em.Code != protocol.ErrorCodeCancelled || em.RequestID != "cp" {
		t.Errorf("error = %+v, want cancelled for cp", em)
	}
	rec := lastAudit(t, c)
	if rec.Event != audit.EventComposeOp || rec.Outcome != audit.OutcomeCancelled || rec.Reason != "timeout" {
		t.Errorf("audit = %+v, want compose_op cancelled timeout", rec)
	}
	pingBarrier(t, w)
}

// An id the edge client doesn't own falls through to the adapter, which owns
// container log stream ids.
func TestCancelUnownedIDDelegatesToAdapter(t *testing.T) {
	t.Parallel()
	c, _, w := newCancelClient(t)
	fa := &fakeAdapter{handleMsgResult: true}
	c.adapter = fa

	sendCancel(t, w, "log-stream-1", "client-abort")
	pingBarrier(t, w)
	got := fa.messageTypes()
	if len(got) != 1 || got[0] != protocol.TypeDDCancel {
		t.Errorf("adapter saw %v, want one dd:cancel", got)
	}
}

// A connection that dropped takes its requests with it: a cancel for an id from
// a previous connection is unknown and sends nothing.
func TestCancelStaleIDAfterReconnectIsNoOp(t *testing.T) {
	t.Parallel()
	c, _, w := newCancelClient(t)

	_, _, untrack := c.trackRequest(context.Background(), "old-conn")
	untrack() // the old generation's handler exited with its connection

	sendCancel(t, w, "old-conn", "shutdown")
	pingBarrier(t, w)
}

func TestInflightRegistryRaces(t *testing.T) {
	t.Parallel()
	c := &Client{}
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(3)
		id := reqID(i % 8) // deliberate duplicates
		go func() {
			defer wg.Done()
			_, _, untrack := c.trackRequest(context.Background(), id)
			untrack()
		}()
		go func() { defer wg.Done(); c.cancelInflight(id, "timeout") }()
		go func() {
			defer wg.Done()
			ctx, _, untrack := c.trackRequest(context.Background(), id)
			defer untrack()
			cancelledByController(ctx)
		}()
	}
	wg.Wait()
	c.inflightMu.Lock()
	defer c.inflightMu.Unlock()
	if len(c.inflight) != 0 {
		t.Errorf("inflight = %d after all finished, want 0", len(c.inflight))
	}
}

func TestDuplicateIDKeepsFirstRegistration(t *testing.T) {
	t.Parallel()
	c := &Client{}
	ctx1, _, un1 := c.trackRequest(context.Background(), "dup")
	ctx2, _, un2 := c.trackRequest(context.Background(), "dup")
	un2() // the duplicate finishing must not evict the first
	if !c.cancelInflight("dup", "timeout") {
		t.Fatal("first registration was evicted by a duplicate")
	}
	if ctx1.Err() == nil {
		t.Error("first request not cancelled")
	}
	if ctx2.Err() == nil {
		// un2 cancels its own ctx with nil cause on exit.
		t.Error("duplicate ctx should be done after untrack")
	}
	un1()
	if c.cancelInflight("dup", "timeout") {
		t.Error("cancel of a finished id reported found")
	}
}

func TestHelloAdvertisesCancelCapability(t *testing.T) {
	t.Parallel()
	c, ctrl := newTestClient(t)
	c.dockerClient = &fakeDocker{}
	c.adapter = &fakeAdapter{}
	if err := c.sendHello(context.Background()); err != nil {
		t.Fatalf("sendHello: %v", err)
	}
	var hello protocol.HelloMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeHello), &hello)
	for _, cp := range hello.Capabilities {
		if cp == protocol.CapCancel {
			return
		}
	}
	t.Errorf("Capabilities = %v, want %q", hello.Capabilities, protocol.CapCancel)
}

// A dd:cancel sent right behind its request, with no wait for the handler to
// reach Docker, must still find the id. The pump registers it before the
// handler goroutine starts.
func TestCancelRightBehindRequestIsNotLost(t *testing.T) {
	t.Parallel()
	_, _, w := newCancelClient(t)

	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("early-%d", i)
		sendEnvelope(t, w.c, protocol.TypeRequest, protocol.RequestMessage{RequestID: id, Method: "POST", Path: "/blocked/x"})
		sendCancel(t, w, id, "timeout")
		var em protocol.ErrorMessage
		decodeData(t, expectType(t, w.c, protocol.TypeError), &em)
		if em.Code != protocol.ErrorCodeCancelled || em.RequestID != id {
			t.Fatalf("error = %+v, want cancelled for %s", em, id)
		}
	}
	pingBarrier(t, w)
}

// A streamed upload cancelled right after its stream_end has the same window.
func TestCancelRightBehindStreamEndIsNotLost(t *testing.T) {
	t.Parallel()
	_, _, w := newCancelClient(t)

	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("early-up-%d", i)
		sendEnvelope(t, w.c, protocol.TypeRequest, protocol.RequestMessage{RequestID: id, Method: "POST", Path: "/blocked/x", BodyStream: true})
		sendEnvelope(t, w.c, protocol.TypeStreamEnd, protocol.StreamEndMessage{RequestID: id})
		sendCancel(t, w, id, "timeout")
		var em protocol.ErrorMessage
		decodeData(t, expectType(t, w.c, protocol.TypeError), &em)
		if em.Code != protocol.ErrorCodeCancelled || em.RequestID != id {
			t.Fatalf("error = %+v, want cancelled for %s", em, id)
		}
	}
	pingBarrier(t, w)
}

// lockWaitCompose models a compose request cancelled while it waits on the
// stack lock: no error, Success:false, Cancelled:false.
type lockWaitCompose struct{ entered atomic.Int32 }

func (b *lockWaitCompose) Execute(ctx context.Context, _ docker.ComposeRequest) (*docker.ComposeResponse, error) {
	b.entered.Add(1)
	<-ctx.Done()
	return &docker.ComposeResponse{Success: false}, nil
}

func TestCancelComposeDuringLockWaitEmitsOneCancelledFrame(t *testing.T) {
	t.Parallel()
	c, _, w := newCancelClient(t)
	lc := &lockWaitCompose{}
	c.compose = lc

	body, _ := json.Marshal(docker.ComposeRequest{Operation: "up", StackName: "web"})
	sendEnvelope(t, w.c, protocol.TypeRequest, protocol.RequestMessage{RequestID: "lw", Method: "POST", Path: composeRequestPrefix, Body: body})
	waitFor(t, "compose entered", func() bool { return lc.entered.Load() == 1 })
	sendCancel(t, w, "lw", "timeout")

	var em protocol.ErrorMessage
	decodeData(t, expectType(t, w.c, protocol.TypeError), &em)
	if em.Code != protocol.ErrorCodeCancelled || em.RequestID != "lw" {
		t.Errorf("error = %+v, want cancelled for lw", em)
	}
	rec := lastAudit(t, c)
	if rec.Event != audit.EventComposeOp || rec.Outcome != audit.OutcomeCancelled || rec.Reason != "timeout" {
		t.Errorf("audit = %+v, want compose_op cancelled timeout", rec)
	}
	// pingBarrier fails on any extra terminal frame ahead of the pong.
	pingBarrier(t, w)
}

// A handler that outlives its connection must not hold its id against a retry
// on the next one: teardown clears the registry.
func TestClearInflightLetsRetryOnNewConnectionBeCancelled(t *testing.T) {
	t.Parallel()
	c := &Client{}
	_, _, oldUntrack := c.trackRequest(context.Background(), "retry")
	c.clearInflight() // old connection torn down, its handler still draining
	ctx2, _, un2 := c.trackRequest(context.Background(), "retry")
	if !c.cancelInflight("retry", "timeout") {
		t.Fatal("retry on the new connection not cancellable by id")
	}
	if ctx2.Err() == nil {
		t.Error("retry ctx not cancelled")
	}
	oldUntrack() // lingering handler finishing must not evict a successor
	un2()
}
