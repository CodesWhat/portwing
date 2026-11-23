// SPDX-License-Identifier: MIT

package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLimiterKeyShapes(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"192.0.2.1":           "192.0.2.1",
		"::ffff:192.0.2.1":    "192.0.2.1",
		"2001:db8:1:2::1":     "2001:db8:1:2::/64",
		"2001:db8:1:2:f::9":   "2001:db8:1:2::/64",
		"fe80::1%eth0":        "fe80::/64",
		"not-an-ip":           "not-an-ip",
		"2001:db8:1:2::/64":   "2001:db8:1:2::/64",
		"2001:db8:1:3:0:0::1": "2001:db8:1:3::/64",
	} {
		if got := limiterKey(in); got != want {
			t.Errorf("limiterKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRateLimiterIPv6SharesBudgetWithinSlash64(t *testing.T) {
	t.Parallel()
	rl := NewRateLimiter()
	defer rl.Stop()

	for i := 0; i < rl.maxFails; i++ {
		rl.RecordFailure(fmt.Sprintf("2001:db8:aaaa:1::%x", i+1))
	}
	if !rl.IsRateLimited("2001:db8:aaaa:1:ffff::1") {
		t.Fatal("a fresh address in the same /64 must share the exhausted budget")
	}
	if rl.tryBeginAuth("2001:db8:aaaa:1::dead") {
		t.Fatal("tryBeginAuth must refuse an address in a locked /64")
	}
}

func TestRateLimiterIPv6DifferentSlash64DoNotShare(t *testing.T) {
	t.Parallel()
	rl := NewRateLimiter()
	defer rl.Stop()

	for i := 0; i < rl.maxFails; i++ {
		rl.RecordFailure("2001:db8:aaaa:1::1")
	}
	if rl.IsRateLimited("2001:db8:aaaa:2::1") {
		t.Fatal("a different /64 must keep its own budget")
	}
	if !rl.tryBeginAuth("2001:db8:aaaa:2::1") {
		t.Fatal("a different /64 must be admitted")
	}
}

func TestRateLimiterIPv4IsPerAddress(t *testing.T) {
	t.Parallel()
	rl := NewRateLimiter()
	defer rl.Stop()

	for i := 0; i < rl.maxFails; i++ {
		rl.RecordFailure("192.0.2.10")
	}
	if !rl.IsRateLimited("192.0.2.10") {
		t.Fatal("the failing IPv4 address must be limited")
	}
	if rl.IsRateLimited("192.0.2.11") {
		t.Fatal("a neighbouring IPv4 address must not share the budget")
	}
	if !rl.IsRateLimited("::ffff:192.0.2.10") {
		t.Fatal("the IPv4-mapped form must share the IPv4 address's budget")
	}
	if rl.IsRateLimited("::ffff:192.0.2.11") {
		t.Fatal("an IPv4-mapped neighbour must stay per address, not collapse into a /64")
	}
}

func TestRateLimiterFailsClosedAtCapacity(t *testing.T) {
	t.Parallel()
	rl := &RateLimiter{
		attempts:    map[string]*ipAttempts{"192.0.2.1": {count: 1, firstFail: time.Now()}},
		abuse:       map[string]*ipAttempts{"192.0.2.1": {count: 1, firstFail: time.Now()}},
		maxFails:    10,
		window:      time.Minute,
		maxIPs:      1,
		maxInFlight: 2,
	}

	if rl.tryBeginAuth("192.0.2.2") {
		t.Fatal("an untracked source must be refused once the table is full")
	}
	if _, ok := rl.attempts["192.0.2.2"]; ok {
		t.Fatal("a refused source must not be added to the table")
	}
	if !rl.tryBeginAuth("192.0.2.1") {
		t.Fatal("an already tracked source must keep working at capacity")
	}
	rl.finishAuth("192.0.2.1", true)

	if !rl.isAbuseRateLimited("192.0.2.2") {
		t.Fatal("an untracked source must be refused by the abuse limiter at capacity")
	}
	if rl.isAbuseRateLimited("192.0.2.1") {
		t.Fatal("a tracked source below the threshold must keep working at capacity")
	}
}

func TestAuthMiddlewareRejectsUntrackedSourceAtCapacity(t *testing.T) {
	t.Parallel()
	rl := NewRateLimiter()
	defer rl.Stop()
	rl.maxIPs = 1
	rl.RecordFailure("192.0.2.1")

	h := rl.AuthMiddlewareWithEd25519(newRawTokenVerifier("correct"), Ed25519Config{}, noAudit(t), nil, http.HandlerFunc(okHandler))
	do := func(remote string) int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = remote + ":1234"
		req.Header.Set(headerPortwingToken, "correct")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := do("192.0.2.9"); got != http.StatusTooManyRequests {
		t.Fatalf("untracked source at capacity: status %d, want 429", got)
	}
	if got := do("192.0.2.1"); got != http.StatusOK {
		t.Fatalf("tracked source at capacity: status %d, want 200", got)
	}
}
