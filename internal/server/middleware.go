package server

import (
	"bufio"
	"bytes"
	"crypto/subtle"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/auth"
	"github.com/codeswhat/portwing/internal/metrics"
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

const (
	// Header names only; these are not secret values.
	// #nosec G101 -- header name constant; not a credential.
	headerPortwingToken = "X-Portwing-Token"
	// #nosec G101 -- header name constant; not a credential.
	headerDrydockAgentSecret = "X-Dd-Agent-Secret"
)

// RateLimiter tracks failed authentication attempts by IP address and blocks
// IPs that exceed the failure threshold within a rolling window.
type RateLimiter struct {
	mu       sync.Mutex
	attempts map[string]*ipAttempts
	maxFails int
	window   time.Duration
	maxIPs   int

	trustedProxies []*net.IPNet

	done chan struct{}
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
		done:     make(chan struct{}),
	}
	go rl.cleanup()
	return rl
}

// Stop terminates the background cleanup goroutine. It is idempotent.
func (rl *RateLimiter) Stop() {
	select {
	case <-rl.done:
	default:
		close(rl.done)
	}
}

// cleanup runs every 5 minutes and removes entries whose window has expired.
func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-rl.done:
			return
		case <-ticker.C:
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
// The middleware checks Authorization: Bearer first, then X-Portwing-Token,
// then X-Dd-Agent-Secret for Drydock compatibility.
//
// Rate limiting is applied before verification, so failed attempts are always
// counted regardless of whether the verifier uses a raw token or Argon2id.
//
// auditor receives auth_failure and rate_limited events, and an api_request
// event (with outcome and duration) for every request that reaches next.
// reg receives request/auth/rate-limit counters and duration histograms;
// it may be nil (metrics are skipped in that case).
func (rl *RateLimiter) AuthMiddleware(verifier tokenVerifier, auditor *audit.Logger, reg *metrics.Registry, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// No authentication configured - pass through.
		if verifier == nil {
			rw := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
			if reg != nil {
				reg.IncInFlight()
				defer reg.DecInFlight()
			}
			next.ServeHTTP(rw, r)
			auditor.APIRequest("", r.Method, r.URL.Path, audit.OutcomeAllowed, rw.code, ms(start))
			if reg != nil {
				reg.IncRequest(r.Method, rw.code)
				reg.ObserveRequestDuration(time.Since(start).Seconds())
			}
			return
		}

		clientIP := rl.clientIP(r)

		// Rate-limit check happens BEFORE verification. This ensures that failed
		// attempts accumulate in the limiter even for expensive Argon2id paths,
		// and that blocked IPs never reach the verifier.
		if rl.IsRateLimited(clientIP) {
			auditor.RateLimited(clientIP, r.Method, r.URL.Path)
			if reg != nil {
				reg.IncRequest(r.Method, http.StatusTooManyRequests)
				reg.IncRateLimited()
			}
			http.Error(w, "too many failed attempts", http.StatusTooManyRequests)
			return
		}

		provided := agentToken(r)

		if !verifier.Verify(provided) {
			rl.RecordFailure(clientIP)
			slog.Warn("authentication failed", "ip", clientIP)
			auditor.AuthFailure(clientIP, r.Method, r.URL.Path)
			if reg != nil {
				reg.IncRequest(r.Method, http.StatusUnauthorized)
				reg.IncAuthFailure("bad_token")
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		rw := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		if reg != nil {
			reg.IncInFlight()
			defer reg.DecInFlight()
		}
		next.ServeHTTP(rw, r)
		auditor.APIRequest(clientIP, r.Method, r.URL.Path, audit.OutcomeAllowed, rw.code, ms(start))
		if reg != nil {
			reg.IncRequest(r.Method, rw.code)
			reg.ObserveRequestDuration(time.Since(start).Seconds())
		}
	})
}

// rateLimitOnly wraps a handler with rate limiting (by IP) but no auth check.
// This is used for the enrollment endpoint which does its own credential
// check. Downstream 401 responses are recorded as failures so the endpoint
// cannot be brute-forced past the limiter.
// reg receives request counters and duration histograms; it may be nil.
func (rl *RateLimiter) rateLimitOnly(next http.Handler, reg *metrics.Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		clientIP := rl.clientIP(r)
		if rl.IsRateLimited(clientIP) {
			if reg != nil {
				reg.IncRequest(r.Method, http.StatusTooManyRequests)
				reg.IncRateLimited()
			}
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		rw := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		if reg != nil {
			reg.IncInFlight()
			defer reg.DecInFlight()
		}
		next.ServeHTTP(rw, r)
		if rw.code == http.StatusUnauthorized {
			rl.RecordFailure(clientIP)
		}
		if reg != nil {
			reg.IncRequest(r.Method, rw.code)
			reg.ObserveRequestDuration(time.Since(start).Seconds())
		}
	})
}

// Ed25519Config holds the optional Ed25519 verifier parameters for
// AuthMiddlewareWithEd25519. When Registry is nil the Ed25519 path is skipped
// and the middleware behaves identically to AuthMiddleware.
type Ed25519Config struct {
	Registry       *auth.KeyRegistry
	Nonces         *auth.NonceLRU
	MaxSkewSeconds int
}

// AuthMiddlewareWithEd25519 is AuthMiddleware extended with an optional Ed25519
// verification path. When an incoming request carries X-Portwing-Signature,
// it is verified via Ed25519;
// otherwise the request falls through to the token verifier. Either path must
// succeed for the request to proceed.
//
// The body is consumed and buffered for signature verification; the downstream
// handler sees a fresh reader.
// reg receives request/auth/rate-limit counters and duration histograms;
// it may be nil (metrics are skipped in that case).
func (rl *RateLimiter) AuthMiddlewareWithEd25519(
	verifier tokenVerifier,
	ed Ed25519Config,
	auditor *audit.Logger,
	reg *metrics.Registry,
	next http.Handler,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// No authentication configured - pass through.
		if verifier == nil && ed.Registry == nil {
			rw := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
			if reg != nil {
				reg.IncInFlight()
				defer reg.DecInFlight()
			}
			next.ServeHTTP(rw, r)
			auditor.APIRequest("", r.Method, r.URL.Path, audit.OutcomeAllowed, rw.code, ms(start))
			if reg != nil {
				reg.IncRequest(r.Method, rw.code)
				reg.ObserveRequestDuration(time.Since(start).Seconds())
			}
			return
		}

		clientIP := rl.clientIP(r)

		if rl.IsRateLimited(clientIP) {
			auditor.RateLimited(clientIP, r.Method, r.URL.Path)
			if reg != nil {
				reg.IncRequest(r.Method, http.StatusTooManyRequests)
				reg.IncRateLimited()
			}
			http.Error(w, "too many failed attempts", http.StatusTooManyRequests)
			return
		}

		// If Ed25519 is configured and the request carries a signature, use
		// that path exclusively. Reading the body first is required because
		// VerifyRequest needs it for the canonical message.
		if ed.Registry != nil && auth.HasSignature(r.Header) {
			// Buffer the body (capped at 1 MB; MaxBytesReader also closes the
			// connection on overflow, preventing slow-drip memory exhaustion).
			var body []byte
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
				var err error
				body, err = io.ReadAll(r.Body)
				if closeErr := r.Body.Close(); closeErr != nil {
					slog.Warn("closing request body", "error", closeErr)
				}
				if err != nil {
					http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
					return
				}
				// Restore for downstream handlers.
				r.Body = io.NopCloser(bytes.NewReader(body))
			}

			skew := ed.MaxSkewSeconds
			if skew <= 0 {
				skew = 60
			}
			keyID, err := auth.VerifyRequest(r, body, ed.Registry, ed.Nonces, skew)
			if err != nil {
				rl.RecordFailure(clientIP)
				reason := auth.ReasonFor(err)
				slog.Warn("ed25519 authentication failed",
					"ip", clientIP, "reason", reason)
				auditor.AuthFailure(clientIP, r.Method, r.URL.Path)
				if reg != nil {
					reg.IncRequest(r.Method, http.StatusUnauthorized)
					reg.IncAuthFailure(reason)
				}
				setAuthReason(w.Header(), reason)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			slog.Debug("ed25519 authentication succeeded", "key_id", keyID, "ip", clientIP)
			rw := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
			if reg != nil {
				reg.IncInFlight()
				defer reg.DecInFlight()
			}
			next.ServeHTTP(rw, r)
			auditor.APIRequest(clientIP, r.Method, r.URL.Path, audit.OutcomeAllowed, rw.code, ms(start))
			if reg != nil {
				reg.IncRequest(r.Method, rw.code)
				reg.ObserveRequestDuration(time.Since(start).Seconds())
			}
			return
		}

		// Fall through to token verifier.
		if verifier == nil {
			// Ed25519 was configured but request had no signature, and there
			// is no token verifier — authentication required but none presented.
			rl.RecordFailure(clientIP)
			slog.Warn("authentication failed: no credentials presented", "ip", clientIP)
			auditor.AuthFailure(clientIP, r.Method, r.URL.Path)
			if reg != nil {
				reg.IncRequest(r.Method, http.StatusUnauthorized)
				reg.IncAuthFailure("no_credentials")
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		provided := agentToken(r)

		if !verifier.Verify(provided) {
			rl.RecordFailure(clientIP)
			slog.Warn("authentication failed", "ip", clientIP)
			auditor.AuthFailure(clientIP, r.Method, r.URL.Path)
			if reg != nil {
				reg.IncRequest(r.Method, http.StatusUnauthorized)
				reg.IncAuthFailure("bad_token")
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		rw := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		if reg != nil {
			reg.IncInFlight()
			defer reg.DecInFlight()
		}
		next.ServeHTTP(rw, r)
		auditor.APIRequest(clientIP, r.Method, r.URL.Path, audit.OutcomeAllowed, rw.code, ms(start))
		if reg != nil {
			reg.IncRequest(r.Method, rw.code)
			reg.ObserveRequestDuration(time.Since(start).Seconds())
		}
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

func agentToken(r *http.Request) string {
	if token := bearerToken(r); token != "" {
		return token
	}
	if token := r.Header.Get(headerPortwingToken); token != "" {
		return token
	}
	return r.Header.Get(headerDrydockAgentSecret)
}

func setAuthReason(h http.Header, reason string) {
	h.Set(auth.HeaderReason, reason)
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
