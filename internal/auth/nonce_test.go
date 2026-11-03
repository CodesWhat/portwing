// SPDX-License-Identifier: MIT

package auth

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNonceLRU_FreshNonceAccepted(t *testing.T) {
	t.Parallel()
	lru := NewNonceLRU(100, 60)
	if err := lru.Add("abc123"); err != nil {
		t.Error("first Add should succeed")
	}
}

func TestNonceLRU_ReplayDetected(t *testing.T) {
	t.Parallel()
	lru := NewNonceLRU(100, 60)
	_ = lru.Add("abc123")
	if err := lru.Add("abc123"); err == nil {
		t.Error("second Add of same nonce should fail (replay)")
	}
}

func TestNonceLRU_Seen(t *testing.T) {
	t.Parallel()
	lru := NewNonceLRU(100, 60)
	if lru.Seen("xyz") {
		t.Error("Seen should return false for unknown nonce")
	}
	_ = lru.Add("xyz")
	if !lru.Seen("xyz") {
		t.Error("Seen should return true after Add")
	}
}

func TestNonceLRU_DifferentNoncesAccepted(t *testing.T) {
	t.Parallel()
	lru := NewNonceLRU(100, 60)
	for i := 0; i < 10; i++ {
		n := fmt.Sprintf("nonce%04d", i)
		if err := lru.Add(n); err != nil {
			t.Errorf("Add(%q) failed on first use", n)
		}
	}
	if lru.Len() != 10 {
		t.Errorf("expected 10 entries, got %d", lru.Len())
	}
}

// fillNonceLRU adds n nonces and fails the test if any is refused.
func fillNonceLRU(t *testing.T, lru *NonceLRU, n int) []string {
	t.Helper()
	added := make([]string, n)
	for i := 0; i < n; i++ {
		added[i] = fmt.Sprintf("n%d", i)
		if err := lru.Add(added[i]); err != nil {
			t.Fatalf("Add(%q) refused while filling to %d", added[i], n)
		}
	}
	return added
}

// backdateNonces rewrites the recorded time of every tracked nonce so it is
// older than the TTL, without waiting one out.
func backdateNonces(t *testing.T, lru *NonceLRU) {
	t.Helper()
	lru.mu.Lock()
	defer lru.mu.Unlock()
	stale := time.Now().Add(-2 * lru.ttl)
	for nonce := range lru.seen {
		lru.seen[nonce] = stale
	}
}

// TestNonceLRU_AtCapacityEvictsExpiredThenRecords covers the normal way a
// full cache makes room: everything in it is past its TTL and can no longer
// be replayed, so it is dropped and the new nonce is recorded like any other.
// The point is that it IS recorded — the old code returned true here without
// storing anything, which left that request replayable.
func TestNonceLRU_AtCapacityEvictsExpiredThenRecords(t *testing.T) {
	t.Parallel()
	const capacity = 5
	lru := NewNonceLRU(capacity, 60)
	t.Cleanup(lru.Close)

	fillNonceLRU(t, lru, capacity)
	backdateNonces(t, lru)

	if err := lru.Add("overflow"); err != nil {
		t.Fatal("Add at capacity refused a nonce with only expired entries to evict")
	}
	if !lru.Seen("overflow") {
		t.Fatal("Add succeeded without recording the nonce: the request stays replayable")
	}
	if err := lru.Add("overflow"); err == nil {
		t.Fatal("replay of the nonce accepted at capacity was not refused")
	}
}

// TestNonceLRU_AtCapacityRejectsWhenNothingExpired covers the other branch:
// every tracked nonce is still inside its own replay window, so there is
// nothing safe to evict. Rejecting is the only answer that does not open a
// replay hole at one end or the other.
func TestNonceLRU_AtCapacityRejectsWhenNothingExpired(t *testing.T) {
	t.Parallel()
	const capacity = 5
	lru := NewNonceLRU(capacity, 60)
	t.Cleanup(lru.Close)

	fillNonceLRU(t, lru, capacity)

	if err := lru.Add("overflow"); !errors.Is(err, ErrNonceCapacity) {
		t.Fatalf("Add at capacity = %v, want ErrNonceCapacity", err)
	}
	if lru.Seen("overflow") {
		t.Fatal("rejected nonce was recorded anyway")
	}
	if lru.Len() != capacity {
		t.Fatalf("len = %d after a rejected Add, want %d", lru.Len(), capacity)
	}
}

func TestNonceExpiryRetainsFreshRequests(t *testing.T) {
	t.Parallel()
	lru := NewNonceLRU(3, 60)
	t.Cleanup(lru.Close)
	nonces := fillNonceLRU(t, lru, 3)
	lru.mu.Lock()
	for _, nonce := range nonces[:2] {
		lru.seen[nonce] = time.Now().Add(-2 * lru.ttl)
	}
	lru.mu.Unlock()
	if err := lru.Add("replacement"); err != nil {
		t.Fatalf("expired entries did not free capacity: %v", err)
	}
	if err := lru.Add(nonces[2]); !errors.Is(err, ErrNonceReplay) {
		t.Fatalf("fresh request replay = %v", err)
	}
	if lru.Seen(nonces[0]) || lru.Seen(nonces[1]) {
		t.Fatal("expired nonces were retained")
	}
	if err := lru.Add("last-slot"); err != nil {
		t.Fatal(err)
	}
	if err := lru.Add("overflow"); !errors.Is(err, ErrNonceCapacity) {
		t.Fatalf("full cache = %v", err)
	}
}

// TestNonceLRU_ConcurrentAccess exercises concurrent Add/Seen without races.
func TestNonceLRU_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	lru := NewNonceLRU(10000, 60)
	const goroutines = 20
	const perGoroutine = 500

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				n := fmt.Sprintf("g%d-n%d", g, i)
				_ = lru.Add(n)
				lru.Seen(n)
			}
		}()
	}
	wg.Wait()
}

// TestNonceLRU_ReplayConcurrent verifies replay detection under concurrency:
// exactly one of many concurrent goroutines trying to Add the same nonce wins.
func TestNonceLRU_ReplayConcurrent(t *testing.T) {
	t.Parallel()
	lru := NewNonceLRU(10000, 60)
	const goroutines = 50
	const nonce = "shared-nonce-xyz"

	var wg sync.WaitGroup
	wins := make([]bool, goroutines)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			wins[i] = lru.Add(nonce) == nil
		}()
	}
	wg.Wait()

	count := 0
	for _, w := range wins {
		if w {
			count++
		}
	}
	// Exactly one goroutine should win (first Add succeeds, all subsequent
	// see the nonce already and fail).
	if count != 1 {
		t.Errorf("expected exactly one goroutine to win the nonce Add race, got %d", count)
	}
}

func TestNonceLRU_EvictsOnWallClock(t *testing.T) {
	t.Parallel()
	lru := NewNonceLRU(10, 60)
	t.Cleanup(lru.Close)
	base := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	cur := base
	lru.now = func() time.Time { return cur }

	if err := lru.Add("n1"); err != nil {
		t.Fatal(err)
	}
	// After a backward wall-clock step the signed timestamp can still be valid
	// for up to 2W, so the nonce must survive until then.
	cur = base.Add(-30 * time.Second)
	lru.evictExpired()
	if !lru.Seen("n1") {
		t.Fatal("nonce evicted after a backward wall-clock step")
	}
	cur = base.Add(2*time.Minute - time.Second)
	lru.evictExpired()
	if !lru.Seen("n1") {
		t.Fatal("nonce evicted before 2W elapsed")
	}
	cur = base.Add(2*time.Minute + time.Second)
	lru.evictExpired()
	if lru.Seen("n1") {
		t.Fatal("nonce kept past 2W")
	}
}

func TestNonceLRU_RecordsMonotonicReading(t *testing.T) {
	t.Parallel()
	lru := NewNonceLRU(10, 60)
	t.Cleanup(lru.Close)
	if err := lru.Add("n1"); err != nil {
		t.Fatal(err)
	}
	lru.mu.Lock()
	got := lru.seen["n1"]
	lru.mu.Unlock()
	if !strings.Contains(got.String(), "m=") {
		t.Fatalf("recorded time %q lost its monotonic reading", got)
	}
	lru.now = nil
	if !strings.Contains(lru.clock().String(), "m=") {
		t.Fatal("fallback clock lacks a monotonic reading")
	}
}

// A forward wall step past the TTL must not evict while monotonic elapsed is
// still under it, or a step back would let a valid signature replay.
func TestNonceLRU_ForwardWallStepKeepsNonce(t *testing.T) {
	t.Parallel()
	lru := NewNonceLRU(10, 60)
	t.Cleanup(lru.Close)
	base := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	cur := base
	lru.now = func() time.Time { return cur }
	mono := time.Second
	lru.elapsed = func(_, _ time.Time) time.Duration { return mono }

	if err := lru.Add("n1"); err != nil {
		t.Fatal(err)
	}
	cur = base.Add(time.Hour)
	lru.evictExpired()
	if !lru.Seen("n1") {
		t.Fatal("nonce evicted by a forward wall step with little monotonic elapsed")
	}
	cur = base
	if err := lru.Add("n1"); !errors.Is(err, ErrNonceReplay) {
		t.Fatalf("replay after wall step back = %v", err)
	}
	// Monotonic past the TTL but wall time not: still kept.
	mono = 3 * time.Minute
	lru.evictExpired()
	if !lru.Seen("n1") {
		t.Fatal("nonce evicted on monotonic expiry alone")
	}
	// Both clocks past the TTL: evicted.
	cur = base.Add(time.Hour)
	lru.evictExpired()
	if lru.Seen("n1") {
		t.Fatal("nonce kept after both clocks passed the TTL")
	}
}

func TestNonceLRU_ExpiredOnBothClocksEvicts(t *testing.T) {
	t.Parallel()
	lru := NewNonceLRU(10, 60)
	t.Cleanup(lru.Close)
	if err := lru.Add("n1"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(2*lru.ttl + time.Second)
	lru.mu.Lock()
	lru.evictExpiredLocked(now)
	lru.mu.Unlock()
	if lru.Seen("n1") {
		t.Fatal("nonce past TTL on both clocks was kept")
	}
}

func BenchmarkNonceCapacityRefusal(b *testing.B) {
	for _, size := range []int{100, 10000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			lru := NewNonceLRU(size, 60)
			b.Cleanup(lru.Close)
			for i := range size {
				_ = lru.Add(fmt.Sprint(i))
			}
			b.ResetTimer()
			for range b.N {
				_ = lru.Add("overflow")
			}
		})
	}
}

// After a backward wall step, a later nonce can expire before an earlier one.
// Eviction must reclaim it rather than stop at the first unexpired entry.
func TestNonceLRU_EvictsExpiredBehindUnexpired(t *testing.T) {
	t.Parallel()
	lru := NewNonceLRU(2, 60)
	t.Cleanup(lru.Close)
	base := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	cur := base
	lru.now = func() time.Time { return cur }
	lru.elapsed = func(seen, now time.Time) time.Duration { return now.Sub(seen) }

	if err := lru.Add("first"); err != nil {
		t.Fatal(err)
	}
	cur = base.Add(-10 * time.Minute) // wall clock steps back
	if err := lru.Add("second"); err != nil {
		t.Fatal(err)
	}
	// Past the TTL for "second" but not for "first".
	cur = base.Add(-10*time.Minute + 3*time.Minute)
	if err := lru.Add("third"); err != nil {
		t.Fatalf("at capacity with an expired entry behind an unexpired one: %v", err)
	}
	if lru.Seen("second") {
		t.Fatal("expired nonce behind an unexpired one was not evicted")
	}
	if !lru.Seen("first") || !lru.Seen("third") {
		t.Fatal("unexpired nonces were evicted")
	}
}
