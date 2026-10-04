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

func TestRawExecStartIDClassification(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		method, path, id string
		ok               bool
	}{
		{http.MethodPost, "/exec/abc/start", "abc", true},
		{http.MethodPost, "/v1.47/exec/abc/start", "abc", true},
		{http.MethodPost, "/v1.47/exec/abc/start?x=1", "abc", true},
		{http.MethodPost, "/v1.47/exec/a%2Fb/start", "a/b", true},
		{http.MethodGet, "/v1.47/exec/abc/start", "", false},
		{http.MethodPost, "/v1.47/exec/abc/json", "", false},
		{http.MethodPost, "/containers/create", "", false},
	} {
		id, ok := rawExecStartID(tc.method, tc.path)
		if id != tc.id || ok != tc.ok {
			t.Errorf("%s %s = (%q, %v), want (%q, %v)", tc.method, tc.path, id, ok, tc.id, tc.ok)
		}
	}
}

// realDockerDaemon serves a fake daemon on a Unix socket behind a real
// docker.Client, so the request path goes through the client's URL join.
// Every request other than the version negotiation is reported on paths.
func realDockerDaemon(t *testing.T) (*docker.Client, <-chan string) {
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
		paths <- r.URL.Path
		w.WriteHeader(http.StatusOK)
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
	if len(records) != 1 || records[0].ExecID != "abc" || records[0].Outcome != audit.OutcomeAllowed {
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
	if got := len(execRecords(logger)); got != 1 {
		t.Fatalf("exec records = %d, want 1 (a refused start that got this far is audited; one refused earlier by the stream cap never reaches here)", got)
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
