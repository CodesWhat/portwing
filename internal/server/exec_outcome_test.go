package server

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/docker"
)

// An upgraded exec start refused by the exec session limit is recorded once as
// denied, answered 503, and never reaches the daemon (the recorder is not a
// Hijacker, so reaching the hijack would answer 500 instead).
func TestHijackExecStartRefusedIsAuditedDenied(t *testing.T) {
	t.Parallel()

	client, bodies, _, _ := execStartDaemon(t)
	s := newExecStartServer(t, client, 1, 0)
	if !s.execSem.acquire() {
		t.Fatal("could not fill the exec slot")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1.47/exec/abc123/start", strings.NewReader(`{}`))
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "tcp")
	s.handleDockerProxy(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	select {
	case b := <-bodies:
		t.Fatalf("refused start reached the daemon with body %q", b)
	default:
	}
	records := execStartRecords(s)
	if len(records) != 1 || records[0].Outcome != audit.OutcomeDenied || records[0].ExecID != "abc123" {
		t.Fatalf("exec start records = %+v, want one denied record for abc123", records)
	}
}

// An admitted exec start is recorded once as allowed.
func TestAdmittedExecStartAuditedAllowedOnce(t *testing.T) {
	t.Parallel()

	client, bodies, _, _ := execStartDaemon(t)
	s := newExecStartServer(t, client, 1, 0)

	rec := httptest.NewRecorder()
	s.handleDockerProxy(rec, httptest.NewRequest(http.MethodPost, "/exec/abc123/start", strings.NewReader(`{"Detach":true}`)))
	<-bodies

	records := execStartRecords(s)
	if len(records) != 1 || records[0].Outcome != audit.OutcomeAllowed {
		t.Fatalf("exec start records = %+v, want one allowed record", records)
	}
}

// When the daemon cannot be dialled after an upgraded exec start was admitted,
// the exec_start record stays allowed (the admission decision) and the request's
// api_request record carries the 502 the client was sent.
func TestHijackDialFailureRecordedAsBadGatewayStatus(t *testing.T) {
	t.Parallel()

	sockPath, cleanup := shortSocketPath(t)
	defer cleanup()
	client, err := docker.NewClient(sockPath, 5)
	if err != nil {
		t.Fatalf("docker.NewClient: %v", err)
	}
	s := newExecStartServer(t, client, 1, 0)
	s.dockerDialer = func(string, string) (net.Conn, error) { return nil, net.ErrClosed }

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	response := captureHijackedResponse(clientConn)
	hrw := &hijackableResponseWriter{
		conn: serverConn,
		buf:  bufio.NewReadWriter(bufio.NewReader(serverConn), bufio.NewWriter(serverConn)),
		hdr:  make(http.Header),
	}
	rw := &statusRecorder{ResponseWriter: hrw, code: http.StatusOK}

	s.handleDockerHijack(rw, httptest.NewRequest(http.MethodPost, "/exec/abc123/start", strings.NewReader(`{}`)))
	_ = serverConn.Close()
	requireBadGatewayResponse(t, response)

	if rw.code != http.StatusBadGateway {
		t.Fatalf("recorded status = %d, want 502", rw.code)
	}
	records := execStartRecords(s)
	if len(records) != 1 || records[0].Outcome != audit.OutcomeAllowed {
		t.Fatalf("exec start records = %+v, want one allowed record", records)
	}
}

func TestNoteHijackStatus(t *testing.T) {
	t.Parallel()

	rw := &statusRecorder{ResponseWriter: httptest.NewRecorder(), code: http.StatusOK}
	noteHijackStatus(rw, http.StatusSwitchingProtocols)
	if rw.code != http.StatusOK {
		t.Fatalf("a successful upgrade changed the recorded status to %d", rw.code)
	}
	noteHijackStatus(rw, http.StatusConflict)
	if rw.code != http.StatusConflict {
		t.Fatalf("recorded status = %d, want 409", rw.code)
	}
	noteHijackStatus(httptest.NewRecorder(), http.StatusConflict) // not a recorder: must not panic
}

// hijackExecThroughProxy sends an upgraded exec start to the proxy listening at
// ts and returns the status code of the proxy's response.
func hijackExecThroughProxy(t *testing.T, ts *httptest.Server) int {
	t.Helper()
	conn, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	const req = "POST /exec/abc123/start HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer proxy-secret\r\n" +
		"Connection: Upgrade\r\nUpgrade: tcp\r\nContent-Length: 2\r\n\r\n{}"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read proxy response: %v", err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// waitForExecAPIRequest returns the api_request record the middleware writes
// once the exec handler returns, waiting a bounded time for it.
func waitForExecAPIRequest(t *testing.T, s *Server) audit.Record {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, rec := range s.auditor.Records(0) {
			if rec.Event == audit.EventAPIRequest && strings.HasSuffix(rec.Path, "/exec/abc123/start") {
				return rec
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no api_request record for the exec start")
	return audit.Record{}
}

// Through the real proxy, the exec_start record is already in the ring, as the
// one allowed record, when the daemon first sees the upgraded request.
func TestHijackExecStartRecordedBeforeDaemonSeesRequest(t *testing.T) {
	t.Parallel()

	var srv atomic.Pointer[Server]
	seen := make(chan []audit.Record, 1)
	s, ts := newStreamRouteServer(t, func(w http.ResponseWriter, _ *http.Request) {
		if cur := srv.Load(); cur != nil {
			select {
			case seen <- execStartRecords(cur):
			default:
			}
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		_, _ = conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: tcp\r\nConnection: Upgrade\r\n\r\n"))
		_ = conn.Close()
	}, 5, 4)
	srv.Store(s)

	if status := hijackExecThroughProxy(t, ts); status != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", status)
	}
	select {
	case records := <-seen:
		if len(records) != 1 || records[0].Outcome != audit.OutcomeAllowed || records[0].ExecID != "abc123" {
			t.Fatalf("exec_start records when the daemon saw the request = %+v, want one allowed record", records)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon never saw the request")
	}
	if rec := waitForExecAPIRequest(t, s); rec.Status != http.StatusOK {
		t.Fatalf("successful upgrade recorded status %d, want 200", rec.Status)
	}
}

// A daemon that answers an upgraded exec start with a non-101 status has that
// status on the request's api_request record, not the 200 default.
func TestHijackDaemonRejectionRecordedWithItsStatus(t *testing.T) {
	t.Parallel()

	s, ts := newStreamRouteServer(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "exec already running", http.StatusConflict)
	}, 5, 4)

	if status := hijackExecThroughProxy(t, ts); status != http.StatusConflict {
		t.Fatalf("status = %d, want 409", status)
	}
	if rec := waitForExecAPIRequest(t, s); rec.Status != http.StatusConflict {
		t.Fatalf("api_request status = %d, want 409", rec.Status)
	}
	records := execStartRecords(s)
	if len(records) != 1 || records[0].Outcome != audit.OutcomeAllowed {
		t.Fatalf("exec_start records = %+v, want one allowed record", records)
	}
}
