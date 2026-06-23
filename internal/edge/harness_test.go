package edge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/config"
	"github.com/codeswhat/portwing/internal/protocol"
)

// readTimeout bounds every controller-side read so a missing reply fails the
// test fast instead of hanging the suite.
const readTimeout = 2 * time.Second

// newWSPair stands up an in-memory WebSocket and returns both ends. agent is
// the connection the edge Client writes to (assigned to Client.conn); ctrl is
// the controller side the test drives — it sends frames to the agent and reads
// back whatever the agent emits. Both ends are closed at test cleanup.
func newWSPair(t *testing.T) (agent, ctrl *websocket.Conn) {
	t.Helper()

	upgrader := websocket.Upgrader{}
	srvCh := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		srvCh <- c
	}))
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	agent, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial controller: %v", err)
	}

	select {
	case ctrl = <-srvCh:
	case <-time.After(readTimeout):
		t.Fatal("controller never upgraded the connection")
	}

	t.Cleanup(func() {
		_ = agent.Close()
		_ = ctrl.Close()
	})
	return agent, ctrl
}

// newTestClient builds a Client wired to an in-memory controller. The returned
// ctrl conn is the controller side. The dockerClient and adapter are left nil
// on purpose — every path this harness exercises stops before touching them;
// the Docker-backed exec paths (CreateExec/StartExec/ResizeExec success) belong
// to an integration tier against a real daemon, not this unit harness.
func newTestClient(t *testing.T) (*Client, *websocket.Conn) {
	t.Helper()

	agent, ctrl := newWSPair(t)

	// "" disables the audit logger (zero overhead beyond a nil check), so the
	// readPump exec_start audit call is a safe no-op.
	auditor, closeAudit, err := audit.New("", 0)
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}
	t.Cleanup(closeAudit)

	c := &Client{
		cfg:       &config.Config{},
		auditor:   auditor,
		streamSem: make(chan struct{}, maxStreams),
		conn:      agent,
	}
	return c, ctrl
}

// expectEnvelope reads one envelope from the controller, failing if none
// arrives within readTimeout.
func expectEnvelope(t *testing.T, ctrl *websocket.Conn) protocol.Envelope {
	t.Helper()

	if err := ctrl.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, raw, err := ctrl.ReadMessage()
	if err != nil {
		t.Fatalf("read from agent: %v", err)
	}
	var env protocol.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal envelope %q: %v", raw, err)
	}
	return env
}

// expectType reads one envelope and asserts its type, returning the inner data
// for further decoding.
func expectType(t *testing.T, ctrl *websocket.Conn, want string) json.RawMessage {
	t.Helper()

	env := expectEnvelope(t, ctrl)
	if env.Type != want {
		t.Fatalf("envelope type = %q, want %q (data=%s)", env.Type, want, env.Data)
	}
	return env.Data
}

// decodeData unmarshals an envelope's data payload into v.
func decodeData(t *testing.T, data json.RawMessage, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("decode %T: %v", v, err)
	}
}

// newExecSession constructs a minimal registered ExecSession over conn, with no
// input writer running. Use for tests that drive readLoop/Close/EndExec
// directly and don't exercise the input queue.
func newExecSession(c *Client, execID string, conn net.Conn) *ExecSession {
	s := &ExecSession{
		execID:    execID,
		conn:      conn,
		client:    c,
		connReady: make(chan struct{}),
		inbox:     make(chan execItem, execInputQueue),
		done:      make(chan struct{}),
	}
	c.execSessions.Store(execID, s)
	return s
}

// newReadySession registers a fully live exec session: conn wired, connReady
// closed, and the inputWriter goroutine running. Use for tests that send input
// through HandleInput and expect it written to conn (the writer drains
// asynchronously, so assert with waitFor).
func newReadySession(c *Client, execID string, conn net.Conn) *ExecSession {
	s := newExecSession(c, execID, conn)
	close(s.connReady)
	go s.inputWriter(context.Background())
	return s
}

// waitFor polls cond until it returns true or readTimeout elapses, failing the
// test on timeout. Use to await asynchronous effects (e.g. the input writer
// draining the queue to conn).
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(readTimeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// fakeConn is a scripted net.Conn. Read drains the queued chunks then returns
// readErr (io.EOF by default). Write records bytes unless writeErr is set, in
// which case it always fails — used to drive the write-retry path.
//
// Set blockRead to a channel that Read will block on (after draining any
// scripted chunks). When the channel is closed, Read returns io.EOF. This lets
// tests that care only about writes keep the readLoop alive so it doesn't race
// with the inputWriter via the done channel.
type fakeConn struct {
	mu        sync.Mutex
	reads     [][]byte
	readErr   error
	writeErr  error
	writes    bytes.Buffer
	closed    bool
	blockRead chan struct{} // when non-nil, Read blocks until this is closed
}

func (c *fakeConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	if len(c.reads) > 0 {
		chunk := c.reads[0]
		c.reads = c.reads[1:]
		n := copy(p, chunk)
		// If the chunk didn't fit, requeue the remainder.
		if n < len(chunk) {
			c.reads = append([][]byte{chunk[n:]}, c.reads...)
		}
		c.mu.Unlock()
		return n, nil
	}
	block := c.blockRead
	readErr := c.readErr
	c.mu.Unlock()

	if block != nil {
		<-block
		return 0, io.EOF
	}
	if readErr != nil {
		return 0, readErr
	}
	return 0, io.EOF
}

func (c *fakeConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	return c.writes.Write(p)
}

func (c *fakeConn) written() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.writes.Bytes()...)
}

func (c *fakeConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

// isClosed reports whether Close has been called. Mutex-safe.
func (c *fakeConn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *fakeConn) LocalAddr() net.Addr                { return fakeAddr{} }
func (c *fakeConn) RemoteAddr() net.Addr               { return fakeAddr{} }
func (c *fakeConn) SetDeadline(_ time.Time) error      { return nil }
func (c *fakeConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *fakeConn) SetWriteDeadline(_ time.Time) error { return nil }

type fakeAddr struct{}

func (fakeAddr) Network() string { return "fake" }
func (fakeAddr) String() string  { return "fake" }

// fakeDocker is a scripted dockerAPI: it records calls and returns canned
// values, standing in for *docker.Client so the exec sessions and request
// fan-out can be driven without a live Docker daemon.
type fakeDocker struct {
	mu sync.Mutex

	version string

	createExecID  string
	createExecErr error
	createCalls   []createExecCall

	startConn    net.Conn
	startExecErr error
	startCalls   []string // exec ids passed to StartExec
	// startGate, when non-nil, blocks StartExec until the channel is closed or
	// receives — lets a test hold the exec bring-up open while it sends input
	// that must be buffered in order rather than dropped.
	startGate chan struct{}

	// resizeFailFirst makes the first N ResizeExec calls fail before succeeding,
	// to exercise the retry path.
	resizeFailFirst int
	resizeErr       error
	resizeAttempts  int
	resizeCalls     []resizeCall

	doResp     *http.Response
	doErr      error
	streamResp *http.Response
	streamErr  error
	doCalls    []doCall
}

type createExecCall struct {
	containerID string
	cmd         []string
	user        string
}

type resizeCall struct {
	execID string
	cols   int
	rows   int
}

type doCall struct {
	method string
	path   string
	stream bool
}

func (f *fakeDocker) GetVersion(context.Context) (string, error) {
	if f.version == "" {
		return "test-docker", nil
	}
	return f.version, nil
}

func (f *fakeDocker) CreateExec(_ context.Context, containerID string, cmd []string, user string, _ bool) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls = append(f.createCalls, createExecCall{containerID, cmd, user})
	return f.createExecID, f.createExecErr
}

func (f *fakeDocker) StartExec(_ context.Context, execID string, _ bool) (net.Conn, error) {
	f.mu.Lock()
	f.startCalls = append(f.startCalls, execID)
	gate := f.startGate
	startErr := f.startExecErr
	conn := f.startConn
	f.mu.Unlock()

	if gate != nil {
		<-gate
	}
	if startErr != nil {
		return nil, startErr
	}
	return conn, nil
}

func (f *fakeDocker) ResizeExec(_ context.Context, execID string, cols, rows int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resizeAttempts++
	f.resizeCalls = append(f.resizeCalls, resizeCall{execID, cols, rows})
	if f.resizeAttempts <= f.resizeFailFirst {
		return errors.New("resize busy")
	}
	return f.resizeErr
}

func (f *fakeDocker) Do(_ context.Context, method, path string, _ io.Reader) (*http.Response, error) {
	f.mu.Lock()
	f.doCalls = append(f.doCalls, doCall{method, path, false})
	f.mu.Unlock()
	return f.doResp, f.doErr
}

func (f *fakeDocker) DoStream(_ context.Context, method, path string, _ io.Reader) (*http.Response, error) {
	f.mu.Lock()
	f.doCalls = append(f.doCalls, doCall{method, path, true})
	f.mu.Unlock()
	return f.streamResp, f.streamErr
}

func (f *fakeDocker) resizeCallList() []resizeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]resizeCall(nil), f.resizeCalls...)
}

func (f *fakeDocker) createCallList() []createExecCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]createExecCall(nil), f.createCalls...)
}
