package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/docker"
)

// execStartDaemon is a fake Docker daemon for exec start requests. Detached
// starts (a body containing "Detach":true) answer at once; attached starts
// stay open until the returned release func is called. Every exec start body
// the daemon receives is reported on bodies.
func execStartDaemon(t *testing.T) (*docker.Client, <-chan string, <-chan struct{}, func()) {
	t.Helper()

	sockPath, cleanupSocket := shortSocketPath(t)
	listener := newUnixListener(t, sockPath)

	bodies := make(chan string, 16)
	attached := make(chan struct{}, 16)
	hold := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(hold) }) }

	daemon := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/start") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"Version":"26.0.0","ApiVersion":"1.44"}`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		bodies <- string(body)
		w.WriteHeader(http.StatusOK)
		if strings.Contains(string(body), `"Detach":true`) {
			return
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		attached <- struct{}{}
		<-hold
	})}
	go func() { _ = daemon.Serve(listener) }()

	t.Cleanup(func() {
		release()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = daemon.Shutdown(ctx)
		cleanupSocket()
	})

	client, err := docker.NewClient(sockPath, 5)
	if err != nil {
		t.Fatalf("docker.NewClient: %v", err)
	}
	return client, bodies, attached, release
}

func newExecStartServer(t *testing.T, client *docker.Client, execLimit, streamLimit int) *Server {
	t.Helper()
	auditor, _, err := audit.New("", 64)
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}
	t.Cleanup(auditor.Close)
	rl := NewRateLimiter()
	t.Cleanup(rl.Stop)
	return &Server{
		dockerClient: client,
		auditor:      auditor,
		rateLimiter:  rl,
		execSem:      newConcurrencyLimiter(execLimit),
		streamSem:    newConcurrencyLimiter(streamLimit),
	}
}

func execStartRecords(s *Server) []audit.Record {
	var out []audit.Record
	for _, rec := range s.auditor.Records(0) {
		if rec.Event == audit.EventExecStart {
			out = append(out, rec)
		}
	}
	return out
}

func TestDetachedExecStartIsAuditedAndForwarded(t *testing.T) {
	t.Parallel()

	client, bodies, _, _ := execStartDaemon(t)
	s := newExecStartServer(t, client, 1, 1)

	const payload = `{"Detach":true,"Tty":false}`
	rec := httptest.NewRecorder()
	s.handleDockerProxy(rec, httptest.NewRequest(http.MethodPost, "/v1.47/exec/abc123/start", strings.NewReader(payload)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := <-bodies; got != payload {
		t.Fatalf("daemon received body %q, want %q", got, payload)
	}

	records := execStartRecords(s)
	if len(records) != 1 {
		t.Fatalf("exec start records = %d, want 1: %+v", len(records), records)
	}
	if records[0].ExecID != "abc123" || records[0].Container != "/v1.47/exec/abc123/start" || records[0].Outcome != audit.OutcomeAllowed {
		t.Fatalf("exec record = %+v", records[0])
	}

	// The slot returned with the response: with a limit of 1 a second start is admitted.
	rec = httptest.NewRecorder()
	s.handleDockerProxy(rec, httptest.NewRequest(http.MethodPost, "/v1.47/exec/def456/start", strings.NewReader(payload)))
	if rec.Code != http.StatusOK {
		t.Fatalf("second detached start status = %d, want 200 (slot not released)", rec.Code)
	}
	if got := len(execStartRecords(s)); got != 2 {
		t.Fatalf("exec start records = %d, want 2", got)
	}
}

func TestDetachedExecStartRefusedWhenExecSlotsFull(t *testing.T) {
	t.Parallel()

	client, bodies, _, _ := execStartDaemon(t)
	s := newExecStartServer(t, client, 1, 0)
	if !s.execSem.acquire() {
		t.Fatal("could not fill the exec slot")
	}

	rec := httptest.NewRecorder()
	s.handleDockerProxy(rec, httptest.NewRequest(http.MethodPost, "/v1.47/exec/abc123/start", strings.NewReader(`{"Detach":true}`)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "agent busy: exec session limit reached" {
		t.Fatalf("body = %q", got)
	}
	select {
	case b := <-bodies:
		t.Fatalf("refused start reached the daemon with body %q", b)
	default:
	}
	// Same as the hijack path: the attempt is audited even when refused.
	if got := len(execStartRecords(s)); got != 1 {
		t.Fatalf("exec start records = %d, want 1", got)
	}
}

func TestAttachedExecStartWithoutUpgradeHoldsSlotUntilStreamEnds(t *testing.T) {
	t.Parallel()

	client, bodies, attached, release := execStartDaemon(t)
	s := newExecStartServer(t, client, 1, 1)

	done := make(chan int, 1)
	go func() {
		rec := httptest.NewRecorder()
		s.handleDockerProxy(rec, httptest.NewRequest(http.MethodPost, "/v1.47/exec/abc123/start", strings.NewReader(`{"Detach":false,"Tty":true}`)))
		done <- rec.Code
	}()
	<-bodies
	select {
	case <-attached:
	case <-time.After(5 * time.Second):
		t.Fatal("attached start never reached the daemon")
	}

	if got := len(execStartRecords(s)); got != 1 {
		t.Fatalf("exec start records = %d, want 1", got)
	}
	// It took an exec slot, not a stream slot, so the stream limit is still free.
	if !s.streamSem.acquire() {
		t.Fatal("non-upgrade exec start consumed a stream slot as well")
	}
	s.streamSem.release()

	rec := httptest.NewRecorder()
	s.handleDockerProxy(rec, httptest.NewRequest(http.MethodPost, "/v1.47/exec/def456/start", strings.NewReader(`{"Detach":true}`)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("start with slot held: status = %d, want 503", rec.Code)
	}

	release()
	select {
	case code := <-done:
		if code != http.StatusOK {
			t.Fatalf("attached start status = %d, want 200", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("attached start did not finish")
	}
	if !s.execSem.acquire() {
		t.Fatal("exec slot not released after the stream ended")
	}
	s.execSem.release()
}

func TestExecStartRoutesClassifiedAsDaemonRoutes(t *testing.T) {
	t.Parallel()

	client, bodies, _, _ := execStartDaemon(t)
	s := newExecStartServer(t, client, 0, 0)

	for _, tc := range []struct{ target, wantID string }{
		{"/exec/plain/start", "plain"},
		{"/v1.47/exec/abc/start", "abc"},
		{"/v1.47/exec/a%2Fb/start", "a/b"},
	} {
		before := len(execStartRecords(s))
		rec := httptest.NewRecorder()
		s.handleDockerProxy(rec, httptest.NewRequest(http.MethodPost, tc.target, strings.NewReader(`{"Detach":true}`)))
		<-bodies
		records := execStartRecords(s)
		if len(records) != before+1 {
			t.Fatalf("%s: exec start records = %d, want %d", tc.target, len(records), before+1)
		}
		if got := records[0].ExecID; got != tc.wantID {
			t.Fatalf("%s: exec id = %q, want %q", tc.target, got, tc.wantID)
		}
	}

	// A non-exec route is never recorded as an exec start.
	before := len(execStartRecords(s))
	rec := httptest.NewRecorder()
	s.handleDockerProxy(rec, httptest.NewRequest(http.MethodGet, "/v1.47/containers/json", nil))
	if got := len(execStartRecords(s)); got != before {
		t.Fatalf("containers/json produced an exec start record")
	}
}

// The daemon routes only POST to exec start, so any other method on that path
// is not an exec start: no record, no slot, normal handling.
func TestNonPostExecStartPathIsNotAnExecStart(t *testing.T) {
	t.Parallel()

	client, bodies, _, _ := execStartDaemon(t)
	s := newExecStartServer(t, client, 1, 0)
	if !s.execSem.acquire() {
		t.Fatal("could not fill the exec slot")
	}

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		rec := httptest.NewRecorder()
		s.handleDockerProxy(rec, httptest.NewRequest(method, "/v1.47/exec/abc/start", strings.NewReader(`{"Detach":true}`)))
		if rec.Code == http.StatusServiceUnavailable {
			t.Fatalf("%s took an exec slot and was refused", method)
		}
		<-bodies
	}
	if got := len(execStartRecords(s)); got != 0 {
		t.Fatalf("non-POST requests produced %d exec start records", got)
	}
}
