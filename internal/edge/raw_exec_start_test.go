package edge

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/docker"
	"github.com/codeswhat/portwing/internal/protocol"
)

func execRecords(logger *audit.Logger) []audit.Record {
	var out []audit.Record
	for _, r := range logger.Records(0) {
		if r.Event == audit.EventExecStart {
			out = append(out, r)
		}
	}
	return out
}

func TestRawExecStartClassification(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		method, path, id, route string
		ok                      bool
	}{
		{http.MethodPost, "/exec/abc/start", "abc", "/exec/abc/start", true},
		{http.MethodPost, "/v1.47/exec/abc/start", "abc", "/v1.47/exec/abc/start", true},
		{http.MethodPost, "/v1.47/exec/abc/start?x=1", "abc", "/v1.47/exec/abc/start", true},
		{http.MethodPost, "/v1.47/exec/a%2Fb/start", "a/b", "/v1.47/exec/a/b/start", true},
		{http.MethodGet, "/v1.47/exec/abc/start", "", "", false},
		{http.MethodPost, "/v1.47/exec/abc/json", "", "", false},
		{http.MethodPost, "/containers/create", "", "", false},
	} {
		id, route, ok := rawExecStart(tc.method, tc.path)
		if id != tc.id || route != tc.route || ok != tc.ok {
			t.Errorf("%s %s = (%q, %q, %v), want (%q, %q, %v)", tc.method, tc.path, id, route, ok, tc.id, tc.route, tc.ok)
		}
	}
}

// realDockerDaemon serves a fake daemon on a Unix socket behind a real
// docker.Client, so the request path goes through the client's URL join.
// Every request other than the version negotiation is reported on paths.
func realDockerDaemon(t *testing.T) (*docker.Client, <-chan string) {
	t.Helper()
	return realDockerDaemonHolding(t, nil)
}

// realDockerDaemonHolding is realDockerDaemon whose non-version responses stay
// open after the headers are flushed until hold is closed (or the request is
// cancelled), so a test can keep a stream in flight. A nil hold answers at once.
func realDockerDaemonHolding(t *testing.T, hold <-chan struct{}) (*docker.Client, <-chan string) {
	t.Helper()
	return realDockerDaemonObserving(t, hold, nil)
}

// realDockerDaemonObserving is realDockerDaemonHolding that also calls onRequest,
// when non-nil, on every non-version request before it answers.
func realDockerDaemonObserving(t *testing.T, hold <-chan struct{}, onRequest func()) (*docker.Client, <-chan string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "lk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "docker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	paths := make(chan string, 16)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			_, _ = w.Write([]byte(`{"ApiVersion":"1.44"}`))
			return
		}
		if onRequest != nil {
			onRequest()
		}
		paths <- r.URL.Path
		w.WriteHeader(http.StatusOK)
		if hold != nil {
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			select {
			case <-hold:
			case <-r.Context().Done():
			}
		}
	}))
	_ = srv.Listener.Close()
	srv.Listener = listener
	srv.Start()
	t.Cleanup(srv.Close)
	dc, err := docker.NewClient(socket, 10)
	if err != nil {
		t.Fatal(err)
	}
	return dc, paths
}

func TestRawDetachedExecStartAuditedAndSlotReleased(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	logger, closeAudit, err := audit.New("", 20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeAudit)
	c.auditor = logger
	dc, paths := realDockerDaemon(t)
	c.dockerClient = dc

	c.handleRequest(context.Background(), protocol.RequestMessage{
		RequestID: "raw1", Method: http.MethodPost, Path: "/exec/abc/start",
		Body: []byte(`{"Detach":true}`),
	})
	var resp protocol.ResponseMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeResponse), &resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := <-paths; got != "/v1.44/exec/abc/start" {
		t.Fatalf("daemon path = %q", got)
	}

	records := execRecords(logger)
	if len(records) != 1 || records[0].ExecID != "abc" || records[0].Container != "/exec/abc/start" || records[0].Outcome != audit.OutcomeAllowed {
		t.Fatalf("exec records = %+v", records)
	}
	c.execAdmissionMu.Lock()
	held := c.rawExecStarts
	c.execAdmissionMu.Unlock()
	if held != 0 {
		t.Fatalf("rawExecStarts = %d after response, want 0", held)
	}
}

// A path with no leading slash is glued to the API version prefix, so these
// would reach the daemon as /v1.44.0/exec/abc/start. They must be refused
// before any network call, with an error for the request ID.
func TestRawSlashlessExecStartPathsNeverReachDaemon(t *testing.T) {
	t.Parallel()

	for _, path := range []string{".0/exec/abc/start", "./exec/abc/start", "%2E0/exec/abc/start"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			c, ctrl := newTestClient(t)
			dc, paths := realDockerDaemon(t)
			c.dockerClient = dc

			c.handleRequest(context.Background(), protocol.RequestMessage{
				RequestID: "slashless", Method: http.MethodPost, Path: path,
			})
			var e protocol.ErrorMessage
			decodeData(t, expectType(t, ctrl, protocol.TypeError), &e)
			if e.RequestID != "slashless" || !strings.Contains(e.Message, "must begin with") {
				t.Fatalf("error = %+v", e)
			}
			select {
			case got := <-paths:
				t.Fatalf("daemon was reached with %q", got)
			default:
			}
		})
	}
}

func TestRawExecStartRefusedWhenTypedSessionsFillCap(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	logger, closeAudit, err := audit.New("", 20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeAudit)
	c.auditor = logger
	fd := &fakeDocker{}
	c.dockerClient = fd
	for i := 0; i < maxExecSessions; i++ {
		c.execSessions.Store("limit-"+strconv.Itoa(i), &ExecSession{})
	}

	c.handleRequest(context.Background(), protocol.RequestMessage{
		RequestID: "raw2", Method: http.MethodPost, Path: "/exec/abc/start",
	})
	var e protocol.ErrorMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeError), &e)
	if e.RequestID != "raw2" || e.Message != "agent busy: exec session limit reached" {
		t.Fatalf("error = %+v", e)
	}
	records := execRecords(logger)
	if len(records) != 1 || records[0].Outcome != audit.OutcomeDenied {
		t.Fatalf("exec records = %+v, want one denied record (a start refused by the exec cap inside handleRequestTo is audited there; one refused earlier by the stream cap is audited by the dispatch site)", records)
	}
}

func TestTypedExecRefusedWhenRawStartsFillCap(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	c.execAdmissionMu.Lock()
	c.rawExecStarts = maxExecSessions
	c.execAdmissionMu.Unlock()

	c.StartExec(context.Background(), protocol.ExecStartMessage{ExecID: "typed", ContainerID: "c1", Cmd: []string{"sh"}})
	var end protocol.ExecEndMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeExecEnd), &end)
	if end.ExecID != "typed" || end.Reason != "session limit reached" {
		t.Fatalf("exec_end = %+v", end)
	}
}

func TestRawExecSlotsShareCapWithTypedSessions(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	for i := 0; i < maxExecSessions-1; i++ {
		c.execSessions.Store("s-"+strconv.Itoa(i), &ExecSession{})
	}
	if !c.acquireRawExecSlot() {
		t.Fatal("last free slot refused")
	}
	if c.acquireRawExecSlot() {
		t.Fatal("slot admitted past the shared cap")
	}
	c.releaseRawExecSlot()
	if !c.acquireRawExecSlot() {
		t.Fatal("released slot not reusable")
	}
}

func TestNonExecRawRequestUnchanged(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	logger, closeAudit, err := audit.New("", 20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeAudit)
	c.auditor = logger
	//nolint:bodyclose // the response body is consumed and closed by handleRequest, the code under test.
	c.dockerClient = &fakeDocker{doResp: mkResp(http.StatusOK, "application/json", `{}`)}
	c.execAdmissionMu.Lock()
	c.rawExecStarts = maxExecSessions
	c.execAdmissionMu.Unlock()

	c.handleRequest(context.Background(), protocol.RequestMessage{RequestID: "n1", Method: http.MethodPost, Path: "/containers/create"})
	expectType(t, ctrl, protocol.TypeResponse)
	if got := len(execRecords(logger)); got != 0 {
		t.Fatalf("non-exec request produced %d exec records", got)
	}
}

// An attached raw start keeps its exec slot for as long as the daemon's stream
// stays open, so typed sessions see the cap shrink, and returns it at the end.
func TestRawAttachedExecStartHoldsSlotUntilStreamEnds(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	hold := make(chan struct{})
	dc, paths := realDockerDaemonHolding(t, hold)
	// Registered after the helper so it runs before the daemon's srv.Close
	// (cleanups run last-in first-out). srv.Close waits on the handler parked
	// on hold, so releasing hold first is what lets a failing assertion end the
	// test instead of hanging to the test timeout.
	t.Cleanup(func() {
		select {
		case <-hold:
		default:
			close(hold)
		}
	})
	c.dockerClient = dc
	for i := 0; i < maxExecSessions-1; i++ {
		c.execSessions.Store("s-"+strconv.Itoa(i), &ExecSession{})
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		c.handleRequest(context.Background(), protocol.RequestMessage{
			RequestID: "att", Method: http.MethodPost, Path: "/exec/abc/start",
			Body: []byte(`{"Detach":false,"Tty":true}`),
		})
	}()
	expectType(t, ctrl, protocol.TypeResponse)
	select {
	case <-paths:
	case <-time.After(readTimeout):
		t.Fatal("attached start never reached the daemon")
	}

	slots := func() int {
		c.execAdmissionMu.Lock()
		defer c.execAdmissionMu.Unlock()
		return c.rawExecStarts
	}
	if got := slots(); got != 1 {
		t.Fatalf("rawExecStarts = %d while the stream is open, want 1", got)
	}
	c.StartExec(context.Background(), protocol.ExecStartMessage{ExecID: "typed", ContainerID: "c1", Cmd: []string{"sh"}})
	var end protocol.ExecEndMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeExecEnd), &end)
	if end.Reason != "session limit reached" {
		t.Fatalf("typed start with the cap used = %+v", end)
	}

	close(hold)
	select {
	case <-done:
	case <-time.After(readTimeout):
		t.Fatal("attached start did not finish after the stream ended")
	}
	if got := slots(); got != 0 {
		t.Fatalf("rawExecStarts = %d after the stream ended, want 0", got)
	}
}

// With the request cap full, dispatch refuses before handleRequestTo runs. A
// refused raw exec start is still recorded once and never reaches the daemon;
// a refused request that is not an exec start writes no exec_start record.
func TestRefusedAtRequestCapAuditsRawExecStartOnce(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, method, path string
		want               int
	}{
		{"exec start", http.MethodPost, "/v1.47/exec/abc/start?x=1", 1},
		{"other request", http.MethodPost, "/containers/create", 0},
	} {
		for _, site := range []string{"inline", "streamed"} {
			t.Run(tc.name+"/"+site, func(t *testing.T) {
				t.Parallel()
				c, ctrl := newTestClient(t)
				logger, closeAudit, err := audit.New("", 20)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(closeAudit)
				c.auditor = logger
				dc, paths := realDockerDaemon(t)
				c.dockerClient = dc
				for i := 0; i < maxStreams; i++ {
					c.streamSem <- struct{}{}
				}
				req := protocol.RequestMessage{RequestID: "cap", Method: tc.method, Path: tc.path}

				if site == "inline" {
					runReadPump(t, c)
					sendEnvelope(t, ctrl, protocol.TypeRequest, req)
				} else {
					go c.dispatchStreamedBody(context.Background(), req, c.currentOutboundTarget(), 0)
				}
				var e protocol.ErrorMessage
				decodeData(t, expectType(t, ctrl, protocol.TypeError), &e)
				if e.RequestID != "cap" || e.Message != "agent busy: too many concurrent requests" {
					t.Fatalf("error = %+v", e)
				}

				records := execRecords(logger)
				if len(records) != tc.want {
					t.Fatalf("exec records = %+v, want %d", records, tc.want)
				}
				if tc.want == 1 && (records[0].ExecID != "abc" || records[0].Container != "/v1.47/exec/abc/start" || records[0].Outcome != audit.OutcomeDenied) {
					t.Fatalf("exec record = %+v", records[0])
				}
				select {
				case got := <-paths:
					t.Fatalf("daemon was reached with %q", got)
				default:
				}
			})
		}
	}
}
