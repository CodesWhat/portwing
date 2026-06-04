package server

import (
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// RateLimiter tracks failed authentication attempts by IP address and blocks
// IPs that exceed the failure threshold within a rolling window.
type RateLimiter struct {
	mu       sync.Mutex
	attempts map[string]*ipAttempts
	maxFails int
	window   time.Duration
	maxIPs   int
}

type ipAttempts struct {
	count     int
	firstFail time.Time
}

// NewRateLimiter returns a RateLimiter that allows 10 failures per IP within
// a one-minute window. A background goroutine prunes expired entries every
// five minutes.
func NewRateLimiter() *RateLimiter {
	rl := &RateLimiter{
		attempts: make(map[string]*ipAttempts),
		maxFails: 10,
		window:   time.Minute,
		maxIPs:   10000,
	}
	go rl.cleanup()
	return rl
}

// cleanup runs every 5 minutes and removes entries whose window has expired.
func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		for ip, a := range rl.attempts {
			if now.Sub(a.firstFail) > rl.window {
				delete(rl.attempts, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// IsRateLimited returns true if the given IP has exceeded the failure threshold
// within the current window.
func (rl *RateLimiter) IsRateLimited(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	a, ok := rl.attempts[ip]
	if !ok {
		return false
	}

	// If the window has expired, remove the entry and allow through.
	if time.Since(a.firstFail) > rl.window {
		delete(rl.attempts, ip)
		return false
	}

	return a.count >= rl.maxFails
}

// RecordFailure records a failed authentication attempt for the given IP.
// If the total number of tracked IPs exceeds the limit, the failure is silently
// dropped to prevent memory exhaustion.
func (rl *RateLimiter) RecordFailure(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if len(rl.attempts) >= rl.maxIPs {
		return
	}

	a, ok := rl.attempts[ip]
	if !ok {
		rl.attempts[ip] = &ipAttempts{count: 1, firstFail: time.Now()}
		return
	}

	// If the window has expired, start a new one.
	if time.Since(a.firstFail) > rl.window {
		a.count = 1
		a.firstFail = time.Now()
		return
	}

	a.count++
}

// AuthMiddleware returns an http.Handler that validates the authentication
// token before calling next. If token is empty, authentication is disabled
// and all requests are passed through.
//
// The middleware checks the X-Lookout-Token header first, then falls back to
// X-Dd-Agent-Secret for Drydock backwards compatibility.
func (rl *RateLimiter) AuthMiddleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No authentication configured - pass through.
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}

		clientIP := getClientIP(r)

		if rl.IsRateLimited(clientIP) {
			http.Error(w, "too many failed attempts", http.StatusTooManyRequests)
			return
		}

		// Check X-Lookout-Token first, then fall back to X-Dd-Agent-Secret.
		provided := r.Header.Get("X-Lookout-Token")
		if provided == "" {
			provided = r.Header.Get("X-Dd-Agent-Secret")
		}

		if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			rl.RecordFailure(clientIP)
			slog.Warn("authentication failed", "ip", clientIP)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// RecoveryMiddleware catches panics in downstream handlers, logs the stack
// trace, and returns a 500 Internal Server Error.
func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				stack := debug.Stack()
				slog.Error("panic recovered",
					"error", fmt.Sprintf("%v", err),
					"stack", string(stack),
					"method", r.Method,
					"path", r.URL.Path,
				)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// getClientIP extracts the client IP address from the request, checking
// X-Forwarded-For and X-Real-IP headers before falling back to RemoteAddr.
func getClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// X-Forwarded-For can contain multiple IPs; the first is the client.
		if idx := strings.IndexByte(xff, ','); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}

	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
