package edge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/codeswhat/portwing/internal/config"
	"github.com/codeswhat/portwing/internal/protocol"
)

// ---------------------------------------------------------------------------
// Package-level constant definitions (client.go:45,46,58,72,95,103)
// ---------------------------------------------------------------------------

// TestPackageConstantValues pins the exact computed value of every size/
// duration constant derived from an arithmetic expression. Every use site in
// the package reads these through their symbolic names, so a mutation to the
// arithmetic in the definition (e.g. `*` -> `/`) changes the value everywhere
// consistently and is invisible to any test that only exercises behavior
// through the name. Hardcoding the expected literal here is the only way to
// pin the definition itself.
func TestPackageConstantValues(t *testing.T) {
	t.Parallel()

	if maxReadSize != 16*1024*1024 {
		t.Errorf("maxReadSize = %d, want %d", maxReadSize, 16*1024*1024)
	}
	if maxResponseBody != 100*1024*1024 {
		t.Errorf("maxResponseBody = %d, want %d", maxResponseBody, 100*1024*1024)
	}
	if writeWait != 10*time.Second {
		t.Errorf("writeWait = %v, want %v", writeWait, 10*time.Second)
	}
	if maxRequestBodyStream != 512*1024*1024 {
		t.Errorf("maxRequestBodyStream = %d, want %d", maxRequestBodyStream, 512*1024*1024)
	}
	if maxStreamedRequestBodyBytes != 1024*1024*1024 {
		t.Errorf("maxStreamedRequestBodyBytes = %d, want %d", maxStreamedRequestBodyBytes, 1024*1024*1024)
	}
	if requestBodyStreamIdleTimeout != 30*time.Second {
		t.Errorf("requestBodyStreamIdleTimeout = %v, want %v", requestBodyStreamIdleTimeout, 30*time.Second)
	}
}

// ---------------------------------------------------------------------------
// appendPendingBody — per-request and aggregate overflow boundaries
// (client.go:927, 929)
// ---------------------------------------------------------------------------

// TestAppendPendingBodyPerRequestCapBoundary proves the exact boundary of the
// per-request maxRequestBodyStream check: a chunk that lands exactly at the
// cap is accepted, and one byte over is rejected. This distinguishes `>` from
// `>=` (CONDITIONALS_BOUNDARY), `>` from `<=` (CONDITIONALS_NEGATION), and `+`
// from `-` in the byte-count arithmetic (ARITHMETIC_BASE): any of those
// mutations flips the accept/reject decision at this exact boundary.
func TestAppendPendingBodyPerRequestCapBoundary(t *testing.T) {
	// Not t.Parallel(): mutates the package-level maxRequestBodyStream var.
	orig := maxRequestBodyStream
	maxRequestBodyStream = 10
	t.Cleanup(func() { maxRequestBodyStream = orig })

	fa := &fakeAdapter{handleMsgResult: true}
	c, ctrl := newTestClient(t)
	c.adapter = fa
	//nolint:bodyclose // consumed and closed by handleRequestTo, the code under test.
	fd := &fakeDocker{doResp: mkResp(http.StatusCreated, "application/json", `{"ok":true}`)}
	c.dockerClient = fd

	runReadPump(t, c)

	// Exactly at the cap (10 bytes): must be accepted and dispatched.
	sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
		RequestID:  "at-cap",
		Method:     http.MethodPost,
		Path:       "/containers/create",
		BodyStream: true,
	})
	sendEnvelope(t, ctrl, protocol.TypeStream, protocol.StreamMessage{
		RequestID: "at-cap",
		Data:      base64.StdEncoding.EncodeToString([]byte("0123456789")), // 10 bytes
	})
	sendEnvelope(t, ctrl, protocol.TypeStreamEnd, protocol.StreamEndMessage{RequestID: "at-cap", Reason: "complete"})

	var resp protocol.ResponseMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeResponse), &resp)
	if resp.RequestID != "at-cap" {
		t.Fatalf("RequestID = %q, want at-cap (the exact-cap body was rejected)", resp.RequestID)
	}

	// One byte over the cap: must be rejected with a TypeError.
	sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
		RequestID:  "over-cap",
		Method:     http.MethodPost,
		Path:       "/containers/create",
		BodyStream: true,
	})
	sendEnvelope(t, ctrl, protocol.TypeStream, protocol.StreamMessage{
		RequestID: "over-cap",
		Data:      base64.StdEncoding.EncodeToString([]byte("01234567890")), // 11 bytes
	})

	var em protocol.ErrorMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeError), &em)
	if em.RequestID != "over-cap" {
		t.Fatalf("error RequestID = %q, want over-cap (the over-cap body was accepted)", em.RequestID)
	}
}

// TestAppendPendingBodyAggregateCapBoundary proves the exact boundary of the
// aggregate maxStreamedRequestBodyBytes check the same way as
// TestAppendPendingBodyPerRequestCapBoundary, for the second `case` in
// appendPendingBody's overflow switch.
func TestAppendPendingBodyAggregateCapBoundary(t *testing.T) {
	// Not t.Parallel(): mutates the package-level maxStreamedRequestBodyBytes
	// var. maxRequestBodyStream stays at its real 512 MB so the per-request
	// cap never fires first.
	orig := maxStreamedRequestBodyBytes
	maxStreamedRequestBodyBytes = 10
	t.Cleanup(func() { maxStreamedRequestBodyBytes = orig })

	fa := &fakeAdapter{handleMsgResult: true}
	c, ctrl := newTestClient(t)
	c.adapter = fa
	//nolint:bodyclose // consumed and closed by handleRequestTo, the code under test.
	fd := &fakeDocker{doResp: mkResp(http.StatusCreated, "application/json", `{"ok":true}`)}
	c.dockerClient = fd

	runReadPump(t, c)

	// Exactly at the aggregate cap: must be accepted.
	sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
		RequestID:  "agg-at-cap",
		Method:     http.MethodPost,
		Path:       "/containers/create",
		BodyStream: true,
	})
	sendEnvelope(t, ctrl, protocol.TypeStream, protocol.StreamMessage{
		RequestID: "agg-at-cap",
		Data:      base64.StdEncoding.EncodeToString([]byte("0123456789")), // 10 bytes
	})
	sendEnvelope(t, ctrl, protocol.TypeStreamEnd, protocol.StreamEndMessage{RequestID: "agg-at-cap", Reason: "complete"})

	var resp protocol.ResponseMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeResponse), &resp)
	if resp.RequestID != "agg-at-cap" {
		t.Fatalf("RequestID = %q, want agg-at-cap (the exact-cap body was rejected)", resp.RequestID)
	}

	// One byte over: must be rejected.
	sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
		RequestID:  "agg-over-cap",
		Method:     http.MethodPost,
		Path:       "/containers/create",
		BodyStream: true,
	})
	sendEnvelope(t, ctrl, protocol.TypeStream, protocol.StreamMessage{
		RequestID: "agg-over-cap",
		Data:      base64.StdEncoding.EncodeToString([]byte("01234567890")), // 11 bytes
	})

	var em protocol.ErrorMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeError), &em)
	if em.RequestID != "agg-over-cap" {
		t.Fatalf("error RequestID = %q, want agg-over-cap (the over-cap body was accepted)", em.RequestID)
	}
}

// ---------------------------------------------------------------------------
// finishPendingBody — reservation sequence (client.go:1023)
// ---------------------------------------------------------------------------

// TestFinishPendingBodyReservationIncrementsSequence proves nextDispatchSeq
// increments (not decrements) across successive calls. nextDispatchSeq is a
// uint64 starting at zero, so INCREMENT_DECREMENT (++  -> --) would underflow
// the first reservation to the maximum uint64 value instead of 1.
func TestFinishPendingBodyReservationIncrementsSequence(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	c.pendingBodies = map[string]*pendingRequestBody{
		"a": {req: protocol.RequestMessage{RequestID: "a"}, timer: time.NewTimer(time.Hour)},
		"b": {req: protocol.RequestMessage{RequestID: "b"}, timer: time.NewTimer(time.Hour)},
	}
	t.Cleanup(func() {
		c.pendingBodiesMu.Lock()
		for _, pb := range c.pendingBodies {
			pb.timer.Stop()
		}
		c.pendingBodiesMu.Unlock()
	})

	_, _, res1, ok := c.finishPendingBody("a")
	if !ok || res1 != 1 {
		t.Fatalf("first reservation = %d, ok=%v, want 1, true", res1, ok)
	}
	_, _, res2, ok := c.finishPendingBody("b")
	if !ok || res2 != 2 {
		t.Fatalf("second reservation = %d, ok=%v, want 2, true", res2, ok)
	}
}

// ---------------------------------------------------------------------------
// connect — welcome.PollInterval boundary at zero (client.go:458)
// ---------------------------------------------------------------------------

// TestConnectWelcomePollIntervalZeroDoesNotResetExisting covers the exact
// boundary of `welcome.PollInterval > 0`: with an existing non-zero
// c.welcomePollInterval and a welcome carrying PollInterval == 0, the field
// must be left untouched. CONDITIONALS_BOUNDARY (`>` -> `>=`) would make
// `0 >= 0` true and incorrectly reset it to 0.
func TestConnectWelcomePollIntervalZeroDoesNotResetExisting(t *testing.T) {
	t.Parallel()

	srv := newControllerServer(t, func(ctrl *websocket.Conn) {
		readAndAckHello(t, ctrl)
		sendWelcomeMsg(t, ctrl, protocol.WelcomeMessage{PollInterval: 0})
		// Close immediately: the pumps finish as soon as the read errors,
		// instead of idling until a hardcoded server-side deadline.
	})

	cfg := &config.Config{
		DrydockURL:        srv,
		HeartbeatInterval: 30,
		WelcomeTimeout:    5,
		DDPollInterval:    300,
		SkipDFCollection:  true,
	}
	c := newWireClient(t, cfg)
	c.welcomePollInterval = 77 // sentinel: must survive a PollInterval==0 welcome

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	established, _ := c.connect(ctx)
	if !established {
		t.Fatal("established = false, want true")
	}
	if c.welcomePollInterval != 77 {
		t.Errorf("welcomePollInterval = %d, want 77 (unchanged by a zero welcome value)", c.welcomePollInterval)
	}
}

// ---------------------------------------------------------------------------
// connect — controller compat mismatch is logged (client.go:470)
// ---------------------------------------------------------------------------

// TestConnectWelcomeCompatMismatchLogsWarning covers the CONDITIONALS_NEGATION
// on `serverMajor != agentMajor`: the mismatch branch's only observable effect
// is the warning log, so a mutation that inverts the comparison silently drops
// it (established stays true either way).
func TestConnectWelcomeCompatMismatchLogsWarning(t *testing.T) {
	// Deliberately NOT t.Parallel(): captures the process-global slog default.

	srv := newControllerServer(t, func(ctrl *websocket.Conn) {
		readAndAckHello(t, ctrl)
		sendWelcomeMsg(t, ctrl, protocol.WelcomeMessage{
			Config: map[string]string{"serverCompatLevel": "99.0"},
		})
		// Close immediately: the pumps finish as soon as the read errors,
		// instead of idling until a hardcoded server-side deadline.
	})

	cfg := &config.Config{
		DrydockURL:        srv,
		HeartbeatInterval: 30,
		WelcomeTimeout:    5,
		DDPollInterval:    300,
		SkipDFCollection:  true,
	}
	c := newWireClient(t, cfg)

	logBuf := &bytes.Buffer{}
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logBuf, nil)))
	defer slog.SetDefault(oldLogger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	established, _ := c.connect(ctx)
	if !established {
		t.Fatal("established = false, want true")
	}
	if !strings.Contains(logBuf.String(), "controller compat level mismatch") {
		t.Errorf("log = %q, want the compat mismatch warning", logBuf.String())
	}
}

// ---------------------------------------------------------------------------
// connect — adapter.OnConnect failure is logged (client.go:514)
// ---------------------------------------------------------------------------

// TestConnectAdapterOnConnectFailureLogsWarning covers the CONDITIONALS_
// NEGATION on OnConnect's error check the same way: established is true
// either way, so the log is the only observable signal.
func TestConnectAdapterOnConnectFailureLogsWarning(t *testing.T) {
	// Deliberately NOT t.Parallel(): captures the process-global slog default.

	srv := newControllerServer(t, func(ctrl *websocket.Conn) {
		readAndAckHello(t, ctrl)
		sendWelcomeMsg(t, ctrl, protocol.WelcomeMessage{})
		// Close immediately: the pumps finish as soon as the read errors,
		// instead of idling until a hardcoded server-side deadline.
	})

	cfg := &config.Config{
		DrydockURL:        srv,
		HeartbeatInterval: 30,
		WelcomeTimeout:    5,
		DDPollInterval:    300,
		SkipDFCollection:  true,
	}
	c := newWireClient(t, cfg)
	c.adapter = &fakeAdapter{onConnectErr: errors.New("sync failed"), caps: []string{}}

	logBuf := &bytes.Buffer{}
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logBuf, nil)))
	defer slog.SetDefault(oldLogger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	established, _ := c.connect(ctx)
	if !established {
		t.Fatal("established = false, want true even when OnConnect errors")
	}
	if !strings.Contains(logBuf.String(), "adapter OnConnect failed") {
		t.Errorf("log = %q, want the OnConnect failure warning", logBuf.String())
	}
}

// ---------------------------------------------------------------------------
// connect — c.conn cleared after the pumps finish (client.go:545)
// ---------------------------------------------------------------------------

// TestConnectClearsConnAfterPumpsFinish covers the CONDITIONALS_NEGATION on
// `c.conn != nil` in connect's post-pump cleanup: c.conn is always non-nil at
// that point in the real flow, so `!= nil` -> `== nil` always skips clearing
// it, leaving a stale reference to an already-torn-down connection.
func TestConnectClearsConnAfterPumpsFinish(t *testing.T) {
	t.Parallel()

	srv := newControllerServer(t, func(ctrl *websocket.Conn) {
		readAndAckHello(t, ctrl)
		sendWelcomeMsg(t, ctrl, protocol.WelcomeMessage{})
		// Close immediately so the read pump errors out and the pumps finish
		// quickly instead of idling for the full context timeout.
	})

	cfg := &config.Config{
		DrydockURL:        srv,
		HeartbeatInterval: 30,
		WelcomeTimeout:    5,
		DDPollInterval:    300,
		SkipDFCollection:  true,
	}
	c := newWireClient(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	established, _ := c.connect(ctx)
	if !established {
		t.Fatal("established = false, want true")
	}

	c.connMu.Lock()
	conn := c.conn
	c.connMu.Unlock()
	if conn != nil {
		t.Error("c.conn was not cleared after the pumps finished")
	}
}

// ---------------------------------------------------------------------------
// readPump — malformed exec_resize / exec_end / error frames are distinctly
// logged and skipped (client.go:760, 768, 822)
// ---------------------------------------------------------------------------

// TestReadPumpMalformedExecResizeLogsAndSkips covers the CONDITIONALS_
// NEGATION on the exec_resize unmarshal-error check: the malformed branch
// logs "invalid exec_resize message" and continues; the mutant would instead
// fall through silently (no such log) into HandleResize with a zero-value
// message.
func TestReadPumpMalformedExecResizeLogsAndSkips(t *testing.T) {
	// Deliberately NOT t.Parallel(): captures the process-global slog default.

	c, ctrl := newTestClient(t)
	runReadPump(t, c)

	logBuf := &bytes.Buffer{}
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logBuf, nil)))
	defer slog.SetDefault(oldLogger)

	badEnv := protocol.Envelope{Type: protocol.TypeExecResize, Data: json.RawMessage(`"notanobject"`)}
	if err := ctrl.WriteJSON(badEnv); err != nil {
		t.Fatalf("write bad exec_resize: %v", err)
	}

	sendEnvelope(t, ctrl, protocol.TypePing, protocol.PingMessage{Timestamp: 200})
	expectType(t, ctrl, protocol.TypePong)

	if !strings.Contains(logBuf.String(), "invalid exec_resize message") {
		t.Errorf("log = %q, want the invalid-exec_resize warning", logBuf.String())
	}
}

// TestReadPumpMalformedExecEndLogsAndSkips is the exec_end counterpart of
// TestReadPumpMalformedExecResizeLogsAndSkips, covering client.go:768.
func TestReadPumpMalformedExecEndLogsAndSkips(t *testing.T) {
	// Deliberately NOT t.Parallel(): captures the process-global slog default.

	c, ctrl := newTestClient(t)
	runReadPump(t, c)

	logBuf := &bytes.Buffer{}
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logBuf, nil)))
	defer slog.SetDefault(oldLogger)

	badEnv := protocol.Envelope{Type: protocol.TypeExecEnd, Data: json.RawMessage(`"notanobject"`)}
	if err := ctrl.WriteJSON(badEnv); err != nil {
		t.Fatalf("write bad exec_end: %v", err)
	}

	sendEnvelope(t, ctrl, protocol.TypePing, protocol.PingMessage{Timestamp: 201})
	expectType(t, ctrl, protocol.TypePong)

	if !strings.Contains(logBuf.String(), "invalid exec_end message") {
		t.Errorf("log = %q, want the invalid-exec_end warning", logBuf.String())
	}
}

// TestReadPumpMalformedErrorMessageLogsAndSkips covers the CONDITIONALS_
// NEGATION on the TypeError unmarshal-error check (client.go:822): a
// malformed error envelope must log "invalid error message" and continue,
// never falling through to log "received error from controller" with an
// empty (zero-value) payload.
func TestReadPumpMalformedErrorMessageLogsAndSkips(t *testing.T) {
	// Deliberately NOT t.Parallel(): captures the process-global slog default.

	c, ctrl := newTestClient(t)
	runReadPump(t, c)

	logBuf := &bytes.Buffer{}
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logBuf, nil)))
	defer slog.SetDefault(oldLogger)

	badEnv := protocol.Envelope{Type: protocol.TypeError, Data: json.RawMessage(`"notanobject"`)}
	if err := ctrl.WriteJSON(badEnv); err != nil {
		t.Fatalf("write bad error envelope: %v", err)
	}

	sendEnvelope(t, ctrl, protocol.TypePing, protocol.PingMessage{Timestamp: 202})
	expectType(t, ctrl, protocol.TypePong)

	out := logBuf.String()
	if !strings.Contains(out, "invalid error message") {
		t.Errorf("log = %q, want the invalid-error-message warning", out)
	}
	if strings.Contains(out, "received error from controller") {
		t.Errorf("log = %q, should not fall through to the parsed-error log", out)
	}
}

// ---------------------------------------------------------------------------
// handleRequestTo — streaming response loop terminates on the first error
// read, sending every chunk that preceded it (client.go:1181)
// ---------------------------------------------------------------------------

// multiChunkReader is an io.Reader that hands back its content across two
// non-empty reads (both returning a nil error) before a final read reports
// io.EOF, forcing the streaming copy loop in handleRequestTo through more
// than one no-error iteration. A single-chunk body can't distinguish
// `readErr != nil` from `readErr == nil`: with only one non-empty read, both
// break at the same point since nothing else is left to send. See
// TestHandleRequestStreamMultipleChunksSendsAllData.
type multiChunkReader struct {
	chunks [][]byte
	i      int
}

func (r *multiChunkReader) Read(p []byte) (int, error) {
	if r.i >= len(r.chunks) {
		return 0, errReaderDone
	}
	n := copy(p, r.chunks[r.i])
	r.i++
	return n, nil
}

var errReaderDone = errors.New("multiChunkReader: no more chunks")

func TestHandleRequestStreamMultipleChunksSendsAllData(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	body := &multiChunkReader{chunks: [][]byte{
		bytes.Repeat([]byte("a"), 100),
		bytes.Repeat([]byte("b"), 100),
	}}
	//nolint:bodyclose // consumed and closed by handleRequestTo, the code under test.
	fd := &fakeDocker{streamResp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       nopReadCloser{body},
	}}
	c.dockerClient = fd

	c.handleRequest(context.Background(), protocol.RequestMessage{
		RequestID: "multi-chunk",
		Method:    "GET",
		Path:      "/containers/abc/logs",
	})

	var resp protocol.ResponseMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeResponse), &resp)
	if !resp.IsStream {
		t.Fatal("IsStream = false, want true")
	}

	var chunk1, chunk2 protocol.StreamMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeStream), &chunk1)
	decodeData(t, expectType(t, ctrl, protocol.TypeStream), &chunk2)

	got1, _ := base64.StdEncoding.DecodeString(chunk1.Data)
	got2, _ := base64.StdEncoding.DecodeString(chunk2.Data)
	if string(got1) != strings.Repeat("a", 100) {
		t.Errorf("first chunk = %q, want 100 a's", got1)
	}
	if string(got2) != strings.Repeat("b", 100) {
		t.Errorf("second chunk = %q, want 100 b's", got2)
	}

	var end protocol.StreamEndMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeStreamEnd), &end)
	if end.RequestID != "multi-chunk" || end.Reason != "complete" {
		t.Errorf("stream_end = %+v, want multi-chunk / complete", end)
	}
}

type nopReadCloser struct{ r *multiChunkReader }

func (n nopReadCloser) Read(p []byte) (int, error) { return n.r.Read(p) }
func (n nopReadCloser) Close() error               { return nil }

// ---------------------------------------------------------------------------
// allowedDockerRequestHeaders — value length boundary (client.go:1303)
// ---------------------------------------------------------------------------

// TestAllowedDockerRequestHeadersLengthBoundary proves the exact boundary of
// `len(value) > 64*1024`: a value of exactly 64KiB is forwarded, one byte
// longer is dropped.
func TestAllowedDockerRequestHeadersLengthBoundary(t *testing.T) {
	t.Parallel()

	atLimit := strings.Repeat("a", 64*1024)
	overLimit := atLimit + "a"

	got := allowedDockerRequestHeaders(map[string]string{"Accept": atLimit})
	if got.Get("Accept") != atLimit {
		t.Error("a 64KiB header value was dropped, want it forwarded")
	}

	got = allowedDockerRequestHeaders(map[string]string{"Accept": overLimit})
	if got.Get("Accept") != "" {
		t.Error("a 64KiB+1 header value was forwarded, want it dropped")
	}
}

// ---------------------------------------------------------------------------
// sendMessageTo — direct-write failure is logged (client.go:1437)
// ---------------------------------------------------------------------------

// TestSendMessageToHandshakePathLogsWriteFailure covers the CONDITIONALS_
// NEGATION on the handshake-path WriteJSON error check: the log is the only
// observable effect (both branches return immediately afterward).
func TestSendMessageToHandshakePathLogsWriteFailure(t *testing.T) {
	// Deliberately NOT t.Parallel(): captures the process-global slog default.

	agent, ctrl := newWSPair(t)
	_ = ctrl.Close()
	_ = agent.Close()

	logBuf := &bytes.Buffer{}
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logBuf, nil)))
	defer slog.SetDefault(oldLogger)

	c := &Client{}
	c.sendMessageTo(outboundTarget{conn: agent}, protocol.Envelope{Type: protocol.TypePing})

	if !strings.Contains(logBuf.String(), "websocket write failed") {
		t.Errorf("log = %q, want the write-failure warning", logBuf.String())
	}
}

// ---------------------------------------------------------------------------
// outboundQueueState.release — negative-byte clamp (client.go:1486)
// ---------------------------------------------------------------------------

// TestOutboundQueueStateReleaseClampsNegativeToZero proves release clamps
// s.bytes to 0 instead of letting it go negative. CONDITIONALS_NEGATION
// (`< 0` -> `>= 0`) would skip the clamp entirely here, since bytes is
// genuinely negative after the subtraction.
func TestOutboundQueueStateReleaseClampsNegativeToZero(t *testing.T) {
	t.Parallel()

	s := &outboundQueueState{bytes: 0}
	env := protocol.Envelope{Type: "x", Data: json.RawMessage(`"abcdefgh"`)}
	s.release(env) // bytes -= size(>0) with nothing reserved -> would go negative

	if s.bytes != 0 {
		t.Errorf("bytes = %d, want 0 (clamped)", s.bytes)
	}
}

// ---------------------------------------------------------------------------
// readDeadline — arithmetic (client.go:1587)
// ---------------------------------------------------------------------------

// TestReadDeadlineComputesDoubleHeartbeat pins the exact value of
// 2*heartbeatSeconds*time.Second for an input large enough that the 60s floor
// doesn't apply, so any single-operator arithmetic mutation changes the
// result away from the expected duration.
func TestReadDeadlineComputesDoubleHeartbeat(t *testing.T) {
	t.Parallel()

	if got := readDeadline(50); got != 100*time.Second {
		t.Errorf("readDeadline(50) = %v, want 100s", got)
	}
}

// ---------------------------------------------------------------------------
// msEdge — arithmetic (client.go:1580)
// ---------------------------------------------------------------------------

// TestMsEdgeComputesMilliseconds pins msEdge's division by 1e6 (nanoseconds
// to milliseconds): ARITHMETIC_BASE (`/` -> `*`) would produce a wildly
// different (huge) number for any non-trivial elapsed time.
func TestMsEdgeComputesMilliseconds(t *testing.T) {
	t.Parallel()

	start := time.Now().Add(-250 * time.Millisecond)
	got := msEdge(start)
	if got < 200 || got > 2000 {
		t.Errorf("msEdge(-250ms) = %v, want roughly 250 (in [200, 2000])", got)
	}
}

// ---------------------------------------------------------------------------
// closeWebSocket — close error is logged (client.go:1609)
// ---------------------------------------------------------------------------

// TestCloseWebSocketLogsErrorOnAlreadyClosed covers the CONDITIONALS_NEGATION
// on the Close() error check: closing an already-closed conn returns an
// error, whose only observable effect is the debug log.
func TestCloseWebSocketLogsErrorOnAlreadyClosed(t *testing.T) {
	// Deliberately NOT t.Parallel(): captures the process-global slog default.

	agent, _ := newWSPair(t)
	_ = agent.Close()

	logBuf := &bytes.Buffer{}
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(oldLogger)

	closeWebSocket(agent, "double-close")

	if !strings.Contains(logBuf.String(), "closing websocket") {
		t.Errorf("log = %q, want the close-error debug message", logBuf.String())
	}
}

// ---------------------------------------------------------------------------
// ensureOperationalState — streamSem initialization (client.go:1700)
// ---------------------------------------------------------------------------

// TestEnsureOperationalStateInitializesStreamSem proves a zero-value Client
// gets a non-nil streamSem. CONDITIONALS_NEGATION (`== nil` -> `!= nil`) would
// skip creating it, since streamSem genuinely is nil going in.
func TestEnsureOperationalStateInitializesStreamSem(t *testing.T) {
	t.Parallel()

	c := &Client{}
	c.ensureOperationalState()

	if c.streamSem == nil {
		t.Fatal("streamSem is nil after ensureOperationalState")
	}
	if cap(c.streamSem) != maxStreams {
		t.Errorf("streamSem cap = %d, want %d", cap(c.streamSem), maxStreams)
	}
}

// ---------------------------------------------------------------------------
// dockerReady — nil response body (client.go:1715)
// ---------------------------------------------------------------------------

// TestDockerReadyNilBodyDoesNotPanic covers the CONDITIONALS_NEGATION on
// `response.Body != nil`: response.Body is a nil io.ReadCloser interface
// here, so the mutant (`== nil`) would call Close on a nil interface value
// and panic.
func TestDockerReadyNilBodyDoesNotPanic(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	fd := &fakeDocker{doResp: &http.Response{StatusCode: http.StatusOK, Body: nil}}
	c.dockerClient = fd

	if !c.dockerReady(context.Background()) {
		t.Error("dockerReady = false, want true for a 200 response with a nil body")
	}
}

// ---------------------------------------------------------------------------
// writePump — poll tick error paths are logged (client.go:1361, 1365)
// ---------------------------------------------------------------------------

// TestWritePumpPollRefreshErrorLogsAndSkipsNotify covers the CONDITIONALS_
// NEGATION on `err != nil` after RefreshContainers: the error branch logs
// "container refresh failed" and continues (skipping OnContainerRefresh); the
// mutant would silently fall through without that log.
func TestWritePumpPollRefreshErrorLogsAndSkipsNotify(t *testing.T) {
	// Deliberately NOT t.Parallel(): captures the process-global slog default.

	c, _ := newTestClient(t)
	wc := newWireClient(t, &config.Config{SkipDFCollection: true})
	c.collector = wc.collector
	c.cfg.HeartbeatInterval = 999
	c.cfg.DDPollInterval = 999
	c.welcomePollInterval = 1 // fast poll tick
	c.adapter = &errRefreshAdapter{fakeAdapter: fakeAdapter{pollInterval: 999}}

	runSendPump(t, c)

	logBuf := &bytes.Buffer{}
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logBuf, nil)))
	defer slog.SetDefault(oldLogger)

	ctx, cancel := context.WithCancel(context.Background())
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		c.writePump(ctx)
	}()
	t.Cleanup(cancel)

	time.Sleep(1100 * time.Millisecond)
	cancel()
	<-pumpDone // writePump must have returned before logBuf is read below

	if !strings.Contains(logBuf.String(), "container refresh failed") {
		t.Errorf("log = %q, want the refresh-failed warning", logBuf.String())
	}
}

// TestWritePumpPollOnContainerRefreshErrorLogs covers the CONDITIONALS_
// NEGATION on `err != nil` after OnContainerRefresh (client.go:1365).
func TestWritePumpPollOnContainerRefreshErrorLogs(t *testing.T) {
	// Deliberately NOT t.Parallel(): captures the process-global slog default.

	c, _ := newTestClient(t)
	wc := newWireClient(t, &config.Config{SkipDFCollection: true})
	c.collector = wc.collector
	c.cfg.HeartbeatInterval = 999
	c.cfg.DDPollInterval = 999
	c.welcomePollInterval = 1
	c.adapter = &errOnRefreshAdapter{fakeAdapter: fakeAdapter{pollInterval: 999}}

	runSendPump(t, c)

	logBuf := &bytes.Buffer{}
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logBuf, nil)))
	defer slog.SetDefault(oldLogger)

	ctx, cancel := context.WithCancel(context.Background())
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		c.writePump(ctx)
	}()
	t.Cleanup(cancel)

	time.Sleep(1100 * time.Millisecond)
	cancel()
	<-pumpDone // writePump must have returned before logBuf is read below

	if !strings.Contains(logBuf.String(), "container refresh notify failed") {
		t.Errorf("log = %q, want the notify-failed warning", logBuf.String())
	}
}
