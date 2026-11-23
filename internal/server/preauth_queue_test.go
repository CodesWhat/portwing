// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/auth"
)

func preAuthRefs(rl *RateLimiter, key string) int {
	rl.preAuthMu.Lock()
	defer rl.preAuthMu.Unlock()
	if e, ok := rl.preAuth[key]; ok {
		return e.refs
	}
	return 0
}

func preAuthTableLen(rl *RateLimiter) int {
	rl.preAuthMu.Lock()
	defer rl.preAuthMu.Unlock()
	return len(rl.preAuth)
}

func dialSlowSignedBody(t *testing.T, addr string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	var raw strings.Builder
	raw.WriteString("POST /_portwing/info HTTP/1.1\r\nHost: " + addr + "\r\nContent-Length: 64\r\n")
	raw.WriteString(auth.HeaderSignature + ": bogus\r\n")
	raw.WriteString(auth.HeaderSignatureVersion + ": " + auth.SignatureVersion2 + "\r\n\r\n")
	if _, err := conn.Write([]byte(raw.String())); err != nil {
		t.Fatalf("write headers: %v", err)
	}
	return conn
}

func TestPreAuthSlowBodiesQueueTheSeventeenthRequest(t *testing.T) {
	ed, priv := setupEd25519(t)
	rl := NewRateLimiter()
	defer rl.Stop()
	orig := authBodyReadDeadline
	authBodyReadDeadline = 3 * time.Second
	t.Cleanup(func() { authBodyReadDeadline = orig })

	ts := httptest.NewServer(rl.AuthMiddlewareWithEd25519(nil, ed, noAudit(t), nil, http.HandlerFunc(okHandler)))
	defer ts.Close()
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < defaultMaxPreAuth; i++ {
		dialSlowSignedBody(t, u.Host)
	}
	waitFor(t, func() bool { return preAuthRefs(rl, "127.0.0.1") == defaultMaxPreAuth })

	// The 17th is a valid signed request. It must queue, not be refused.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/_portwing/info", nil)
	signEd25519Request(t, req, nil, priv, time.Now().Unix(), freshNonce(t))
	done := make(chan int, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			done <- -1
			return
		}
		_ = resp.Body.Close()
		done <- resp.StatusCode
	}()
	waitFor(t, func() bool { return preAuthRefs(rl, "127.0.0.1") == defaultMaxPreAuth+1 })
	select {
	case code := <-done:
		t.Fatalf("17th request finished (%d) while all 16 slots were held", code)
	case <-time.After(200 * time.Millisecond):
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}

// Not parallel: mutates authBodyReadDeadline.
func TestPreAuthWaitTimeoutReturns503(t *testing.T) {
	ed, priv := setupEd25519(t)
	rl := NewRateLimiter()
	defer rl.Stop()
	orig := authBodyReadDeadline
	authBodyReadDeadline = 150 * time.Millisecond
	t.Cleanup(func() { authBodyReadDeadline = orig })

	releases := make([]func(), 0, defaultMaxPreAuth)
	for i := 0; i < defaultMaxPreAuth; i++ {
		rel, err := rl.acquirePreAuth(context.Background(), "198.51.100.5")
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, rel)
	}
	reg := newMetricsRegistry()
	h := rl.AuthMiddlewareWithEd25519(newRawTokenVerifier("correct"), ed, noAudit(t), reg, http.HandlerFunc(okHandler))

	for name, build := range map[string]func() *http.Request{
		"signature": func() *http.Request {
			req := httptest.NewRequest(http.MethodGet, "/_portwing/info", nil)
			signEd25519Request(t, req, nil, priv, time.Now().Unix(), freshNonce(t))
			return req
		},
		"token": func() *http.Request {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set(headerPortwingToken, "correct")
			return req
		},
	} {
		req := build()
		req.RemoteAddr = "198.51.100.5:1"
		rec := httptest.NewRecorder()
		start := time.Now()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "1" {
			t.Fatalf("%s: status %d Retry-After %q, want 503 and 1", name, rec.Code, rec.Header().Get("Retry-After"))
		}
		if time.Since(start) < 100*time.Millisecond {
			t.Fatalf("%s: returned before the wait deadline", name)
		}
	}
	var b strings.Builder
	reg.WritePrometheus(&b, func(v string) string { return v })
	for _, want := range []string{`code="503"} 2`, "portwing_rate_limited_total 2"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("metrics missing %q\n%s", want, b.String())
		}
	}

	// Another source, and a /64 sibling's budget, are separate.
	rel, err := rl.acquirePreAuth(context.Background(), "198.51.100.6")
	if err != nil {
		t.Fatalf("other source must not be queued: %v", err)
	}
	rel()
	for _, r := range releases {
		r()
	}
	if got := preAuthTableLen(rl); got != 0 {
		t.Fatalf("table = %d entries after release, want 0", got)
	}
}

func TestPreAuthWaitEndsWhenClientGoesAway(t *testing.T) {
	t.Parallel()
	rl := NewRateLimiter()
	defer rl.Stop()
	for i := 0; i < defaultMaxPreAuth; i++ {
		if _, err := rl.acquirePreAuth(context.Background(), "198.51.100.7"); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	req.RemoteAddr = "198.51.100.7:1"
	req.Header.Set(headerPortwingToken, "x")
	rec := httptest.NewRecorder()
	rl.AuthMiddlewareWithEd25519(newRawTokenVerifier("correct"), Ed25519Config{}, noAudit(t), nil, http.HandlerFunc(okHandler)).ServeHTTP(rec, req)
	if rec.Body.Len() != 0 || rec.Code != http.StatusOK {
		t.Fatalf("a cancelled waiter must get no response, got %d %q", rec.Code, rec.Body.String())
	}
}

func TestPreAuthIPv6SiblingsShareSlots(t *testing.T) {
	t.Parallel()
	rl := NewRateLimiter()
	defer rl.Stop()
	rl.maxPreAuth = 1
	rel, err := rl.acquirePreAuth(context.Background(), "2001:db8:1:1::1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := rl.acquirePreAuth(ctx, "2001:db8:1:1::2"); err == nil {
		t.Fatal("same /64 must share the slot")
	}
	rel()
	if got := preAuthTableLen(rl); got != 0 {
		t.Fatalf("table = %d, want 0", got)
	}
}

// The slot covers verification only: a handler that blocks must not hold it.
func TestPreAuthSlotReleasedBeforeHandlerRuns(t *testing.T) {
	t.Parallel()
	ed, priv := setupEd25519(t)
	rl := NewRateLimiter()
	defer rl.Stop()
	rl.maxPreAuth = 1
	hold := make(chan struct{})
	var inHandler atomic.Int64
	h := rl.AuthMiddlewareWithEd25519(newRawTokenVerifier("correct"), ed, noAudit(t), nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inHandler.Add(1)
		<-hold
	}))
	var wg sync.WaitGroup
	send := func(sign bool) {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodGet, "/_portwing/info", nil)
		req.RemoteAddr = "198.51.100.9:1"
		if sign {
			signEd25519Request(t, req, nil, priv, time.Now().Unix(), freshNonce(t))
		} else {
			req.Header.Set(headerPortwingToken, "correct")
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go send(i%2 == 0)
	}
	waitFor(t, func() bool { return inHandler.Load() == 4 })
	if got := preAuthTableLen(rl); got != 0 {
		t.Fatalf("slots still held while handlers run: table = %d", got)
	}
	close(hold)
	wg.Wait()
}

func TestPreAuthTableDrainsAfterLoad(t *testing.T) {
	t.Parallel()
	ed, priv := setupEd25519(t)
	ed.Nonces = auth.NewNonceLRU(5000, 60)
	rl := NewRateLimiter()
	defer rl.Stop()
	h := rl.AuthMiddlewareWithEd25519(newRawTokenVerifier("correct"), ed, noAudit(t), nil, http.HandlerFunc(okHandler))

	const n = 1000
	var wg sync.WaitGroup
	var bad atomic.Int64
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		req := httptest.NewRequest(http.MethodGet, "/_portwing/info", nil)
		req.RemoteAddr = "[2001:db8:5:5::" + string(rune('1'+i%9)) + "]:1"
		signEd25519Request(t, req, nil, priv, time.Now().Unix(), freshNonce(t))
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				bad.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if bad.Load() != 0 {
		t.Fatalf("%d of %d valid requests were refused", bad.Load(), n)
	}
	if got := preAuthTableLen(rl); got != 0 {
		t.Fatalf("pre-auth table = %d entries after load, want 0", got)
	}
}
