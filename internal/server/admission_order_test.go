// SPDX-License-Identifier: MIT

package server

import (
	"bytes"
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
