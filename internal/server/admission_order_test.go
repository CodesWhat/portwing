// SPDX-License-Identifier: MIT

package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/auth"
)

// TestAuthMiddlewareConcurrentValidSignedRequestsGetNo429 is the regression for
// the sporadic 429s on valid traffic: the per-IP in-flight slot (2) was taken
// before credentials were checked, so any third request from one address that
// overlapped two others was refused with "too many failed attempts" despite a
// good signature.
func TestAuthMiddlewareConcurrentValidSignedRequestsGetNo429(t *testing.T) {
	t.Parallel()
	ed, priv := setupEd25519(t)
	ed.Nonces = auth.NewNonceLRU(5000, 60)
	rl := NewRateLimiter()
	defer rl.Stop()
	h := rl.AuthMiddlewareWithEd25519(nil, ed, noAudit(t), nil, http.HandlerFunc(okHandler))

	const requests = 1000
	var wg sync.WaitGroup
	var tooMany, other atomic.Int64
	start := make(chan struct{})
	for i := 0; i < requests; i++ {
		req := httptest.NewRequest(http.MethodGet, "/_portwing/info", nil)
		req.RemoteAddr = "203.0.113.7:4000"
		signEd25519Request(t, req, nil, priv, time.Now().Unix(), freshNonce(t))
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			switch rec.Code {
			case http.StatusOK:
			case http.StatusTooManyRequests:
				tooMany.Add(1)
			default:
				other.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := tooMany.Load(); got != 0 {
		t.Fatalf("%d of %d valid signed requests from one IP got 429", got, requests)
	}
	if got := other.Load(); got != 0 {
		t.Fatalf("%d of %d valid signed requests were refused", got, requests)
	}
}

func TestAuthMiddlewareConcurrentValidTokenRequestsGetNo429(t *testing.T) {
	t.Parallel()
	rl := NewRateLimiter()
	defer rl.Stop()
	h := rl.AuthMiddlewareWithEd25519(newRawTokenVerifier("correct"), Ed25519Config{}, noAudit(t), nil, http.HandlerFunc(okHandler))

	const requests = 1000
	var wg sync.WaitGroup
	var refused atomic.Int64
	start := make(chan struct{})
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = "203.0.113.8:4000"
			req.Header.Set(headerPortwingToken, "correct")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				refused.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := refused.Load(); got != 0 {
		t.Fatalf("%d of %d valid token requests from one IP were refused", got, requests)
	}
}

func lockOut(rl *RateLimiter, ip string) {
	for i := 0; i < rl.maxFails; i++ {
		rl.RecordFailure(ip)
	}
}

// TestAuthMiddlewareLockedOutSourceStillGets429ForBadCredentials and
// TestAuthMiddlewareLockedOutSourceValidCredentialsSucceed pin the decided
// shared-IP behaviour: lockout answers bad credentials, never valid ones.
func TestAuthMiddlewareLockedOutSourceStillGets429ForBadCredentials(t *testing.T) {
	t.Parallel()
	ed, _ := setupEd25519(t)
	_, otherPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	const ip = "203.0.113.9"

	cases := map[string]func(*testing.T, *RateLimiter) *httptest.ResponseRecorder{
		"bad token": func(t *testing.T, rl *RateLimiter) *httptest.ResponseRecorder {
			h := rl.AuthMiddlewareWithEd25519(newRawTokenVerifier("correct"), Ed25519Config{}, noAudit(t), nil, http.HandlerFunc(okHandler))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = ip + ":1"
			req.Header.Set(headerPortwingToken, "wrong")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			return rec
		},
		"bad signature": func(t *testing.T, rl *RateLimiter) *httptest.ResponseRecorder {
			h := rl.AuthMiddlewareWithEd25519(nil, ed, noAudit(t), nil, http.HandlerFunc(okHandler))
			req := httptest.NewRequest(http.MethodGet, "/_portwing/info", nil)
			req.RemoteAddr = ip + ":1"
			signEd25519Request(t, req, nil, otherPriv, time.Now().Unix(), freshNonce(t))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			return rec
		},
		"no credentials": func(t *testing.T, rl *RateLimiter) *httptest.ResponseRecorder {
			h := rl.AuthMiddlewareWithEd25519(nil, ed, noAudit(t), nil, http.HandlerFunc(okHandler))
			req := httptest.NewRequest(http.MethodGet, "/_portwing/info", nil)
			req.RemoteAddr = ip + ":1"
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			return rec
		},
	}
	for name, send := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rl := NewRateLimiter()
			defer rl.Stop()
			lockOut(rl, ip)
			if rec := send(t, rl); rec.Code != http.StatusTooManyRequests {
				t.Fatalf("locked-out source with %s: status %d, want 429", name, rec.Code)
			}
		})
	}
}

func TestAuthMiddlewareLockedOutSourceValidCredentialsSucceed(t *testing.T) {
	t.Parallel()
	ed, priv := setupEd25519(t)
	const ip = "203.0.113.10"

	t.Run("signature", func(t *testing.T) {
		t.Parallel()
		rl := NewRateLimiter()
		defer rl.Stop()
		lockOut(rl, ip)
		h := rl.AuthMiddlewareWithEd25519(nil, ed, noAudit(t), nil, http.HandlerFunc(okHandler))
		req := httptest.NewRequest(http.MethodGet, "/_portwing/info", nil)
		req.RemoteAddr = ip + ":1"
		signEd25519Request(t, req, nil, priv, time.Now().Unix(), freshNonce(t))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("valid signature from a locked-out source: status %d, want 200", rec.Code)
		}
	})
	t.Run("token", func(t *testing.T) {
		t.Parallel()
		rl := NewRateLimiter()
		defer rl.Stop()
		lockOut(rl, ip)
		h := rl.AuthMiddlewareWithEd25519(newRawTokenVerifier("correct"), Ed25519Config{}, noAudit(t), nil, http.HandlerFunc(okHandler))
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = ip + ":1"
		req.Header.Set(headerPortwingToken, "correct")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("valid token from a locked-out source: status %d, want 200", rec.Code)
		}
	})
}

func TestAuthMiddlewareBadCredentialsLockOutAfterBudget(t *testing.T) {
	t.Parallel()
	rl := NewRateLimiter()
	defer rl.Stop()
	h := rl.AuthMiddlewareWithEd25519(newRawTokenVerifier("correct"), Ed25519Config{}, noAudit(t), nil, http.HandlerFunc(okHandler))
	send := func(token string) int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "203.0.113.11:1"
		req.Header.Set(headerPortwingToken, token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := 0; i < rl.maxFails; i++ {
		if got := send("wrong"); got != http.StatusUnauthorized {
			t.Fatalf("failure %d: status %d, want 401", i+1, got)
		}
	}
	if got := send("wrong"); got != http.StatusTooManyRequests {
		t.Fatalf("failure past the budget: status %d, want 429", got)
	}
	if got := send("correct"); got != http.StatusOK {
		t.Fatalf("valid token after lockout: status %d, want 200", got)
	}
}

func TestRecordAuthFailureWindowAndCapacity(t *testing.T) {
	t.Parallel()
	rl := NewRateLimiter()
	defer rl.Stop()
	rl.maxIPs = 1

	for i := 0; i < rl.maxFails; i++ {
		if rl.recordAuthFailure("198.51.100.1") {
			t.Fatalf("failure %d must not be locked out yet", i+1)
		}
	}
	if !rl.recordAuthFailure("198.51.100.1") {
		t.Fatal("a source at the failure limit must be reported locked out")
	}
	if got := rl.attempts["198.51.100.1"].count; got != rl.maxFails {
		t.Fatalf("a locked-out failure must not be counted again: count = %d", got)
	}
	if !rl.recordAuthFailure("198.51.100.2") {
		t.Fatal("an untracked source must be reported locked out at capacity")
	}

	rl.attempts["198.51.100.1"].firstFail = time.Now().Add(-2 * rl.window)
	if rl.recordAuthFailure("198.51.100.1") {
		t.Fatal("an expired window must start a fresh budget")
	}
	if got := rl.attempts["198.51.100.1"].count; got != 1 {
		t.Fatalf("count after window reset = %d, want 1", got)
	}
	rl.attempts["198.51.100.1"].count = 0
	rl.attempts["198.51.100.1"].firstFail = time.Time{}
	if rl.recordAuthFailure("198.51.100.1") {
		t.Fatal("a cleared entry must start a fresh budget")
	}
}

func signedComposeRequest(t *testing.T, priv ed25519.PrivateKey, size int) *http.Request {
	t.Helper()
	body := bytes.Repeat([]byte("a"), size)
	req := httptest.NewRequest(http.MethodPost, composePath, bytes.NewReader(body))
	req.RemoteAddr = "203.0.113.20:1"
	signEd25519Request(t, req, body, priv, time.Now().Unix(), freshNonce(t))
	return req
}

func drainHandler(got *atomic.Int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := io.Copy(io.Discard, r.Body)
		got.Store(n)
		w.WriteHeader(http.StatusOK)
	})
}

func TestEd25519ComposeBodyCap(t *testing.T) {
	t.Parallel()
	ed, priv := setupEd25519(t)
	rl := NewRateLimiter()
	defer rl.Stop()
	var got atomic.Int64
	h := rl.AuthMiddlewareWithEd25519(nil, ed, noAudit(t), nil, drainHandler(&got))

	t.Run("20 MiB compose body is accepted", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, signedComposeRequest(t, priv, 20<<20))
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d, want 200", rec.Code)
		}
		if got.Load() != 20<<20 {
			t.Fatalf("handler saw %d bytes, want %d", got.Load(), 20<<20)
		}
	})
	t.Run("40 MiB compose body is rejected", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, signedComposeRequest(t, priv, 40<<20))
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status %d, want 413", rec.Code)
		}
	})
	t.Run("small compose body is unaffected", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, signedComposeRequest(t, priv, 1024))
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d, want 200", rec.Code)
		}
	})
	t.Run("other routes keep the 1 MiB cap", func(t *testing.T) {
		body := bytes.Repeat([]byte("a"), 2<<20)
		req := httptest.NewRequest(http.MethodPost, "/_portwing/exec", bytes.NewReader(body))
		req.RemoteAddr = "203.0.113.21:1"
		signEd25519Request(t, req, body, priv, time.Now().Unix(), freshNonce(t))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status %d, want 413", rec.Code)
		}
	})
	t.Run("GET to the compose path keeps the 1 MiB cap", func(t *testing.T) {
		body := bytes.Repeat([]byte("a"), 2<<20)
		req := httptest.NewRequest(http.MethodGet, composePath, bytes.NewReader(body))
		req.RemoteAddr = "203.0.113.22:1"
		signEd25519Request(t, req, body, priv, time.Now().Unix(), freshNonce(t))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status %d, want 413", rec.Code)
		}
	})
}

// Not parallel: it fills the package-level largeSignedBodySlots.
func TestEd25519ComposeLargeBodiesAreBoundedAgentWide(t *testing.T) {
	ed, priv := setupEd25519(t)
	rl := NewRateLimiter()
	defer rl.Stop()
	var got atomic.Int64
	h := rl.AuthMiddlewareWithEd25519(nil, ed, noAudit(t), nil, drainHandler(&got))

	for i := 0; i < maxLargeSignedBodies; i++ {
		largeSignedBodySlots <- struct{}{}
	}
	t.Cleanup(func() {
		for len(largeSignedBodySlots) > 0 {
			<-largeSignedBodySlots
		}
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signedComposeRequest(t, priv, 2<<20))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("large compose body with every slot taken: status %d, want 503", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("503 must carry Retry-After")
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, signedComposeRequest(t, priv, 1024))
	if rec.Code != http.StatusOK {
		t.Fatalf("small compose body must not need a large-body slot: status %d, want 200", rec.Code)
	}
}

// countingArgon2Verifier returns a cold verifier whose derivation is a counter
// plus a constant-time-irrelevant string compare, so tests don't pay for Argon2.
func countingArgon2Verifier(valid string) (*argon2Verifier, *atomic.Int64) {
	var derivations atomic.Int64
	v := newArgon2Verifier(&Argon2idParams{})
	v.slowVerify = func(token string) bool {
		derivations.Add(1)
		return token == valid
	}
	return v, &derivations
}

func tokenRequest(ip, token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = ip + ":1"
	req.Header.Set(headerPortwingToken, token)
	return req
}

func TestAuthMiddlewareLockedOutSourceSkipsColdDerivation(t *testing.T) {
	t.Parallel()
	rl := NewRateLimiter()
	defer rl.Stop()
	v, derivations := countingArgon2Verifier("correct")
	h := rl.AuthMiddlewareWithEd25519(v, Ed25519Config{}, noAudit(t), nil, http.HandlerFunc(okHandler))
	const locked = "203.0.113.30"
	lockOut(rl, locked)

	for _, token := range []string{"correct", "wrong"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, tokenRequest(locked, token))
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("locked-out source, cold hash, token %q: status %d, want 429", token, rec.Code)
		}
	}
	if got := derivations.Load(); got != 0 {
		t.Fatalf("locked-out source ran %d cold derivations, want 0", got)
	}

	// A source that isn't locked out still gets verify-first behaviour and
	// primes the cache.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, tokenRequest("203.0.113.31", "correct"))
	if rec.Code != http.StatusOK {
		t.Fatalf("clean source with valid token: status %d, want 200", rec.Code)
	}
	if got := derivations.Load(); got != 1 {
		t.Fatalf("clean source ran %d derivations, want 1", got)
	}

	// With the derivation cached the check is cheap, so the locked-out source
	// is verified: valid succeeds, invalid stays 429.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, tokenRequest(locked, "correct"))
	if rec.Code != http.StatusOK {
		t.Fatalf("locked-out source with cached hash and valid token: status %d, want 200", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, tokenRequest(locked, "wrong"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("locked-out source with cached hash and bad token: status %d, want 429", rec.Code)
	}
	if got := derivations.Load(); got != 1 {
		t.Fatalf("derivations after cache primed = %d, want 1", got)
	}
}

func TestAuthMiddlewareLockedOutSourceEd25519SucceedsWithColdTokenHash(t *testing.T) {
	t.Parallel()
	ed, priv := setupEd25519(t)
	rl := NewRateLimiter()
	defer rl.Stop()
	v, derivations := countingArgon2Verifier("correct")
	h := rl.AuthMiddlewareWithEd25519(v, ed, noAudit(t), nil, http.HandlerFunc(okHandler))
	const ip = "203.0.113.32"
	lockOut(rl, ip)

	req := httptest.NewRequest(http.MethodGet, "/_portwing/info", nil)
	req.RemoteAddr = ip + ":1"
	signEd25519Request(t, req, nil, priv, time.Now().Unix(), freshNonce(t))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid signature from a locked-out source: status %d, want 200", rec.Code)
	}
	if got := derivations.Load(); got != 0 {
		t.Fatalf("signature path ran %d derivations, want 0", got)
	}
}

func TestRateLimiterLockedOut(t *testing.T) {
	t.Parallel()
	rl := NewRateLimiter()
	defer rl.Stop()
	if rl.lockedOut("198.51.100.1") {
		t.Fatal("unknown source must not be locked out")
	}
	lockOut(rl, "198.51.100.1")
	if !rl.lockedOut("198.51.100.1") {
		t.Fatal("source at the failure limit must be locked out")
	}
	rl.mu.Lock()
	rl.attempts[limiterKey("198.51.100.1")].firstFail = time.Now().Add(-2 * rl.window)
	rl.mu.Unlock()
	if rl.lockedOut("198.51.100.1") {
		t.Fatal("expired window must not be locked out")
	}
	rl.maxIPs = 1
	if !rl.lockedOut("198.51.100.2") {
		t.Fatal("untrackable source (table full) must count as locked out")
	}
}

// Not parallel: it uses the package-level largeSignedBodySlots.
func TestEd25519ComposeLargeSlotHeldUntilHandlerReturns(t *testing.T) {
	ed, priv := setupEd25519(t)
	rl := NewRateLimiter()
	defer rl.Stop()
	t.Cleanup(func() {
		for len(largeSignedBodySlots) > 0 {
			<-largeSignedBodySlots
		}
	})

	entered := make(chan struct{}, maxLargeSignedBodies+1)
	release := make(chan struct{}, maxLargeSignedBodies+1)
	h := rl.AuthMiddlewareWithEd25519(nil, ed, noAudit(t), nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	}))

	var wg sync.WaitGroup
	codes := make(chan int, maxLargeSignedBodies)
	for i := 0; i < maxLargeSignedBodies; i++ {
		req := signedComposeRequest(t, priv, 20<<20)
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			codes <- rec.Code
		}()
	}
	for i := 0; i < maxLargeSignedBodies; i++ {
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatalf("only %d handlers entered", i)
		}
	}

	// Every handler is past verification and still running, so the buffers
	// are still resident and a fifth large body must be turned away.
	blocked := make(chan int, 1)
	go func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, signedComposeRequest(t, priv, 20<<20))
		blocked <- rec.Code
	}()
	select {
	case code := <-blocked:
		if code != http.StatusServiceUnavailable {
			t.Fatalf("fifth large body while four handlers run: status %d, want 503", code)
		}
	case <-time.After(10 * time.Second):
		for i := 0; i <= maxLargeSignedBodies; i++ {
			release <- struct{}{}
		}
		t.Fatal("fifth large body was admitted while four handlers still held their buffers")
	}

	release <- struct{}{}
	deadline := time.After(10 * time.Second)
	for len(largeSignedBodySlots) == maxLargeSignedBodies {
		select {
		case <-deadline:
			t.Fatal("slot not freed after a handler returned")
		default:
			time.Sleep(time.Millisecond)
		}
	}

	fifth := make(chan int, 1)
	go func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, signedComposeRequest(t, priv, 20<<20))
		fifth <- rec.Code
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("fifth request did not proceed after a slot was released")
	}
	for i := 0; i < maxLargeSignedBodies; i++ {
		release <- struct{}{}
	}
	wg.Wait()
	if code := <-fifth; code != http.StatusOK {
		t.Fatalf("fifth request after release: status %d, want 200", code)
	}
}

// Requests queued behind the pre-auth slots were checked before the wait. If
// the source is locked out while they wait, they must be refused on admission
// instead of each running a cold derivation.
func TestAuthMiddlewareRechecksLockoutAfterPreAuthAdmission(t *testing.T) {
	rl := NewRateLimiter()
	defer rl.Stop()
	v, derivations := countingArgon2Verifier("correct")
	h := rl.AuthMiddlewareWithEd25519(v, Ed25519Config{}, noAudit(t), nil, http.HandlerFunc(okHandler))
	const ip = "203.0.113.40"

	var releases []func()
	for i := 0; i < defaultMaxPreAuth; i++ {
		release, err := rl.acquirePreAuth(context.Background(), ip)
		if err != nil {
			t.Fatalf("acquire slot %d: %v", i, err)
		}
		releases = append(releases, release)
	}

	const queued = 4
	codes := make(chan int, queued)
	for i := 0; i < queued; i++ {
		go func() {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, tokenRequest(ip, "wrong"))
			codes <- rec.Code
		}()
	}
	waitFor(t, func() bool { return preAuthRefs(rl, ip) == defaultMaxPreAuth+queued })

	lockOut(rl, ip)
	for _, release := range releases {
		release()
	}
	for i := 0; i < queued; i++ {
		select {
		case code := <-codes:
			if code != http.StatusTooManyRequests {
				t.Errorf("queued request after lockout: status %d, want 429", code)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("queued request never finished")
		}
	}
	if got := derivations.Load(); got != 0 {
		t.Fatalf("queued requests ran %d cold derivations after lockout, want 0", got)
	}
	waitFor(t, func() bool { return preAuthRefs(rl, ip) == 0 })
}
