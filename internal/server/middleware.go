package server

import (
	"bufio"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/codeswhat/lookout/internal/audit"
)

// tokenVerifier is the interface used by AuthMiddleware to verify a presented
// token. It abstracts over plain-text and Argon2id verification.
type tokenVerifier interface {
	Verify(token string) bool
}

// rawTokenVerifier performs timing-safe comparison against a plain-text token.
type rawTokenVerifier struct {
	token string
}

func (v *rawTokenVerifier) Verify(token string) bool {
	return subtle.ConstantTimeCompare([]byte(token), []byte(v.token)) == 1
}

// RateLimiter tracks failed authentication attempts by IP address and blocks
// IPs that exceed the failure threshold within a rolling window.
type RateLimiter struct {
	mu       sync.Mutex
	attempts map[string]*ipAttempts
	maxFails int
	window   time.Duration
	maxIPs   int

	trustedProxies []*net.IPNet
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
// token before calling next. If verifier is nil, authentication is disabled
// and all requests are passed through.
//
// The middleware checks Authorization: Bearer first, then X-Lookout-Token,
// then X-Dd-Agent-Secret for Drydock backwards compatibility.
//
// Rate limiting is applied before verification, so failed attempts are always
// counted regardless of whether the verifier uses a raw token or Argon2id.
//
// auditor receives auth_failure and rate_limited events, and an api_request
// event (with outcome and duration) for every request that reaches next.
func (rl *RateLimiter) AuthMiddleware(verifier tokenVerifier, auditor *audit.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// No authentication configured - pass through.
		if verifier == nil {
			rw := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
			next.ServeHTTP(rw, r)
			auditor.APIRequest("", r.Method, r.URL.Path, audit.OutcomeAllowed, rw.code, ms(start))
			return
		}

		clientIP := rl.clientIP(r)

		// Rate-limit check happens BEFORE verification. This ensures that failed
		// attempts accumulate in the limiter even for expensive Argon2id paths,
		// and that blocked IPs never reach the verifier.
		if rl.IsRateLimited(clientIP) {
			auditor.RateLimited(clientIP, r.Method, r.URL.Path)
			http.Error(w, "too many failed attempts", http.StatusTooManyRequests)
			return
		}

		// Check Authorization: Bearer first, then X-Lookout-Token, then the
		// Drydock-compatible X-Dd-Agent-Secret.
		provided := bearerToken(r)
		if provided == "" {
			provided = r.Header.Get("X-Lookout-Token")
		}
		if provided == "" {
			provided = r.Header.Get("X-Dd-Agent-Secret")
		}

		if !verifier.Verify(provided) {
			rl.RecordFailure(clientIP)
			slog.Warn("authentication failed", "ip", clientIP)
			auditor.AuthFailure(clientIP, r.Method, r.URL.Path)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		rw := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rw, r)
		auditor.APIRequest(clientIP, r.Method, r.URL.Path, audit.OutcomeAllowed, rw.code, ms(start))
	})
}

// statusRecorder wraps ResponseWriter to capture the status code. It must
// forward Flush and Hijack so SSE streaming and Docker exec/attach hijacking
// keep working through the middleware chain.
type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.code = code
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *statusRecorder) Flush() {
	if f, ok := sr.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (sr *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := sr.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// Unwrap supports http.ResponseController.
func (sr *statusRecorder) Unwrap() http.ResponseWriter {
	return sr.ResponseWriter
}

// ms returns elapsed milliseconds since start as a float64.
func ms(start time.Time) float64 {
	return float64(time.Since(start).Nanoseconds()) / 1e6
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

// bearerToken extracts a token from the Authorization header if it uses the
// Bearer scheme.
func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if len(auth) > 7 && strings.EqualFold(auth[:7], "Bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return ""
}

// SetTrustedProxies configures the CIDR ranges whose forwarding headers are
// trusted when extracting the client IP. It must be called before the server
// starts handling requests. With no trusted proxies (the default),
// X-Forwarded-For and X-Real-IP are ignored so spoofed headers cannot evade
// rate limiting.
func (rl *RateLimiter) SetTrustedProxies(nets []*net.IPNet) {
	rl.trustedProxies = nets
}

// ParseTrustedProxies parses CIDR strings into networks for
// SetTrustedProxies. Bare IPs are treated as /32 (or /128 for IPv6).
func ParseTrustedProxies(entries []string) ([]*net.IPNet, error) {
	nets := make([]*net.IPNet, 0, len(entries))
	for _, e := range entries {
		s := strings.TrimSpace(e)
		if s == "" {
			continue
		}
		if !strings.Contains(s, "/") {
			if ip := net.ParseIP(s); ip != nil {
				if ip.To4() != nil {
					s += "/32"
				} else {
					s += "/128"
				}
			}
		}
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy %q: %w", e, err)
		}
		nets = append(nets, n)
	}
	return nets, nil
}

// clientIP extracts the client IP for rate-limiting purposes. Forwarding
// headers are only consulted when the direct peer is a trusted proxy; the
// X-Forwarded-For chain is then walked right to left and the first hop that
// is not itself a trusted proxy wins.
func (rl *RateLimiter) clientIP(r *http.Request) string {
	remote := r.RemoteAddr
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}

	remoteIP := net.ParseIP(remote)
	if remoteIP == nil || !ipInNets(remoteIP, rl.trustedProxies) {
		return remote
	}

	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		hops := strings.Split(xff, ",")
		for i := len(hops) - 1; i >= 0; i-- {
			hop := strings.TrimSpace(hops[i])
			if ip := net.ParseIP(hop); ip != nil && !ipInNets(ip, rl.trustedProxies) {
				return hop
			}
		}
	}

	if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
		return xri
	}

	return remote
}

func ipInNets(ip net.IP, nets []*net.IPNet) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
