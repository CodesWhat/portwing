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

	"github.com/codeswhat/portwing/internal/adapter/drydock"
	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/docker"
)

// newBlockingStreamDaemon starts a fake Docker daemon on a Unix socket whose
// streaming (/logs) responses stay open until the returned release func is
// called, so a test can hold stream slots for as long as it needs. Every
// request that reaches the streaming handler is reported on the entered
// channel. Non-streaming paths answer immediately with the version payload,
// which also satisfies the API negotiation docker.NewClient performs.
func newBlockingStreamDaemon(t *testing.T) (*docker.Client, <-chan string, func()) {
	t.Helper()

	sockPath, cleanupSocket := shortSocketPath(t)
	listener := newUnixListener(t, sockPath)

	entered := make(chan string, 16)
	hold := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(hold) }) }

	daemon := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !strings.HasSuffix(r.URL.Path, "/logs") {
			_, _ = io.WriteString(w, `{"Version":"26.0.0","ApiVersion":"1.44"}`)
			return
		}
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		entered <- r.URL.Path
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
	return client, entered, release
}

func waitForStream(t *testing.T, entered <-chan string) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("streaming request never reached the fake daemon")
	}
}

func TestConcurrencyLimiterNilIsUnbounded(t *testing.T) {
	t.Parallel()

	var unbounded *concurrencyLimiter
	for i := 0; i < 3; i++ {
		if !unbounded.acquire() {
			t.Fatalf("nil limiter refused acquire %d", i)
		}
	}
	unbounded.release()
	if got := unbounded.limit(); got != 0 {
		t.Fatalf("nil limiter limit = %d, want 0", got)
	}

	if newConcurrencyLimiter(0) != nil {
		t.Fatal("newConcurrencyLimiter(0) must be nil (unbounded)")
	}
	if newConcurrencyLimiter(-1) != nil {
		t.Fatal("newConcurrencyLimiter(-1) must be nil (unbounded)")
	}

	bounded := newConcurrencyLimiter(2)
	if got := bounded.limit(); got != 2 {
		t.Fatalf("limit = %d, want 2", got)
	}
	for i := 0; i < 2; i++ {
		if !bounded.acquire() {
			t.Fatalf("bounded limiter refused free slot %d", i)
		}
	}
	if bounded.acquire() {
		t.Fatal("bounded limiter admitted a third session past a limit of 2")
	}
	bounded.release()
	if !bounded.acquire() {
		t.Fatal("released slot was not reusable")
	}
}

func TestStreamProxyRejectsBeyondStreamLimit(t *testing.T) {
	t.Parallel()

	client, entered, release := newBlockingStreamDaemon(t)
	s := &Server{dockerClient: client, streamSem: newConcurrencyLimiter(1)}

	firstCode := make(chan int, 1)
	go func() {
		rec := httptest.NewRecorder()
		s.handleDockerProxy(rec, httptest.NewRequest(http.MethodGet, "/v1.44/containers/abc/logs?follow=1", nil))
		firstCode <- rec.Code
	}()
	waitForStream(t, entered)

	rec := httptest.NewRecorder()
	s.handleDockerProxy(rec, httptest.NewRequest(http.MethodGet, "/v1.44/containers/def/logs?follow=1", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("second concurrent stream status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "agent busy: too many concurrent streams" {
		t.Fatalf("over-limit body = %q", got)
	}

	release()
	select {
	case code := <-firstCode:
		if code != http.StatusOK {
			t.Fatalf("first stream status = %d, want %d", code, http.StatusOK)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first stream did not finish after the daemon released it")
	}

	// The slot has to come back when the stream ends, or the cap becomes a
	// one-shot budget that permanently wedges the agent.
	rec = httptest.NewRecorder()
	s.handleDockerProxy(rec, httptest.NewRequest(http.MethodGet, "/v1.44/containers/ghi/logs?follow=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("stream after slot release = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestNonStreamingProxyIgnoresStreamLimit(t *testing.T) {
	t.Parallel()

	client, _, _ := newBlockingStreamDaemon(t)
	s := &Server{dockerClient: client, streamSem: newConcurrencyLimiter(1)}
	if !s.streamSem.acquire() {
		t.Fatal("could not saturate the stream limiter")
	}

	rec := httptest.NewRecorder()
	s.handleDockerProxy(rec, httptest.NewRequest(http.MethodGet, "/v1.44/containers/json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("non-streaming request while streams are saturated = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestStreamProxyUnboundedWithoutLimiter(t *testing.T) {
	t.Parallel()

	client, entered, release := newBlockingStreamDaemon(t)
	// A Server assembled as a struct literal has no limiter; nil must mean
	// unbounded, not "reject everything".
	s := &Server{dockerClient: client}

	const streams = 3
	codes := make(chan int, streams)
	for i := 0; i < streams; i++ {
		go func() {
			rec := httptest.NewRecorder()
			s.handleDockerProxy(rec, httptest.NewRequest(http.MethodGet, "/v1.44/containers/abc/logs?follow=1", nil))
			codes <- rec.Code
		}()
	}
	for i := 0; i < streams; i++ {
		waitForStream(t, entered)
	}

	release()
	for i := 0; i < streams; i++ {
		select {
		case code := <-codes:
			if code != http.StatusOK {
				t.Fatalf("concurrent stream %d status = %d, want %d", i, code, http.StatusOK)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("concurrent stream %d did not finish", i)
		}
	}
}

func TestDockerHijackRejectsBeyondExecSessionLimit(t *testing.T) {
	t.Parallel()

	auditor, closeAudit, err := audit.New("", 8)
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}
	defer closeAudit()

	s := &Server{
		execSem:     newConcurrencyLimiter(1),
		auditor:     auditor,
		rateLimiter: NewRateLimiter(),
	}
	defer s.rateLimiter.Stop()
	if !s.execSem.acquire() {
		t.Fatal("could not saturate the exec limiter")
	}

	req := httptest.NewRequest(http.MethodPost, "/v1.44/exec/abc123/start", strings.NewReader(`{"Tty":true}`))
	req.Header.Set("Upgrade", "tcp")
	rec := httptest.NewRecorder()
	s.handleDockerHijack(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("exec start past the session limit = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "agent busy: exec session limit reached" {
		t.Fatalf("over-limit body = %q", got)
	}
}

func TestDockerHijackReleasesExecSlot(t *testing.T) {
	t.Parallel()

	s := &Server{execSem: newConcurrencyLimiter(1)}
	// A ResponseRecorder is not an http.Hijacker, so the session ends on the
	// early 500 return — the slot still has to come back.
	rec := httptest.NewRecorder()
	s.handleDockerHijack(rec, httptest.NewRequest(http.MethodPost, "/v1.44/containers/abc123/attach", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("non-hijackable attach = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if !s.execSem.acquire() {
		t.Fatal("exec slot was not released after the session ended")
	}
}

func TestDockerHijackUnboundedWithoutLimiter(t *testing.T) {
	t.Parallel()

	s := &Server{}
	rec := httptest.NewRecorder()
	s.handleDockerHijack(rec, httptest.NewRequest(http.MethodPost, "/v1.44/containers/abc123/attach", nil))
	if rec.Code == http.StatusServiceUnavailable {
		t.Fatal("nil exec limiter rejected a session; nil must mean unbounded")
	}
}

func TestNewServerAppliesConfiguredConcurrencyLimits(t *testing.T) {
	t.Parallel()

	client, cleanup := newStubDockerClient(t)
	defer cleanup()

	cfg := minimalConfig()
	cfg.MaxStreamSessions = 7
	cfg.MaxExecSessions = 5
	s, err := NewServer(cfg, client, &stubServerAdapter{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if got := s.streamSem.limit(); got != 7 {
		t.Errorf("stream limit = %d, want 7", got)
	}
	if got := s.execSem.limit(); got != 5 {
		t.Errorf("exec limit = %d, want 5", got)
	}

	unlimited := minimalConfig()
	unlimited.MaxStreamSessions = 0
	unlimited.MaxExecSessions = -1
	s, err = NewServer(unlimited, client, &stubServerAdapter{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if s.streamSem != nil {
		t.Error("MAX_STREAM_SESSIONS=0 must leave streams unbounded")
	}
	if s.execSem != nil {
		t.Error("a negative MAX_EXEC_SESSIONS must leave exec sessions unbounded")
	}
}

// TestAdapterStreamSharesDockerProxyStreamLimit proves that adapter routes
// (drydock's GET /api/events here) share the same s.streamSem instance as the
// Docker-proxy streaming path rather than an independently configured second
// limiter: with MaxStreamSessions=1, a Docker-proxy log stream holding the
// sole slot must cause a concurrent /api/events request to be rejected, and
// /api/events must succeed once the proxy stream's slot is released.
func TestAdapterStreamSharesDockerProxyStreamLimit(t *testing.T) {
	t.Parallel()

	client, entered, release := newBlockingStreamDaemon(t)

	cfg := minimalConfig()
	cfg.MaxStreamSessions = 1
	a := drydock.NewAdapter(client, "test-agent", drydock.AgentInfo{})
	s, err := NewServer(cfg, client, a)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	srv := httptest.NewServer(s.httpServer.Handler)
	defer srv.Close()

	// Saturate the single stream slot with a Docker-proxy follow-mode log
	// request.
	proxyDone := make(chan int, 1)
	go func() {
		//nolint:bodyclose // Closed in the goroutine after reading the status.
		resp, err := http.Get(srv.URL + "/v1.44/containers/abc/logs?follow=1")
		if err != nil {
			proxyDone <- -1
			return
		}
		defer resp.Body.Close()
		proxyDone <- resp.StatusCode
	}()
	waitForStream(t, entered)

	// The sole stream slot is held by the proxy request; a concurrent
	// /api/events request must be rejected.
	eventsResp, err := http.Get(srv.URL + "/api/events")
	if err != nil {
		t.Fatalf("GET /api/events while saturated: %v", err)
	}
	body, _ := io.ReadAll(eventsResp.Body)
	eventsResp.Body.Close()
	if eventsResp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("/api/events status while stream slot saturated = %d, want %d", eventsResp.StatusCode, http.StatusServiceUnavailable)
	}
	if got := strings.TrimSpace(string(body)); got != "agent busy: too many concurrent streams" {
		t.Fatalf("/api/events over-limit body = %q", got)
	}

	// Release the proxy stream's slot.
	release()
	select {
	case code := <-proxyDone:
		if code != http.StatusOK {
			t.Fatalf("proxy stream status = %d, want %d", code, http.StatusOK)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("proxy stream did not finish after the daemon released it")
	}

	// The slot is now free; /api/events must succeed. The SSE handler
	// streams until the client disconnects, so bound the request with a
	// context that cancels once the response headers arrive.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events", nil)
	if err != nil {
		t.Fatalf("new /api/events request: %v", err)
	}
	eventsResp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/events after slot release: %v", err)
	}
	defer eventsResp2.Body.Close()
	if eventsResp2.StatusCode != http.StatusOK {
		t.Fatalf("/api/events status after slot release = %d, want %d", eventsResp2.StatusCode, http.StatusOK)
	}
}

// TestAdapterStreamUnboundedWithoutStreamLimit proves that MAX_STREAM_SESSIONS
// <= 0 leaves adapter streaming routes unbounded, matching
// handleDockerProxy's existing nil-limiter behavior: any number of
// concurrent /api/events connections must be admitted.
func TestAdapterStreamUnboundedWithoutStreamLimit(t *testing.T) {
	t.Parallel()

	client, cleanup := newStubDockerClient(t)
	defer cleanup()

	cfg := minimalConfig()
	cfg.MaxStreamSessions = 0
	a := drydock.NewAdapter(client, "test-agent", drydock.AgentInfo{})
	s, err := NewServer(cfg, client, a)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if s.streamSem != nil {
		t.Fatal("MAX_STREAM_SESSIONS=0 must leave streams unbounded")
	}

	srv := httptest.NewServer(s.httpServer.Handler)
	defer srv.Close()

	const concurrentClients = 5
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	codes := make(chan int, concurrentClients)
	for i := 0; i < concurrentClients; i++ {
		go func() {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events", nil)
			if err != nil {
				codes <- -1
				return
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				codes <- -1
				return
			}
			defer resp.Body.Close()
			codes <- resp.StatusCode
		}()
	}

	for i := 0; i < concurrentClients; i++ {
		select {
		case code := <-codes:
			if code != http.StatusOK {
				t.Errorf("client %d status = %d, want %d (unbounded stream limit)", i, code, http.StatusOK)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for a concurrent /api/events client")
		}
	}
}
