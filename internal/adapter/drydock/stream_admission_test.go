package drydock

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/codeswhat/portwing/internal/adapter"
)

// denyingAdmitter always refuses admission, simulating a saturated stream
// limiter.
func denyingAdmitter() adapter.StreamAdmitter {
	return func() (func(), bool) { return nil, false }
}

// spyAdmitter counts Admit calls and release calls, always admitting.
type spyAdmitter struct {
	admits   atomic.Int32
	releases atomic.Int32
}

func (s *spyAdmitter) admitter() adapter.StreamAdmitter {
	return func() (func(), bool) {
		s.admits.Add(1)
		return func() { s.releases.Add(1) }, true
	}
}

func TestServeEventsRejectsWhenStreamLimitSaturated(t *testing.T) {
	t.Parallel()

	client, _, shutdown := newRouteTestDockerClient(t)
	defer shutdown()

	a := NewAdapter(client, "test-agent", AgentInfo{})
	mux := http.NewServeMux()
	noopAuth := func(h http.HandlerFunc) http.Handler { return h }
	a.RegisterRoutes(mux, noopAuth, denyingAdmitter())

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if got := rec.Body.String(); got != streamLimitRejectionMessage+"\n" {
		t.Fatalf("body = %q, want %q", got, streamLimitRejectionMessage+"\n")
	}

	a.sse.mu.RLock()
	clientCount := len(a.sse.clients)
	a.sse.mu.RUnlock()
	if clientCount != 0 {
		t.Fatalf("denied SSE session left %d clients registered, want 0", clientCount)
	}
}

func TestServeEventsAdmitsAndReleasesExactlyOnce(t *testing.T) {
	t.Parallel()

	client, _, shutdown := newRouteTestDockerClient(t)
	defer shutdown()

	a := NewAdapter(client, "test-agent", AgentInfo{})
	mux := http.NewServeMux()
	noopAuth := func(h http.HandlerFunc) http.Handler { return h }
	spy := &spyAdmitter{}
	a.RegisterRoutes(mux, noopAuth, spy.admitter())

	// The SSE handler streams until the client disconnects; an already-
	// canceled request context makes it return immediately after
	// registering (and then deregistering) the client.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if got := spy.admits.Load(); got != 1 {
		t.Fatalf("admits = %d, want 1", got)
	}
	if got := spy.releases.Load(); got != 1 {
		t.Fatalf("releases = %d, want 1", got)
	}
}

func TestHandleContainerLogsFollowRejectedBeforeDockerCall(t *testing.T) {
	t.Parallel()

	client, calls, shutdown := newRouteTestDockerClient(t)
	defer shutdown()

	a := NewAdapter(client, "test-agent", AgentInfo{})
	a.admit = denyingAdmitter()

	before := calls.logsCalls.Load()

	req := httptest.NewRequest(http.MethodGet, "/api/containers/container-1/logs?follow=1", nil)
	req.SetPathValue("id", "container-1")
	rec := httptest.NewRecorder()

	a.handleContainerLogs(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if got := rec.Body.String(); got != streamLimitRejectionMessage+"\n" {
		t.Fatalf("body = %q, want %q", got, streamLimitRejectionMessage+"\n")
	}
	if got := calls.logsCalls.Load(); got != before {
		t.Fatalf("docker logs call count = %d, want unchanged %d (rejected before daemon call)", got, before)
	}
}

func TestHandleContainerLogsNonFollowSucceedsEvenWhenStreamLimitSaturated(t *testing.T) {
	t.Parallel()

	client, calls, shutdown := newRouteTestDockerClient(t)
	defer shutdown()

	a := NewAdapter(client, "test-agent", AgentInfo{})
	a.admit = denyingAdmitter()

	req := httptest.NewRequest(http.MethodGet, "/api/containers/container-1/logs", nil)
	req.SetPathValue("id", "container-1")
	rec := httptest.NewRecorder()

	a.handleContainerLogs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("non-follow request status = %d, want %d (gate must be follow-scoped only)", rec.Code, http.StatusOK)
	}
	if calls.logsCalls.Load() == 0 {
		t.Fatal("expected docker logs call for non-follow request")
	}
}

func TestHandleContainerLogsFollowAdmitsAndReleasesExactlyOnce(t *testing.T) {
	t.Parallel()

	client, _, shutdown := newRouteTestDockerClient(t)
	defer shutdown()

	a := NewAdapter(client, "test-agent", AgentInfo{})
	spy := &spyAdmitter{}
	a.admit = spy.admitter()

	req := httptest.NewRequest(http.MethodGet, "/api/containers/container-1/logs?follow=1", nil)
	req.SetPathValue("id", "container-1")
	rec := httptest.NewRecorder()

	a.handleContainerLogs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := spy.admits.Load(); got != 1 {
		t.Fatalf("admits = %d, want 1", got)
	}
	if got := spy.releases.Load(); got != 1 {
		t.Fatalf("releases = %d, want 1", got)
	}
}
