package server

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

// An admitted exec start is recorded once as allowed, and the slot it took
// stays counted until the handler returns.
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
