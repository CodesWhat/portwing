package auth

import (
	"fmt"
	"sync"
	"testing"
)

func TestNonceLRU_FreshNonceAccepted(t *testing.T) {
	t.Parallel()
	lru := NewNonceLRU(100, 60)
	if !lru.Add("abc123") {
		t.Error("first Add should return true (fresh)")
	}
}

func TestNonceLRU_ReplayDetected(t *testing.T) {
	t.Parallel()
	lru := NewNonceLRU(100, 60)
	lru.Add("abc123")
	if lru.Add("abc123") {
		t.Error("second Add of same nonce should return false (replay)")
	}
}

func TestNonceLRU_Seen(t *testing.T) {
	t.Parallel()
	lru := NewNonceLRU(100, 60)
	if lru.Seen("xyz") {
		t.Error("Seen should return false for unknown nonce")
	}
	lru.Add("xyz")
	if !lru.Seen("xyz") {
		t.Error("Seen should return true after Add")
	}
}

func TestNonceLRU_DifferentNoncesAccepted(t *testing.T) {
	t.Parallel()
	lru := NewNonceLRU(100, 60)
	for i := 0; i < 10; i++ {
		n := fmt.Sprintf("nonce%04d", i)
		if !lru.Add(n) {
			t.Errorf("Add(%q) returned false on first use", n)
		}
	}
	if lru.Len() != 10 {
		t.Errorf("expected 10 entries, got %d", lru.Len())
	}
}

// TestNonceLRU_CapacityDropsFreshEntries verifies that when the LRU is full,
// new entries are silently dropped but the function still returns true
// (fail-open: we do not deny legitimate traffic when the map is full, the
// timestamp window still limits replay to a short window).
func TestNonceLRU_CapacityBehavior(t *testing.T) {
	t.Parallel()
	const cap = 5
	lru := NewNonceLRU(cap, 60)
	for i := 0; i < cap; i++ {
		lru.Add(fmt.Sprintf("n%d", i))
	}
	if lru.Len() != cap {
		t.Fatalf("expected %d entries, got %d", cap, lru.Len())
	}
	// Adding one more: cap exceeded, returns true (fail-open).
	result := lru.Add("overflow")
	if !result {
		t.Error("overflow Add should return true (fail-open)")
	}
	// The overflow entry is NOT stored (map is full).
	if lru.Len() != cap {
		t.Errorf("len should still be %d after overflow, got %d", cap, lru.Len())
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
				lru.Add(n)
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
			wins[i] = lru.Add(nonce)
		}()
	}
	wg.Wait()

	count := 0
	for _, w := range wins {
		if w {
			count++
		}
	}
	// Exactly one goroutine should win (first Add returns true, all subsequent
	// see the nonce already and return false).
	if count != 1 {
		t.Errorf("expected exactly one goroutine to win the nonce Add race, got %d", count)
	}
}
