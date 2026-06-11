package auth

import (
	"sync"
	"time"
)

// NonceLRU is an in-memory nonce cache that provides replay protection within
// the timestamp window. It is modelled on RateLimiter in
// internal/server/middleware.go: a Mutex-protected map with a background
// cleanup goroutine.
//
// Capacity is bounded to maxSize entries. When the cap is reached, new nonces
// are silently dropped (fail-open for tracking, not for auth: the timestamp
// window alone still limits replay to 60 s).
type NonceLRU struct {
	mu      sync.Mutex
	seen    map[string]time.Time // nonce → time first seen
	maxSize int
	ttl     time.Duration // entries are safe to evict after this duration
}

// NewNonceLRU returns a NonceLRU with the given capacity and a TTL equal to
// the clock-skew window (entries can be evicted after 2× the window to give
// some extra margin). A background goroutine starts immediately.
func NewNonceLRU(maxSize int, windowSeconds int) *NonceLRU {
	if maxSize <= 0 {
		maxSize = 10000
	}
	if windowSeconds <= 0 {
		windowSeconds = 60
	}
	lru := &NonceLRU{
		seen:    make(map[string]time.Time),
		maxSize: maxSize,
		ttl:     time.Duration(windowSeconds) * time.Second,
	}
	go lru.cleanup()
	return lru
}

// Add records the nonce if it has not been seen before and the cache is not
// full. Returns true if the nonce was freshly added (not a replay), false if
// it has been seen before.
func (l *NonceLRU) Add(nonce string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, exists := l.seen[nonce]; exists {
		return false
	}

	if len(l.seen) >= l.maxSize {
		// Drop the new entry rather than evicting one. The timestamp check
		// already limits replay; this path should be extremely rare.
		return true // treat as fresh (fail-open for tracking)
	}

	l.seen[nonce] = time.Now()
	return true
}

// Seen reports whether the nonce has been recorded in the cache.
func (l *NonceLRU) Seen(nonce string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, exists := l.seen[nonce]
	return exists
}

// Len returns the current number of tracked nonces.
func (l *NonceLRU) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.seen)
}

// cleanup runs every window/2 and removes nonces whose TTL has expired.
func (l *NonceLRU) cleanup() {
	// Cleanup interval: half the TTL, or at least 10 s.
	interval := l.ttl / 2
	if interval < 10*time.Second {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		l.mu.Lock()
		cutoff := time.Now().Add(-l.ttl)
		for nonce, t := range l.seen {
			if t.Before(cutoff) {
				delete(l.seen, nonce)
			}
		}
		l.mu.Unlock()
	}
}
