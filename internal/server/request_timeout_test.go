// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/docker"
	"github.com/codeswhat/portwing/internal/protocol"
)

// timeoutProxyServer wires a Server to a fake dockerd that answers after delay.
// The docker client has a 1 s REQUEST_TIMEOUT. seen records the hint header the
// daemon received for each request.
func timeoutProxyServer(t *testing.T, delay time.Duration) (*Server, *[]string) {
	t.Helper()
	sockPath, cleanup := shortSocketPath(t)
	ln := newUnixListener(t, sockPath)
	var mu sync.Mutex
	seen := &[]string{}
	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"Version":"26.0.0","ApiVersion":"1.44"}`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*seen = append(*seen, r.Header.Get(protocol.RequestTimeoutHeader))
		mu.Unlock()
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "done")
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		cleanup()
	})
	client, err := docker.NewClient(sockPath, 1)
	if err != nil {
		t.Fatal(err)
	}
	auditor, closeAudit, _ := audit.New("", 0)
	t.Cleanup(closeAudit)
	s := &Server{dockerClient: client, rateLimiter: NewRateLimiter(), auditor: auditor}
	t.Cleanup(s.rateLimiter.Stop)
	return s, seen
}

func TestProxyTimeoutHintExtendsDeadlineAndIsStripped(t *testing.T) {
	t.Parallel()
	s, seen := timeoutProxyServer(t, 1500*time.Millisecond)

	req := httptest.NewRequest(http.MethodPost, "/v1.44/containers/a/stop", nil)
	req.Header.Set(protocol.RequestTimeoutHeader, "5000")
	rec := httptest.NewRecorder()
	s.handleDockerProxy(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "done" {
		t.Fatalf("hinted call = %d %q", rec.Code, rec.Body.String())
	}
	if len(*seen) != 1 || (*seen)[0] != "" {
		t.Fatalf("daemon saw hint header %q, want it stripped", *seen)
	}
}

func TestProxyAutoDeadlineForStopAndWait(t *testing.T) {
	t.Parallel()
	s, _ := timeoutProxyServer(t, 1500*time.Millisecond)
	for _, path := range []string{"/containers/a/stop?t=1", "/containers/a/wait?condition=next-exit"} {
		rec := httptest.NewRecorder()
		s.handleDockerProxy(rec, httptest.NewRequest(http.MethodPost, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s = %d, want 200", path, rec.Code)
		}
	}
}

func TestProxyDefaultTimeoutKeeps502(t *testing.T) {
	t.Parallel()
	s, _ := timeoutProxyServer(t, 3*time.Second)
	rec := httptest.NewRecorder()
	s.handleDockerProxy(rec, httptest.NewRequest(http.MethodGet, "/containers/json", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code = %d, want 502", rec.Code)
	}
	if got := rec.Header().Get(protocol.CodeHeader); got != "" {
		t.Fatalf("plain default timeout carried code %q", got)
	}
}

func TestProxyHintedTimeoutIs504WithCode(t *testing.T) {
	t.Parallel()
	s, _ := timeoutProxyServer(t, 3*time.Second)
	req := httptest.NewRequest(http.MethodGet, "/containers/json", nil)
	req.Header.Set(protocol.RequestTimeoutHeader, "1000")
	rec := httptest.NewRecorder()
	s.handleDockerProxy(rec, req)
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("code = %d, want 504", rec.Code)
	}
	if got := rec.Header().Get(protocol.CodeHeader); got != protocol.ErrorCodeRequestTimeout {
		t.Fatalf("code header = %q", got)
	}
}

func TestProxyMalformedHintIsIgnored(t *testing.T) {
	t.Parallel()
	s, _ := timeoutProxyServer(t, 3*time.Second)
	for _, v := range []string{"abc", "-1", "1.5", "99999999", "0"} {
		req := httptest.NewRequest(http.MethodGet, "/containers/json", nil)
		req.Header.Set(protocol.RequestTimeoutHeader, v)
		rec := httptest.NewRecorder()
		s.handleDockerProxy(rec, req)
		if rec.Code != http.StatusBadGateway {
			t.Errorf("hint %q: code = %d, want 502 (no hint)", v, rec.Code)
		}
	}
}
