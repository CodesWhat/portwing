// Package edge tests provide an in-process mock drydock WebSocket server that
// speaks enough lookout/1.0 to exercise the Client without a real Docker daemon
// or network.  All tests use the real frame format from internal/protocol, so
// marshalling and unmarshalling are exercised end-to-end.
package edge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/codeswhat/lookout/internal/adapter"
	"github.com/codeswhat/lookout/internal/audit"
	"github.com/codeswhat/lookout/internal/config"
	"github.com/codeswhat/lookout/internal/docker"
	"github.com/codeswhat/lookout/internal/metrics"
	"github.com/codeswhat/lookout/internal/protocol"
)

// ---- mock drydock server ---------------------------------------------------

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true },
}

// mockDrydock is a minimal in-process WebSocket server that speaks lookout/1.0.
type mockDrydock struct {
	ts *httptest.Server

	mu     sync.Mutex
	conn   *websocket.Conn // most recent connected client; guarded by mu
	sendMu sync.Mutex      // serialises outbound writes on conn (gorilla is not concurrent-safe for writes)

	allFrames []protocol.Envelope    // every frame received from the client; guarded by mu
	frameC    chan protocol.Envelope // non-blocking fan-out of received frames

	rejectHello bool // if true, send error instead of welcome
}

func newMockDrydock(t *testing.T) *mockDrydock {
	t.Helper()
	md := &mockDrydock{
		frameC: make(chan protocol.Envelope, 512),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/lookout/ws", md.handleWS)
	md.ts = httptest.NewServer(mux)
	t.Cleanup(md.ts.Close)
	return md
}

func (md *mockDrydock) wsURL() string {
	return "ws" + md.ts.URL[len("http"):]
}

func (md *mockDrydock) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	md.mu.Lock()
	md.conn = conn
	md.mu.Unlock()

	// Read the first frame — must be hello.
	_, msg, err := conn.ReadMessage()
	if err != nil {
		return
	}
	var env protocol.Envelope
	if err := json.Unmarshal(msg, &env); err != nil {
		return
	}
	md.record(env)

	if env.Type != protocol.TypeHello {
		return
	}

	if md.rejectHello {
		// Send an error frame to reject the hello.
		raw, _ := json.Marshal(protocol.ErrorMessage{Message: "rejected"})
		_ = conn.WriteJSON(protocol.Envelope{
			Type: protocol.TypeError,
			Data: json.RawMessage(raw),
		})
		return
	}

	// Send welcome.
	raw, _ := json.Marshal(protocol.WelcomeMessage{PollInterval: 300})
	if err := conn.WriteJSON(protocol.Envelope{
		Type: protocol.TypeWelcome,
		Data: json.RawMessage(raw),
	}); err != nil {
		return
	}

	// Pump subsequent client frames into the record + channel.
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var env protocol.Envelope
		if err := json.Unmarshal(msg, &env); err != nil {
			continue
		}
		md.record(env)
	}
}

func (md *mockDrydock) record(env protocol.Envelope) {
	md.mu.Lock()
	md.allFrames = append(md.allFrames, env)
	md.mu.Unlock()

	select {
	case md.frameC <- env:
	default:
	}
}

// send pushes a frame to the connected client (best-effort).
// The sendMu serialises writes because gorilla Conn is not safe for concurrent writes.
func (md *mockDrydock) send(env protocol.Envelope) error {
	md.mu.Lock()
	conn := md.conn
	md.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("no connected client")
	}
	md.sendMu.Lock()
	defer md.sendMu.Unlock()
	return conn.WriteJSON(env)
}

// sendTyped wraps data in an Envelope and sends it.
func (md *mockDrydock) sendTyped(msgType string, data interface{}) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return md.send(protocol.Envelope{Type: msgType, Data: json.RawMessage(raw)})
}

// waitFrame blocks until a frame of the given type arrives, up to 2 s.
func (md *mockDrydock) waitFrame(t *testing.T, wantType string) protocol.Envelope {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case env := <-md.frameC:
			if env.Type == wantType {
				return env
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for frame type %q", wantType)
		}
	}
}

// ---- makePipeWS: in-process WebSocket pair without a server ----------------

// makePipeWS creates a pair of gorilla WebSocket connections connected via
// net.Pipe — no TCP port or httptest server needed.  The server-side conn
// uses the HTTP upgrade handshake automatically; call within a goroutine
// to avoid deadlock.
//
//	cliConn, srvConn := makePipeWS(t)
//
// srvConn behaves as the "drydock side"; cliConn is set into c.conn.
func makePipeWS(t *testing.T) (cliConn, srvConn *websocket.Conn) {
	t.Helper()

	// We need a real HTTP upgrade because gorilla doesn't expose a low-level
	// constructor.  Use an in-process httptest server that immediately
	// upgrades and returns the conn via a channel.
	connC := make(chan *websocket.Conn, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		connC <- c
		// Keep the handler alive until the test ends.
		<-r.Context().Done()
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	wsURL := "ws" + ts.URL[len("http"):] + "/ws"
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	cli, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("makePipeWS dial: %v", err)
	}
	t.Cleanup(func() { cli.Close() })

	srv := <-connC
	t.Cleanup(func() { srv.Close() })

	return cli, srv
}

// ---- noop EdgeAdapter ------------------------------------------------------

// noopEdgeAdapter satisfies adapter.EdgeAdapter with no-op implementations.
type noopEdgeAdapter struct{}

func (noopEdgeAdapter) Name() string                            { return "noop" }
func (noopEdgeAdapter) Capabilities() []string                  { return nil }
func (noopEdgeAdapter) HelloExtension() *adapter.HelloExtension { return nil }
func (noopEdgeAdapter) OnConnect(_ context.Context, _ adapter.MessageSender) error {
	return nil
}
func (noopEdgeAdapter) RefreshContainers(_ context.Context) (
	added, updated, removed []adapter.Container, err error,
) {
	return nil, nil, nil, nil
}
func (noopEdgeAdapter) OnContainerRefresh(
	_ context.Context, _ adapter.MessageSender,
	_, _, _ []adapter.Container,
) error {
	return nil
}
func (noopEdgeAdapter) PollInterval() int { return 300 }
func (noopEdgeAdapter) HandleMessage(
	_ context.Context, _ adapter.MessageSender, _ string, _ json.RawMessage,
) bool {
	return false
}

// ---- test helpers ----------------------------------------------------------

// newTestConfig returns a minimal config pointing at the mock server.
func newTestConfig(wsURL string) *config.Config {
	// connect() does: wsURL = cfg.DrydockURL + "/api/lookout/ws"
	// and replaces "http://" → "ws://", so DrydockURL must be http-scheme.
	drydockURL := "http" + wsURL[len("ws"):]
	return &config.Config{
		DrydockURL:        drydockURL,
		Token:             "testtoken",
		AgentID:           "test-agent-id",
		AgentName:         "test-agent",
		HeartbeatInterval: 60,
		ReconnectDelay:    1,
		MaxReconnectDelay: 1,
		WelcomeTimeout:    5,
		DDPollInterval:    300,
		DockerSocket:      "/dev/null", // not used in these tests
	}
}

// newTestDockerClient returns a *docker.Client pointing at a non-existent
// socket.  API calls will return errors, which the edge Client handles
// gracefully (e.g. sendHello falls back to dockerVersion="unknown").
func newTestDockerClient(t *testing.T) *docker.Client {
	t.Helper()
	dc, err := docker.NewClient("/dev/null", 1)
	if err != nil {
		t.Fatalf("docker.NewClient: %v", err)
	}
	return dc
}

// newTestClient builds a Client wired to noopEdgeAdapter and a disabled auditor.
// The docker client points at /dev/null — API calls return errors but do not panic.
// The metrics collector uses the real constructor (it handles missing /proc gracefully).
func newTestClient(t *testing.T, cfg *config.Config) *Client {
	t.Helper()
	auditor, _, _ := audit.New("")
	return &Client{
		cfg:          cfg,
		dockerClient: newTestDockerClient(t),
		adapter:      noopEdgeAdapter{},
		auditor:      auditor,
		collector:    metrics.NewCollector("/var/lib/docker", true), // skipDisk=true in tests
		streamSem:    make(chan struct{}, maxStreams),
	}
}

// newTestClientWithWS builds a Client and wires it to the provided WS conn.
func newTestClientWithWS(t *testing.T, cfg *config.Config, conn *websocket.Conn) *Client {
	t.Helper()
	c := newTestClient(t, cfg)
	c.connMu.Lock()
	c.conn = conn
	c.connMu.Unlock()
	return c
}

// drainWS reads all frames from conn in a goroutine, returning them via the
// channel.  The goroutine exits when conn is closed.
func drainWS(conn *websocket.Conn) chan protocol.Envelope {
	ch := make(chan protocol.Envelope, 512)
	go func() {
		defer close(ch)
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var env protocol.Envelope
			if err := json.Unmarshal(msg, &env); err != nil {
				continue
			}
			select {
			case ch <- env:
			default:
			}
		}
	}()
	return ch
}

// ---- hello/welcome handshake tests -----------------------------------------

// TestHello_HappyPath verifies that connect() sends a valid hello frame and
// receives a welcome, leaving the pump running until the context is cancelled.
func TestHello_HappyPath(t *testing.T) {
	t.Parallel()

	md := newMockDrydock(t)
	cfg := newTestConfig(md.wsURL())
	c := newTestClient(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- c.connect(ctx)
	}()

	// Expect hello.
	helloEnv := md.waitFrame(t, protocol.TypeHello)

	var hello protocol.HelloMessage
	if err := json.Unmarshal(helloEnv.Data, &hello); err != nil {
		t.Fatalf("unmarshal hello: %v", err)
	}
	if hello.Protocol != protocol.ProtocolString {
		t.Errorf("protocol = %q, want %q", hello.Protocol, protocol.ProtocolString)
	}
	if hello.AgentID != cfg.AgentID {
		t.Errorf("agentId = %q, want %q", hello.AgentID, cfg.AgentID)
	}
	if len(hello.Capabilities) == 0 {
		t.Error("capabilities must not be empty")
	}

	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("connect did not return after context cancellation")
	}
}

// TestHello_Rejected verifies that connect() returns an error when the server
// responds with an error frame instead of welcome.
func TestHello_Rejected(t *testing.T) {
	t.Parallel()

	md := newMockDrydock(t)
	md.rejectHello = true

	cfg := newTestConfig(md.wsURL())
	c := newTestClient(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := c.connect(ctx)
	if err == nil {
		t.Fatal("expected connect to return an error for rejected hello, got nil")
	}
	t.Logf("connect error (expected): %v", err)
}

// ---- request fan-out / concurrency cap tests --------------------------------

// TestMaxStreams_ConcurrencyCap verifies that when streamSem is saturated,
// additional request frames are immediately rejected with an error frame rather
// than blocking readPump.
func TestMaxStreams_ConcurrencyCap(t *testing.T) {
	t.Parallel()

	md := newMockDrydock(t)
	cfg := newTestConfig(md.wsURL())
	c := newTestClient(t, cfg)

	// Fill the semaphore completely so every incoming request is over the cap.
	for i := 0; i < maxStreams; i++ {
		c.streamSem <- struct{}{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { c.connect(ctx) }()

	md.waitFrame(t, protocol.TypeHello)
	time.Sleep(50 * time.Millisecond)

	reqMsg := protocol.RequestMessage{
		RequestID: "req-cap-001",
		Method:    "GET",
		Path:      "/v1.41/containers/json",
	}
	raw, _ := json.Marshal(reqMsg)
	if err := md.send(protocol.Envelope{Type: protocol.TypeRequest, Data: json.RawMessage(raw)}); err != nil {
		t.Fatalf("send request frame: %v", err)
	}

	// Must receive an error frame promptly (no blocking).
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case env := <-md.frameC:
			if env.Type == protocol.TypeError {
				var errMsg protocol.ErrorMessage
				if err := json.Unmarshal(env.Data, &errMsg); err == nil {
					t.Logf("got expected error frame: %q", errMsg.Message)
					return
				}
			}
		case <-timer.C:
			t.Fatal("timed out waiting for error frame from saturated semaphore")
		}
	}
}

// TestMaxExecSessions_ConcurrencyCap verifies that StartExec rejects sessions
// beyond maxExecSessions with an exec_end frame.
func TestMaxExecSessions_ConcurrencyCap(t *testing.T) {
	t.Parallel()

	// Build a WS pair for the client to write to.
	cliWS, srvWS := makePipeWS(t)

	auditor, _, _ := audit.New("")
	c := &Client{
		cfg:       newTestConfig("ws://unused"),
		adapter:   noopEdgeAdapter{},
		auditor:   auditor,
		streamSem: make(chan struct{}, maxStreams),
	}
	c.conn = cliWS

	// Collect frames from the server side.
	srvFrames := drainWS(srvWS)

	// Inject synthetic sessions to fill the cap using net.Pipe conns.
	pipes := make([]net.Conn, 0, maxExecSessions)
	for i := 0; i < maxExecSessions; i++ {
		a, b := net.Pipe()
		pipes = append(pipes, a, b)
		sess := &ExecSession{
			execID: fmt.Sprintf("fake-exec-%d", i),
			conn:   a,
			client: c,
			done:   make(chan struct{}),
			inputQ: make(chan []byte, execInputQueueDepth),
		}
		c.execSessions.Store(sess.execID, sess)
	}
	t.Cleanup(func() {
		for _, p := range pipes {
			p.Close()
		}
	})

	// Attempt to start one more session — must be rejected with exec_end.
	msg := protocol.ExecStartMessage{
		ExecID:      "overflow-exec",
		ContainerID: "c1",
		Cmd:         []string{"sh"},
	}
	c.StartExec(context.Background(), msg)

	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case env, ok := <-srvFrames:
			if !ok {
				t.Fatal("server WS closed before exec_end frame")
			}
			if env.Type == protocol.TypeExecEnd {
				var end protocol.ExecEndMessage
				if err := json.Unmarshal(env.Data, &end); err == nil && end.ExecID == "overflow-exec" {
					t.Logf("received exec_end rejection for overflow session: %q", end.Reason)
					return
				}
			}
		case <-deadline.C:
			t.Fatal("timed out waiting for exec_end rejection frame")
		}
	}
}

// ---- exec session lifecycle test -------------------------------------------

// TestExecSession_Lifecycle exercises readLoop end-to-end using net.Pipe:
// simulated Docker stdout → exec_output frames → EOF → exec_end frame.
func TestExecSession_Lifecycle(t *testing.T) {
	t.Parallel()

	cliWS, srvWS := makePipeWS(t)
	srvFrames := drainWS(srvWS)

	auditor, _, _ := audit.New("")
	c := &Client{
		cfg:       newTestConfig("ws://unused"),
		adapter:   noopEdgeAdapter{},
		auditor:   auditor,
		streamSem: make(chan struct{}, maxStreams),
	}
	c.conn = cliWS

	// net.Pipe simulates the Docker hijacked exec connection.
	execDockerSide, execClientSide := net.Pipe()
	t.Cleanup(func() { execDockerSide.Close(); execClientSide.Close() })

	session := &ExecSession{
		execID: "lifecycle-exec-1",
		conn:   execClientSide,
		client: c,
		done:   make(chan struct{}),
		inputQ: make(chan []byte, execInputQueueDepth),
	}
	c.execSessions.Store(session.execID, session)

	go session.drainInput()
	go session.readLoop()

	// Simulate Docker writing stdout and then closing (EOF).
	go func() {
		_, _ = execDockerSide.Write([]byte("hello from docker\n"))
		execDockerSide.Close()
	}()

	var gotOutput, gotEnd bool
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for !gotOutput || !gotEnd {
		select {
		case env, ok := <-srvFrames:
			if !ok {
				t.Fatal("server WS closed unexpectedly")
			}
			switch env.Type {
			case protocol.TypeExecOutput:
				gotOutput = true
			case protocol.TypeExecEnd:
				gotEnd = true
			}
		case <-deadline.C:
			t.Fatalf("timed out: gotOutput=%v gotEnd=%v", gotOutput, gotEnd)
		}
	}
}

// ---- #30 regression: ordered exec input queue tests ------------------------

// TestExecInputQueue_OrderPreserved verifies that the drainInput goroutine
// serialises concurrent HandleInput calls: every byte arrives at the Docker
// side intact with no interleaving.
//
// NOTE: This test calls HandleInput directly from N goroutines and does NOT
// run a live readPump.  It confirms that drainInput is the sole writer and
// produces no byte-level interleaving, but it cannot detect the old
// readPump-blocking bug (the bug stalled readPump with time.Sleep, which this
// test bypasses).  TestPing_UnderExecLoad is the primary regression test for
// the readPump-blocking anti-pattern fixed in #30 — see that test's doc
// comment.
func TestExecInputQueue_OrderPreserved(t *testing.T) {
	t.Parallel()

	const N = 32 // number of concurrent senders

	// net.Pipe simulates the Docker exec hijacked connection.
	execClientConn, execDockerConn := net.Pipe()
	t.Cleanup(func() {
		execClientConn.Close()
		execDockerConn.Close()
	})

	// Build a Client with a WS pair (used only for error-frame writes on drop).
	cliWS, srvWS := makePipeWS(t)
	_ = srvWS // drain goroutine not needed for this test
	go func() {
		for {
			if _, _, err := srvWS.ReadMessage(); err != nil {
				return
			}
		}
	}()

	auditor, _, _ := audit.New("")
	c := &Client{
		cfg:       newTestConfig("ws://unused"),
		adapter:   noopEdgeAdapter{},
		auditor:   auditor,
		streamSem: make(chan struct{}, maxStreams),
	}
	c.conn = cliWS

	session := &ExecSession{
		execID: "order-test",
		conn:   execClientConn,
		client: c,
		done:   make(chan struct{}),
		inputQ: make(chan []byte, execInputQueueDepth),
	}
	c.execSessions.Store(session.execID, session)
	go session.drainInput()

	// Collect all bytes that arrive at the Docker side.
	received := make(chan byte, N)
	go func() {
		defer close(received)
		buf := make([]byte, 1)
		for {
			n, err := execDockerConn.Read(buf)
			if n > 0 {
				received <- buf[0]
			}
			if err != nil {
				return
			}
		}
	}()

	// N goroutines each call HandleInput with their index as a single byte.
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			data := base64.StdEncoding.EncodeToString([]byte{byte(i)})
			c.HandleInput(protocol.ExecInputMessage{
				ExecID: session.execID,
				Data:   data,
			})
		}()
	}
	wg.Wait()

	// Collect all N bytes from the Docker side.
	collected := make([]byte, 0, N)
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for len(collected) < N {
		select {
		case b, ok := <-received:
			if !ok {
				goto done
			}
			collected = append(collected, b)
		case <-deadline.C:
			t.Fatalf("timed out after %d/%d bytes received", len(collected), N)
		}
	}
done:

	if len(collected) != N {
		t.Fatalf("received %d bytes, want %d", len(collected), N)
	}

	// Each of the N values 0..N-1 must appear exactly once: no frame was
	// dropped (queue has headroom for N=32 ≤ execInputQueueDepth=64), and no
	// two frames' bytes interleaved (each message is one byte, atomically
	// written by the single drain goroutine).
	seen := make(map[byte]int)
	for _, b := range collected {
		seen[b]++
	}
	for i := 0; i < N; i++ {
		if seen[byte(i)] != 1 {
			t.Errorf("byte value %d seen %d times (want exactly 1)", i, seen[byte(i)])
		}
	}
	t.Logf("all %d input frames delivered intact via ordered queue", N)
}

// TestExecInputQueue_NoBlockOnFull verifies that HandleInput returns
// immediately even when the session's queue is completely full.
// This is the core readPump non-blocking guarantee from #30.
func TestExecInputQueue_NoBlockOnFull(t *testing.T) {
	t.Parallel()

	execA, execB := net.Pipe()
	t.Cleanup(func() { execA.Close(); execB.Close() })

	cliWS, srvWS := makePipeWS(t)
	go func() {
		for {
			if _, _, err := srvWS.ReadMessage(); err != nil {
				return
			}
		}
	}()

	auditor, _, _ := audit.New("")
	c := &Client{
		cfg:       newTestConfig("ws://unused"),
		adapter:   noopEdgeAdapter{},
		auditor:   auditor,
		streamSem: make(chan struct{}, maxStreams),
	}
	c.conn = cliWS

	// Create session but do NOT start drainInput — queue fills up immediately.
	session := &ExecSession{
		execID: "noblock-test",
		conn:   execA,
		client: c,
		done:   make(chan struct{}),
		inputQ: make(chan []byte, execInputQueueDepth),
	}
	c.execSessions.Store(session.execID, session)

	// Fill the queue to capacity.
	for i := 0; i < execInputQueueDepth; i++ {
		data := base64.StdEncoding.EncodeToString([]byte{byte(i % 256)})
		c.HandleInput(protocol.ExecInputMessage{ExecID: session.execID, Data: data})
	}

	// The next call must return immediately (not block).
	overflowData := base64.StdEncoding.EncodeToString([]byte("overflow"))
	returned := make(chan struct{})
	go func() {
		c.HandleInput(protocol.ExecInputMessage{ExecID: session.execID, Data: overflowData})
		close(returned)
	}()

	select {
	case <-returned:
		// Good — HandleInput returned without blocking.
	case <-time.After(200 * time.Millisecond):
		t.Fatal("HandleInput blocked on a full queue (violates readPump non-blocking requirement from #30)")
	}
}

// TestExecInputQueue_DrainOnClose verifies that drainInput exits promptly when
// the session is closed — no goroutine leak.
func TestExecInputQueue_DrainOnClose(t *testing.T) {
	t.Parallel()

	execA, execB := net.Pipe()
	t.Cleanup(func() { execA.Close(); execB.Close() })

	auditor, _, _ := audit.New("")
	c := &Client{
		cfg:       newTestConfig("ws://unused"),
		adapter:   noopEdgeAdapter{},
		auditor:   auditor,
		streamSem: make(chan struct{}, maxStreams),
	}

	session := &ExecSession{
		execID: "drain-close-test",
		conn:   execA,
		client: c,
		done:   make(chan struct{}),
		inputQ: make(chan []byte, execInputQueueDepth),
	}
	c.execSessions.Store(session.execID, session)

	drainDone := make(chan struct{})
	go func() {
		session.drainInput()
		close(drainDone)
	}()

	session.Close()

	select {
	case <-drainDone:
		// Good — no goroutine leak.
	case <-time.After(time.Second):
		t.Fatal("drainInput goroutine did not exit after session.Close()")
	}
}

// TestExecClose_Idempotent verifies that calling session.Close() multiple times
// does not panic or deadlock.
func TestExecClose_Idempotent(t *testing.T) {
	t.Parallel()

	execA, execB := net.Pipe()
	t.Cleanup(func() { execA.Close(); execB.Close() })

	auditor, _, _ := audit.New("")
	c := &Client{
		cfg:       newTestConfig("ws://unused"),
		adapter:   noopEdgeAdapter{},
		auditor:   auditor,
		streamSem: make(chan struct{}, maxStreams),
	}

	session := &ExecSession{
		execID: "idempotent-close",
		conn:   execA,
		client: c,
		done:   make(chan struct{}),
		inputQ: make(chan []byte, execInputQueueDepth),
	}
	c.execSessions.Store(session.execID, session)

	// Must not panic or deadlock.
	session.Close()
	session.Close()
	session.Close()
}

// ---- ping round-trip tests -------------------------------------------------

// TestPing_RoundTrip verifies that readPump replies to a TypePing frame with
// a TypePong frame carrying the same timestamp.
func TestPing_RoundTrip(t *testing.T) {
	t.Parallel()

	md := newMockDrydock(t)
	cfg := newTestConfig(md.wsURL())
	c := newTestClient(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { c.connect(ctx) }()

	md.waitFrame(t, protocol.TypeHello)
	time.Sleep(50 * time.Millisecond)

	ts := time.Now().UnixMilli()
	if err := md.sendTyped(protocol.TypePing, protocol.PingMessage{Timestamp: ts}); err != nil {
		t.Fatalf("send ping: %v", err)
	}

	pongEnv := md.waitFrame(t, protocol.TypePong)
	var pong protocol.PongMessage
	if err := json.Unmarshal(pongEnv.Data, &pong); err != nil {
		t.Fatalf("unmarshal pong: %v", err)
	}
	if pong.Timestamp != ts {
		t.Errorf("pong.Timestamp = %d, want %d", pong.Timestamp, ts)
	}
}

// TestPing_UnderExecLoad is the PRIMARY regression test for the readPump-blocking
// anti-pattern fixed in #30.  A ping round-trip must complete within 500 ms
// while a burst of exec_input frames is being delivered through the live
// readPump goroutine (connect() is running in the background).
//
// Before the fix (blocking retry loop called synchronously on readPump),
// readPump was stalled for up to 500 ms per exec_input burst, causing this
// test to time out.  With the ordered queue, HandleInput is non-blocking and
// readPump processes the ping immediately.
//
// This is the only test in this file that exercises the full readPump dispatch
// path; TestExecInputQueue_OrderPreserved calls HandleInput directly and cannot
// detect readPump stalls.
func TestPing_UnderExecLoad(t *testing.T) {
	t.Parallel()

	md := newMockDrydock(t)
	cfg := newTestConfig(md.wsURL())
	c := newTestClient(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go func() { c.connect(ctx) }()

	md.waitFrame(t, protocol.TypeHello)
	time.Sleep(50 * time.Millisecond)

	// Register a fake exec session that can absorb the burst.
	execA, execB := net.Pipe()
	t.Cleanup(func() { execA.Close(); execB.Close() })
	go func() { io.Copy(io.Discard, execB) }() //nolint:errcheck

	execID := "ping-load-exec"
	session := &ExecSession{
		execID: execID,
		conn:   execA,
		client: c,
		done:   make(chan struct{}),
		inputQ: make(chan []byte, execInputQueueDepth),
	}
	c.execSessions.Store(execID, session)
	go session.drainInput()

	// Burst exec_input frames from the server.
	const burst = 200
	go func() {
		for i := 0; i < burst; i++ {
			data := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("chunk-%04d\n", i)))
			inputMsg := protocol.ExecInputMessage{ExecID: execID, Data: data}
			raw, _ := json.Marshal(inputMsg)
			_ = md.send(protocol.Envelope{Type: protocol.TypeExecInput, Data: json.RawMessage(raw)})
		}
	}()

	// Send a ping mid-burst; it must arrive within 500 ms.
	time.Sleep(5 * time.Millisecond) // let burst start
	ts := time.Now().UnixMilli()
	if err := md.sendTyped(protocol.TypePing, protocol.PingMessage{Timestamp: ts}); err != nil {
		t.Fatalf("send ping: %v", err)
	}

	pongTimer := time.NewTimer(500 * time.Millisecond)
	defer pongTimer.Stop()
	for {
		select {
		case env := <-md.frameC:
			if env.Type == protocol.TypePong {
				var pong protocol.PongMessage
				if err := json.Unmarshal(env.Data, &pong); err == nil && pong.Timestamp == ts {
					elapsed := time.Now().UnixMilli() - ts
					t.Logf("pong in %d ms under exec burst of %d frames — readPump non-blocking OK", elapsed, burst)
					return
				}
			}
		case <-pongTimer.C:
			t.Fatal("ping round-trip exceeded 500 ms during exec_input burst: readPump is blocking")
		}
	}
}

// ---- HandleResize non-blocking regression test --------------------------------

// TestHandleResize_NonBlocking is the primary regression test for the
// readPump-blocking anti-pattern in HandleResize (mirror of #30 for resize).
//
// HandleResize used to run a blocking retry loop (up to 10×50 ms = 500 ms) on
// readPump.  The fix dispatches it with `go`, so readPump returns immediately.
// This test verifies that a ping round-trip completes within 500 ms while
// HandleResize is invoked for an unknown exec session (which previously caused
// the full retry wait).
func TestHandleResize_NonBlocking(t *testing.T) {
	t.Parallel()

	md := newMockDrydock(t)
	cfg := newTestConfig(md.wsURL())
	c := newTestClient(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go func() { c.connect(ctx) }()

	md.waitFrame(t, protocol.TypeHello)
	time.Sleep(50 * time.Millisecond)

	// Send a burst of resize frames for a non-existent session.
	// Under the old code each would block readPump for up to 500 ms.
	const bursts = 5
	go func() {
		for i := 0; i < bursts; i++ {
			resizeMsg := protocol.ExecResizeMessage{ExecID: "no-such-exec", Cols: 80, Rows: 24}
			raw, _ := json.Marshal(resizeMsg)
			_ = md.send(protocol.Envelope{Type: protocol.TypeExecResize, Data: json.RawMessage(raw)})
		}
	}()

	time.Sleep(5 * time.Millisecond) // let bursts start

	ts := time.Now().UnixMilli()
	if err := md.sendTyped(protocol.TypePing, protocol.PingMessage{Timestamp: ts}); err != nil {
		t.Fatalf("send ping: %v", err)
	}

	pongTimer := time.NewTimer(500 * time.Millisecond)
	defer pongTimer.Stop()
	for {
		select {
		case env := <-md.frameC:
			if env.Type == protocol.TypePong {
				var pong protocol.PongMessage
				if err := json.Unmarshal(env.Data, &pong); err == nil && pong.Timestamp == ts {
					elapsed := time.Now().UnixMilli() - ts
					t.Logf("pong in %d ms during HandleResize burst — readPump non-blocking OK", elapsed)
					return
				}
			}
		case <-pongTimer.C:
			t.Fatal("ping round-trip exceeded 500 ms during exec_resize burst: HandleResize is blocking readPump")
		}
	}
}

// ---- exec session teardown on disconnect test --------------------------------

// TestExecSessions_TornDownOnDisconnect verifies that all active exec sessions
// are closed when the WebSocket tunnel drops (connect() returns).  Before the
// fix, sessions' readLoop and drainInput goroutines were orphaned on every
// unclean disconnect, leaking goroutines and Docker exec processes.
func TestExecSessions_TornDownOnDisconnect(t *testing.T) {
	t.Parallel()

	md := newMockDrydock(t)
	cfg := newTestConfig(md.wsURL())
	c := newTestClient(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	connectDone := make(chan error, 1)
	go func() {
		connectDone <- c.connect(ctx)
	}()

	md.waitFrame(t, protocol.TypeHello)
	time.Sleep(50 * time.Millisecond)

	// Inject two fake exec sessions with net.Pipe conns.
	const numSessions = 2
	sessionDones := make([]chan struct{}, numSessions)
	for i := 0; i < numSessions; i++ {
		execClientConn, execDockerConn := net.Pipe()
		t.Cleanup(func() { execClientConn.Close(); execDockerConn.Close() })

		done := make(chan struct{})
		sessionDones[i] = done

		sess := &ExecSession{
			execID: fmt.Sprintf("teardown-exec-%d", i),
			conn:   execClientConn,
			client: c,
			done:   make(chan struct{}),
			inputQ: make(chan []byte, execInputQueueDepth),
		}
		c.execSessions.Store(sess.execID, sess)
		go sess.drainInput()

		// Goroutine that signals when the exec conn is closed (i.e. session was torn down).
		go func(conn net.Conn, done chan struct{}) {
			buf := make([]byte, 1)
			conn.Read(buf) // blocks until conn is closed
			close(done)
		}(execDockerConn, done)
	}

	// Drop the server connection to trigger a disconnect.
	md.mu.Lock()
	if md.conn != nil {
		md.conn.Close()
	}
	md.mu.Unlock()

	// connect() must return.
	select {
	case <-connectDone:
	case <-time.After(3 * time.Second):
		t.Fatal("connect() did not return after server-side disconnect")
	}

	// Every session's Docker conn must have been closed by the teardown loop.
	for i, done := range sessionDones {
		select {
		case <-done:
			// Good — session i was torn down.
		case <-time.After(time.Second):
			t.Errorf("exec session %d was not closed after tunnel disconnect (goroutine leak)", i)
		}
	}
}

// ---- drainInput write-error exit test -----------------------------------------

// TestDrainInput_ExitsOnWriteErrors verifies that drainInput calls Close() and
// exits after maxWriteErrors consecutive conn.Write failures, guarding against
// the asymmetric half-close scenario where conn.Read never errors.
func TestDrainInput_ExitsOnWriteErrors(t *testing.T) {
	t.Parallel()

	// Use a net.Pipe but close only the write-side (execClientConn) from the
	// test's perspective: we call execClientConn.Close() so writes fail but we
	// control when the "read" side sees an EOF.
	execClientConn, execDockerConn := net.Pipe()
	t.Cleanup(func() { execDockerConn.Close() })

	auditor, _, _ := audit.New("")
	c := &Client{
		cfg:       newTestConfig("ws://unused"),
		adapter:   noopEdgeAdapter{},
		auditor:   auditor,
		streamSem: make(chan struct{}, maxStreams),
	}

	session := &ExecSession{
		execID: "write-err-test",
		conn:   execClientConn,
		client: c,
		done:   make(chan struct{}),
		inputQ: make(chan []byte, execInputQueueDepth),
	}
	c.execSessions.Store(session.execID, session)

	drainExited := make(chan struct{})
	go func() {
		session.drainInput()
		close(drainExited)
	}()

	// Close the conn so all writes will fail.
	execClientConn.Close()

	// Send enough frames to trigger the consecutive-error threshold (maxWriteErrors = 3).
	for i := 0; i < 5; i++ {
		session.inputQ <- []byte("ping")
	}

	select {
	case <-drainExited:
		// drainInput exited after consecutive write errors — correct.
	case <-time.After(2 * time.Second):
		t.Fatal("drainInput did not exit after consecutive write errors (potential goroutine leak)")
	}

	// session.done must be closed (Close() was called).
	select {
	case <-session.done:
		// Good.
	default:
		t.Error("session.done is not closed: session.Close() was not called by drainInput")
	}
}

// ---- compile-time import check ---------------------------------------------

// Ensure the docker import is used (docker.Client is referenced in production
// code; importing it here keeps the test binary's build graph correct).
var _ *docker.Client

// Ensure atomic is referenced to confirm the import is live.
var _ atomic.Bool
