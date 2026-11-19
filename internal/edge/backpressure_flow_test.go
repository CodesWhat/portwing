// SPDX-License-Identifier: MIT

package edge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/codeswhat/portwing/internal/adapter/drydock"
	"github.com/codeswhat/portwing/internal/config"
	"github.com/codeswhat/portwing/internal/docker"
	"github.com/codeswhat/portwing/internal/protocol"
)

// flowReadTimeout bounds one controller-side read in the flow tests. It is
// generous on purpose: a throttled consumer under -race is slow, and a hang
// is what the test is hunting, not latency.
const flowReadTimeout = 30 * time.Second

// readFlowEnvelope reads one envelope from ctrl with the generous flow timeout.
func readFlowEnvelope(t *testing.T, ctrl *websocket.Conn) protocol.Envelope {
	t.Helper()
	if err := ctrl.SetReadDeadline(time.Now().Add(flowReadTimeout)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, raw, err := ctrl.ReadMessage()
	if err != nil {
		t.Fatalf("controller read failed (tunnel dropped?): %v", err)
	}
	var env protocol.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	return env
}

// requireTunnelUp proves the agent connection is still usable by round-tripping
// a pong through the send queue.
func requireTunnelUp(t *testing.T, c *Client, ctrl *websocket.Conn) {
	t.Helper()
	if err := c.sendTypedMessage(protocol.TypePong, protocol.PongMessage{Timestamp: 99}); err != nil {
		t.Fatalf("send pong: %v", err)
	}
	for {
		env := readFlowEnvelope(t, ctrl)
		if env.Type == protocol.TypePong {
			return
		}
	}
}

// newLogDockerClient serves N multiplexed one-line log frames for any
// container's /logs endpoint over a unix socket.
func newLogDockerClient(t *testing.T, lines int) *docker.Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "bp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "d.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/logs") {
			for i := 0; i < lines; i++ {
				payload := fmt.Sprintf("line %d\n", i)
				header := make([]byte, 8)
				header[0] = 1
				binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
				if _, err := w.Write(append(header, payload...)); err != nil {
					return
				}
			}
			return
		}
		_, _ = w.Write([]byte(`{"ApiVersion":"1.44"}`))
	}))
	_ = srv.Listener.Close()
	srv.Listener = listener
	srv.Start()
	t.Cleanup(srv.Close)
	dc, err := docker.NewClient(socket, 60)
	if err != nil {
		t.Fatal(err)
	}
	return dc
}

// A consumer slower than the producer must throttle the Drydock log stream,
// not evict the tunnel: every line arrives, in order, then the end frame, and
// the connection is still open.
func TestDrydockLogStreamSurvivesThrottledConsumer(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		lines int
		every int // consumer sleeps 1ms after this many frames
	}{
		{"5000", 5000, 10},
		{"50000", 50000, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, ctrl := newTestClient(t)
			c.adapter = drydock.NewAdapter(newLogDockerClient(t, tc.lines), "test-agent", drydock.AgentInfo{})

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			data, _ := json.Marshal(protocol.DDContainerLogRequestMessage{
				RequestID: "logs-1", ContainerID: "c1", Stream: true, Tail: tc.lines,
			})
			if !c.adapter.HandleMessage(ctx, c.currentMessageSender(), protocol.TypeDDContainerLogRequest, data) {
				t.Fatal("log request not handled")
			}

			var got bytes.Buffer
			for frames := 1; ; frames++ {
				env := readFlowEnvelope(t, ctrl)
				if frames%tc.every == 0 {
					time.Sleep(time.Millisecond)
				}
				if env.Type == protocol.TypeDDContainerLogEnd {
					break
				}
				if env.Type != protocol.TypeDDContainerLogChunk {
					t.Fatalf("unexpected frame %q: %s", env.Type, env.Data)
				}
				var chunk protocol.DDContainerLogChunkMessage
				decodeData(t, env.Data, &chunk)
				got.WriteString(chunk.Logs)
			}

			var want strings.Builder
			for i := 0; i < tc.lines; i++ {
				fmt.Fprintf(&want, "line %d\n", i)
			}
			if got.String() != want.String() {
				t.Fatalf("log content mismatch: got %d bytes, want %d", got.Len(), want.Len())
			}
			requireTunnelUp(t, c, ctrl)
		})
	}
}

var patternTable = func() []byte {
	table := make([]byte, 4093)
	for i := range table {
		table[i] = byte(i * 7)
	}
	return table
}()

// patternReader yields n bytes of a deterministic pattern and records their
// SHA-256 so the receiving side can verify nothing was dropped or reordered.
type patternReader struct {
	remaining int
	offset    int
	hash      interface {
		Write([]byte) (int, error)
	}
}

func (r *patternReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(p), r.remaining)
	// Copy from a prime-length table (instead of computing every byte) so the
	// 64 MiB case stays fast under -race.
	for done := 0; done < n; {
		done += copy(p[done:n], patternTable[(r.offset+done)%len(patternTable):])
	}
	_, _ = r.hash.Write(p[:n])
	r.offset += n
	r.remaining -= n
	return n, nil
}

// A 64 MiB Docker stream with a throttled consumer ends with stream_end
// "complete" on the same connection. Before backpressure it evicted after a
// few hundred frames.
func TestHandleRequestLargeStreamWithThrottledConsumer(t *testing.T) {
	t.Parallel()

	const total = 64 << 20
	c, ctrl := newTestClient(t)
	//nolint:bodyclose // closed by handleRequest, the code under test.
	c.dockerClient = &fakeDocker{streamResp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/octet-stream"}},
		Body:       io.NopCloser(&patternReader{remaining: total, hash: sha256.New()}),
	}}

	go c.handleRequest(context.Background(), protocol.RequestMessage{
		RequestID: "big", Method: "GET", Path: "/containers/abc/logs?follow=1",
	})

	// Verifying every byte of 64 MiB under -race costs minutes, so each chunk
	// is checked against the expected pattern at its stream offset only for
	// every 16th frame (and the byte count is checked for all of them).
	expected := &patternReader{remaining: total, hash: sha256.New()}
	size, chunks := 0, 0
	for frames := 1; ; frames++ {
		env := readFlowEnvelope(t, ctrl)
		if frames%16 == 0 {
			time.Sleep(time.Millisecond)
		}
		if env.Type == protocol.TypeResponse {
			continue
		}
		if env.Type == protocol.TypeStreamEnd {
			var end protocol.StreamEndMessage
			decodeData(t, env.Data, &end)
			if end.Reason != "complete" {
				t.Fatalf("stream_end reason = %q, want complete", end.Reason)
			}
			break
		}
		var chunk protocol.StreamMessage
		decodeData(t, env.Data, &chunk)
		// Chunks are full 32 KiB reads of a reader that always fills the
		// buffer, so base64 length gives the decoded size without decoding.
		n := base64.StdEncoding.DecodedLen(len(chunk.Data)) - strings.Count(chunk.Data[max(0, len(chunk.Data)-2):], "=")
		if chunks%16 == 0 {
			raw, err := base64.StdEncoding.DecodeString(chunk.Data)
			if err != nil {
				t.Fatalf("decode chunk: %v", err)
			}
			want := make([]byte, len(raw))
			expected.offset = size
			_, _ = expected.Read(want)
			if !bytes.Equal(raw, want) {
				t.Fatalf("chunk %d at offset %d differs from the sent stream", chunks, size)
			}
		}
		size += n
		chunks++
	}
	if size != total {
		t.Fatalf("received %d bytes, want %d", size, total)
	}
	requireTunnelUp(t, c, ctrl)
}

// 8 MiB of exec output read through readLoop arrives complete and in order
// with a throttled consumer; the tunnel is not evicted.
func TestExecOutputWithThrottledConsumer(t *testing.T) {
	t.Parallel()

	const total = 8 << 20
	c, ctrl := newTestClient(t)
	pipe, peer := net.Pipe()
	session := newExecSession(c, "e1", pipe)
	sent := sha256.New()
	go func() {
		_, _ = io.Copy(peer, &patternReader{remaining: total, hash: sent})
		_ = peer.Close()
	}()
	go session.readLoop()

	received := sha256.New()
	size := 0
	for {
		env := readFlowEnvelope(t, ctrl)
		time.Sleep(time.Millisecond)
		if env.Type == protocol.TypeExecEnd {
			var end protocol.ExecEndMessage
			decodeData(t, env.Data, &end)
			if end.Reason != "exited" {
				t.Fatalf("exec_end reason = %q, want exited", end.Reason)
			}
			break
		}
		var out protocol.ExecOutputMessage
		decodeData(t, env.Data, &out)
		raw, err := base64.StdEncoding.DecodeString(out.Data)
		if err != nil {
			t.Fatalf("decode output: %v", err)
		}
		size += len(raw)
		received.Write(raw)
	}
	if size != total || !bytes.Equal(received.Sum(nil), sent.Sum(nil)) {
		t.Fatalf("received %d bytes (hash match %v), want %d", size, bytes.Equal(received.Sum(nil), sent.Sum(nil)), total)
	}
	requireTunnelUp(t, c, ctrl)
}

// installUndrainedQueue gives the client a queue nobody drains, the model of a
// controller that has stopped reading.
func installUndrainedQueue(c *Client, size int) (chan protocol.Envelope, *outboundQueueState) {
	ch := make(chan protocol.Envelope, size)
	state := &outboundQueueState{}
	c.connMu.Lock()
	c.sendCh = ch
	c.sendState = state
	c.connMu.Unlock()
	return ch, state
}

func bulkEnvelope() protocol.Envelope {
	return protocol.Envelope{Type: protocol.TypeStream, Data: json.RawMessage(`{"requestId":"r","data":"eA=="}`)}
}

func waitForBulkWaiter(t *testing.T, state *outboundQueueState) {
	t.Helper()
	waitFor(t, "a bulk producer to block", func() bool {
		state.mu.Lock()
		defer state.mu.Unlock()
		return state.wake != nil
	})
}

// A bulk frame that finds the queue above its high-water mark waits for the
// pump to release capacity instead of evicting the connection, and control
// frames still fit in the headroom above that mark.
func TestBulkSendBlocksUntilPumpReleasesCapacity(t *testing.T) {
	t.Parallel()

	c, _ := newHandshakeTestClient(t)
	ch, state := installUndrainedQueue(c, 8) // bulk high-water mark: 6 frames
	target := c.currentOutboundTarget()

	for i := 0; i < bulkFrameLimit(ch); i++ {
		if err := c.sendBulkMessageTo(context.Background(), target, bulkEnvelope()); err != nil {
			t.Fatalf("bulk send %d: %v", i, err)
		}
	}

	done := make(chan error, 1)
	go func() { done <- c.sendBulkMessageTo(context.Background(), target, bulkEnvelope()) }()
	waitForBulkWaiter(t, state)
	select {
	case err := <-done:
		t.Fatalf("bulk send returned %v while the queue was above the high-water mark", err)
	default:
	}

	// Headroom is reserved for control frames: they enqueue right now.
	c.sendMessage(protocol.Envelope{Type: protocol.TypePong, Data: json.RawMessage(`{}`)})
	if got := len(ch); got != bulkFrameLimit(ch)+1 {
		t.Fatalf("queue depth after control frame = %d, want %d", got, bulkFrameLimit(ch)+1)
	}

	// The pump finishing frames is what releases the waiter. The control frame
	// took one slot above the mark, so two frames have to drain before the
	// bulk frame fits again.
	state.release(<-ch)
	state.release(<-ch)
	if err := <-done; err != nil {
		t.Fatalf("bulk send after release: %v", err)
	}
	if err := c.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)); err != nil {
		t.Fatalf("connection was evicted by backpressure: %v", err)
	}
}

// Bulk producers stop at the byte high-water mark too, leaving the rest of the
// byte budget to control frames.
func TestBulkSendBlocksAtByteHighWaterMark(t *testing.T) {
	t.Parallel()

	ch := make(chan protocol.Envelope, sendQueueSize)
	state := &outboundQueueState{}
	big := protocol.Envelope{Data: make(json.RawMessage, 32<<20)}
	for i := 0; i < 3; i++ {
		if got := state.enqueueWait(context.Background(), ch, big, time.Second); got != outboundEnqueued {
			t.Fatalf("bulk enqueue %d = %d, want outboundEnqueued", i, got)
		}
	}
	// 96 MiB queued: the byte high-water mark. A bulk frame waits (and here
	// stalls out), while a control frame still fits under the hard limit.
	if got := state.enqueueWait(context.Background(), ch, bulkEnvelope(), 20*time.Millisecond); got != outboundStalled {
		t.Fatalf("bulk enqueue at byte high-water mark = %d, want outboundStalled", got)
	}
	if got := state.enqueue(ch, protocol.Envelope{Type: protocol.TypePong, Data: json.RawMessage(`{}`)}); got != outboundEnqueued {
		t.Fatalf("control enqueue in byte headroom = %d, want outboundEnqueued", got)
	}
	if got := state.enqueueWait(context.Background(), ch, protocol.Envelope{Data: make(json.RawMessage, 97<<20)}, time.Second); got != outboundByteLimitExceeded {
		t.Fatalf("oversized bulk frame = %d, want outboundByteLimitExceeded", got)
	}
}

func TestEnqueueWaitReturnsCanceledAndClosed(t *testing.T) {
	t.Parallel()

	ch := make(chan protocol.Envelope, 4)
	state := &outboundQueueState{}
	for i := 0; i < bulkFrameLimit(ch); i++ {
		state.enqueue(ch, bulkEnvelope())
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan outboundEnqueueResult, 1)
	go func() { done <- state.enqueueWait(ctx, ch, bulkEnvelope(), time.Minute) }()
	waitForBulkWaiter(t, state)
	cancel()
	if got := <-done; got != outboundCanceled {
		t.Fatalf("canceled wait = %d, want outboundCanceled", got)
	}

	go func() { done <- state.enqueueWait(context.Background(), ch, bulkEnvelope(), time.Minute) }()
	waitForBulkWaiter(t, state)
	state.closeAndDiscard(ch)
	if got := <-done; got != outboundQueueClosed {
		t.Fatalf("wait on closing queue = %d, want outboundQueueClosed", got)
	}
}

// A consumer that makes no progress for the whole stall deadline is evicted
// with the stalled error. The deadline is shrunk so the test doesn't wait 30s.
func TestStalledConsumerIsEvictedAfterStallDeadline(t *testing.T) {
	// Deliberately NOT t.Parallel(): overrides the package-level streamStallTimeout.
	old := streamStallTimeout
	streamStallTimeout = 50 * time.Millisecond
	t.Cleanup(func() { streamStallTimeout = old })

	c, ctrl := newHandshakeTestClient(t)
	c.metrics = newEdgeMetricsForBackpressureTest()
	ch, _ := installUndrainedQueue(c, 4)
	target := c.currentOutboundTarget()

	var err error
	for i := 0; i <= bulkFrameLimit(ch); i++ {
		err = c.sendBulkMessageTo(context.Background(), target, bulkEnvelope())
	}
	if !errors.Is(err, errSendQueueStalled) {
		t.Fatalf("send to a stalled queue = %v, want %v", err, errSendQueueStalled)
	}
	if err := ctrl.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	if _, _, err := ctrl.ReadMessage(); err == nil {
		t.Fatal("connection stayed open after the stall deadline")
	}
	requireBackpressureCount(t, c.metrics, 1)
}

// A consumer that is slow but moving restarts the stall window with every
// frame it takes, so a producer well past the deadline in total is not evicted.
// Capacity stays unavailable throughout: each wake comes from a release that
// leaves the queue at its frame limit, so the producer can only survive the
// original deadline because the window was reset, not because room appeared.
func TestSlowButMovingConsumerIsNotEvicted(t *testing.T) {
	t.Parallel()

	const (
		stall    = 200 * time.Millisecond
		interval = 40 * time.Millisecond
		beats    = 8 // 320ms of progress, past the original 200ms deadline
	)
	ch := make(chan protocol.Envelope, 4)
	state := &outboundQueueState{}
	for i := 0; i < bulkFrameLimit(ch); i++ {
		state.enqueue(ch, bulkEnvelope())
	}
	done := make(chan outboundEnqueueResult, 1)
	go func() { done <- state.enqueueWait(context.Background(), ch, bulkEnvelope(), stall) }()
	waitForBulkWaiter(t, state)

	for i := 0; i < beats; i++ {
		time.Sleep(interval)
		// Progress signal without freeing a slot: len(ch) stays at the limit.
		state.release(bulkEnvelope())
		select {
		case got := <-done:
			t.Fatalf("producer returned %d after %d progress signals with no capacity free", got, i+1)
		default:
		}
	}

	state.release(<-ch)
	select {
	case got := <-done:
		if got != outboundEnqueued {
			t.Fatalf("enqueue after sustained progress = %d, want outboundEnqueued", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("producer never enqueued once capacity freed")
	}
}

// While a bulk stream saturates the queue, a ping/pong round trip and a small
// concurrent request both still complete, and nothing is evicted.
func TestControlFramesCompleteWhileBulkSaturatesQueue(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	//nolint:bodyclose // closed by handleRequest, the code under test.
	c.dockerClient = &fakeDocker{doResp: mkResp(http.StatusOK, "application/json", `{"ok":true}`)}
	target := c.currentOutboundTarget()

	bulkCtx, stopBulk := context.WithCancel(context.Background())
	bulkDone := make(chan struct{})
	payload := base64.StdEncoding.EncodeToString(make([]byte, 48<<10))
	go func() {
		defer close(bulkDone)
		for {
			err := c.sendBulkTypedMessageTo(bulkCtx, target, protocol.TypeStream, protocol.StreamMessage{RequestID: "bulk", Data: payload})
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { stopBulk(); <-bulkDone })

	// The controller reads nothing yet, so the TCP buffers fill, the pump
	// blocks on its write, and the queue climbs to the bulk high-water mark.
	waitFor30(t, "queue to reach the bulk high-water mark", func() bool {
		return len(target.ch) >= bulkFrameLimit(target.ch)
	})

	if err := c.sendTypedMessage(protocol.TypePong, protocol.PongMessage{Timestamp: 7}); err != nil {
		t.Fatalf("send pong: %v", err)
	}
	c.handleRequest(context.Background(), protocol.RequestMessage{RequestID: "small", Method: "GET", Path: "/containers/json"})

	gotPong, gotResp := false, false
	for !gotPong || !gotResp {
		env := readFlowEnvelope(t, ctrl)
		switch env.Type {
		case protocol.TypePong:
			gotPong = true
		case protocol.TypeResponse:
			var resp protocol.ResponseMessage
			decodeData(t, env.Data, &resp)
			gotResp = gotResp || resp.RequestID == "small"
		}
	}
}

func waitFor30(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(flowReadTimeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// A non-stream body over the cap is rejected with a TypeError for that request
// instead of being truncated and sent as success, and the connection stays up.
func TestOverCapNonStreamBodyYieldsErrorNotTruncation(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	//nolint:bodyclose // closed by handleRequest, the code under test.
	c.dockerClient = &fakeDocker{doResp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(&patternReader{remaining: maxResponseBody + 1, hash: sha256.New()}),
	}}

	c.handleRequest(context.Background(), protocol.RequestMessage{RequestID: "over", Method: "GET", Path: "/images/json"})

	var e protocol.ErrorMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeError), &e)
	if e.RequestID != "over" || !strings.Contains(e.Message, "exceeds") || !strings.Contains(e.Message, "streaming") {
		t.Fatalf("error = %+v, want an exceeds-cap error for request over", e)
	}
	if e.Code != "response_too_large" {
		t.Fatalf("code = %q, want response_too_large", e.Code)
	}
	requireTunnelUp(t, c, ctrl)
}

// A connection that drops right after the welcome must not reset the backoff:
// the delays keep doubling up to the cap instead of flapping at the base.
func TestBackoffNotResetByConnectionDroppingRightAfterWelcome(t *testing.T) {
	// Deliberately NOT t.Parallel(): overrides the package-level reconnectWait var.
	delays := runWelcomeThenDropReconnects(t, 4, minStableConnection)

	nominal := time.Second
	for i, d := range delays {
		if d < time.Duration(float64(nominal)*0.75) || d > time.Duration(float64(nominal)*1.25) {
			t.Fatalf("delay[%d] = %v, want about %v (backoff must keep growing)", i, d, nominal)
		}
		nominal = min(nominal*2, 4*time.Second)
	}
}

// The stability window starts at the welcome, not at connect: a connection
// whose handshake took longer than minStableConnection but that drops 2s after
// the welcome is not stable and must not reset the backoff.
func TestBackoffWindowStartsAtWelcomeNotConnect(t *testing.T) {
	// Deliberately NOT t.Parallel(): overrides package-level vars.
	// The clock is read twice per connection, at the welcome and after the
	// connection ends, so scripted readings model a welcome 40s into the
	// handshake followed by a drop 2s later.
	var calls atomic.Int64
	readings := []time.Duration{40 * time.Second, 42 * time.Second}
	base := time.Now()
	clock := func() time.Time {
		i := calls.Add(1) - 1
		return base.Add(readings[i%int64(len(readings))])
	}
	delays := runWelcomeThenDropReconnectsClock(t, 4, minStableConnection, clock)

	nominal := time.Second
	for i, d := range delays {
		if d < time.Duration(float64(nominal)*0.75) || d > time.Duration(float64(nominal)*1.25) {
			t.Fatalf("delay[%d] = %v, want about %v (a 2s-old connection is not stable)", i, d, nominal)
		}
		nominal = min(nominal*2, 4*time.Second)
	}
}

// A connection that stayed up for minStableConnection does reset the backoff.
func TestBackoffResetAfterStableConnection(t *testing.T) {
	// Deliberately NOT t.Parallel(): overrides package-level vars.
	delays := runWelcomeThenDropReconnects(t, 4, 0)

	for i, d := range delays {
		if d > time.Duration(float64(time.Second)*1.25) {
			t.Fatalf("delay[%d] = %v, want about 1s: a stable connection resets the backoff", i, d)
		}
	}
}

// runWelcomeThenDropReconnects runs the client against a controller that
// welcomes and then immediately closes, with the stability threshold set to
// stable, and returns the first n reconnect delays.
func runWelcomeThenDropReconnects(t *testing.T, n int, stable time.Duration) []time.Duration {
	t.Helper()
	return runWelcomeThenDropReconnectsClock(t, n, stable, nil)
}

// runWelcomeThenDropReconnectsClock is runWelcomeThenDropReconnects with an
// injected client clock (nil keeps the real one).
func runWelcomeThenDropReconnectsClock(t *testing.T, n int, stable time.Duration, clock func() time.Time) []time.Duration {
	t.Helper()

	srv := newControllerServer(t, func(ctrl *websocket.Conn) {
		readAndAckHello(t, ctrl)
		sendWelcomeMsg(t, ctrl, protocol.WelcomeMessage{})
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var delays []time.Duration
	oldWait, oldStable := reconnectWait, minStableConnection
	minStableConnection = stable
	reconnectWait = func(d time.Duration) <-chan time.Time {
		mu.Lock()
		delays = append(delays, d)
		if len(delays) >= n {
			cancel()
		}
		mu.Unlock()
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}
	t.Cleanup(func() { reconnectWait, minStableConnection = oldWait, oldStable })

	addr := freeAddr(t)
	c := newWireClient(t, &config.Config{
		DrydockURL:        srv,
		HeartbeatInterval: 30,
		WelcomeTimeout:    5,
		ReconnectDelay:    1,
		MaxReconnectDelay: 4,
		DDPollInterval:    300,
		BindAddress:       "127.0.0.1",
		Port:              portFrom(addr),
		SkipDFCollection:  true,
	})
	c.now = clock
	_ = c.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if len(delays) < n {
		t.Fatalf("recorded %d reconnect delays, want %d", len(delays), n)
	}
	return delays[:n]
}

// startBlockedExecOutput returns a session whose read loop is parked in a bulk
// send because the (undrained) queue sits at its bulk high-water mark, plus the
// queue and a channel closed when the loop exits.
func startBlockedExecOutput(t *testing.T, c *Client) (*ExecSession, chan protocol.Envelope, *outboundQueueState, chan struct{}) {
	t.Helper()

	ch, state := installUndrainedQueue(c, 4)
	for i := 0; i < bulkFrameLimit(ch); i++ {
		state.enqueue(ch, bulkEnvelope())
	}
	readBlock := make(chan struct{})
	t.Cleanup(func() { close(readBlock) })
	conn := &fakeConn{reads: [][]byte{[]byte("output")}, blockRead: readBlock}
	session := newExecSession(c, "blocked", conn)
	session.ctx, session.cancel = context.WithCancel(context.Background())
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		session.readLoop()
	}()
	waitForBulkWaiter(t, state)
	return session, ch, state, exited
}

func drainQueue(ch chan protocol.Envelope) []protocol.Envelope {
	var out []protocol.Envelope
	for {
		select {
		case env := <-ch:
			out = append(out, env)
		default:
			return out
		}
	}
}

// When the input writer gives up and closes the session while the read loop is
// blocked in a bulk send, the tunnel is still up, so the controller still gets
// its exec_end instead of a session that never ends.
func TestExecEndSentWhenOutputCancelledUnderBackpressure(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	session, ch, _, exited := startBlockedExecOutput(t, c)

	session.Close()
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("read loop did not exit after the session closed")
	}

	var ends []protocol.ExecEndMessage
	for _, env := range drainQueue(ch) {
		if env.Type == protocol.TypeExecEnd {
			var end protocol.ExecEndMessage
			decodeData(t, env.Data, &end)
			ends = append(ends, end)
		}
	}
	if len(ends) != 1 || ends[0].ExecID != "blocked" || ends[0].Reason != "exec session closed" {
		t.Fatalf("exec_end frames = %+v, want exactly one for blocked with reason %q", ends, "exec session closed")
	}
}

// A session the controller or a tunnel drop ended owns its own exec_end, so a
// cancelled output send adds none.
func TestExecEndNotSentWhenOutputCancelledByPeer(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	session, ch, _, exited := startBlockedExecOutput(t, c)

	session.endByPeer()
	<-exited

	for _, env := range drainQueue(ch) {
		if env.Type == protocol.TypeExecEnd {
			t.Fatal("exec_end sent for a peer-ended session")
		}
	}
}

// With the queue closed the connection is gone, so there is nowhere to send an
// exec_end and nothing may be enqueued.
func TestExecEndNotSentWhenQueueClosed(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	session, ch, state, exited := startBlockedExecOutput(t, c)

	// Hold the state lock so the queue is marked closed before the loop wakes:
	// the cancel lands first, then the close, then the loop re-checks.
	state.mu.Lock()
	session.cancel()
	state.closed = true
	state.mu.Unlock()
	<-exited

	for _, env := range drainQueue(ch) {
		if env.Type == protocol.TypeExecEnd {
			t.Fatal("exec_end enqueued on a closed queue")
		}
	}
}
