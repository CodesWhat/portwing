// SPDX-License-Identifier: MIT

package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/auth"
	applog "github.com/codeswhat/portwing/internal/log"
	"github.com/codeswhat/portwing/internal/metrics"
)

// tokenVerifier is the interface used by the authentication middleware to verify a presented
// token. It abstracts over plain-text and Argon2id verification.
type tokenVerifier interface {
	Verify(token string) bool
}

type capacityTokenVerifier interface {
	VerifyWithCapacity(token string) (valid bool, attempted bool)
}

// coldTokenVerifier is implemented by verifiers whose check can need an
// expensive derivation. wouldDerive reports whether verifying the next token
// would run one, so a locked-out source can be refused before any work starts.
type coldTokenVerifier interface {
	wouldDerive() bool
}

func verifyTokenWithCapacity(verifier tokenVerifier, token string) (bool, bool) {
	if bounded, ok := verifier.(capacityTokenVerifier); ok {
		return bounded.VerifyWithCapacity(token)
	}
	return verifier.Verify(token), true
}

// rawTokenVerifier performs timing-safe comparison against a plain-text token.
type rawTokenVerifier struct {
	digest [sha256.Size]byte
}

func newRawTokenVerifier(token string) *rawTokenVerifier {
	return &rawTokenVerifier{digest: sha256.Sum256([]byte(token))}
}

func (v *rawTokenVerifier) Verify(token string) bool {
	presented := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(presented[:], v.digest[:]) == 1
}

const (
	// Header names only; these are not secret values.
	// #nosec G101 -- header name constant; not a credential.
	headerPortwingToken = "X-Portwing-Token"
	// #nosec G101 -- header name constant; not a credential.
	headerDrydockAgentSecret     = "X-Dd-Agent-Secret"
	defaultMaxEnrollmentInFlight = 32
)

// RateLimiter tracks failed authentication attempts by IP address and blocks
// IPs that exceed the failure threshold within a rolling window.
type RateLimiter struct {
	mu       sync.Mutex
	attempts map[string]*ipAttempts
	abuse    map[string]*ipAttempts
	maxFails int
	window   time.Duration
	maxIPs   int
	// maxInFlight bounds concurrent credential checks from one source before
	// any expensive verifier work begins.
	maxInFlight int
	// maxEnrollmentInFlight bounds unauthenticated enrollment handlers across
	// all sources, including addresses that cannot be added to attempts.
	maxEnrollmentInFlight int
	enrollmentInFlight    int

	// preAuth bounds, per limiter key, how many requests are in the
	// pre-verification phase (header/body read plus the credential check).
	// Entries exist only while a request holds or waits for a slot.
	preAuthMu  sync.Mutex
	preAuth    map[string]*preAuthSem
	maxPreAuth int

	trustedProxies []*net.IPNet

	done chan struct{}
}

// limiterKey maps a client address to the key the rate limiter tracks it by.
// IPv6 sources are keyed by their /64 prefix, because one host or customer
// typically owns a whole /64 and could otherwise rotate through addresses to
// get a fresh failure budget per request. IPv4 and IPv4-mapped IPv6 addresses
// stay per address. Anything that doesn't parse as an IP is returned as is, so
// an already-built key passes through unchanged.
func limiterKey(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.WithZone("").Unmap()
	if addr.Is4() {
		return addr.String()
	}
	// Prefix only errors for a bit count outside 0-128, never for 64.
	prefix, _ := addr.Prefix(64)
	return prefix.String()
}

type ipAttempts struct {
	count     int
	firstFail time.Time
	inFlight  int
}

// NewRateLimiter returns a RateLimiter that allows 10 failures per IP within
// a one-minute window. A background goroutine prunes expired entries every
// five minutes.
func NewRateLimiter() *RateLimiter {
	rl := &RateLimiter{
		attempts:              make(map[string]*ipAttempts),
		abuse:                 make(map[string]*ipAttempts),
		maxFails:              10,
		window:                time.Minute,
		maxIPs:                10000,
		maxInFlight:           2,
		maxEnrollmentInFlight: defaultMaxEnrollmentInFlight,
		done:                  make(chan struct{}),
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
			rl.sweepExpired(time.Now())
		}
	}
}

// sweepExpired drops entries whose window closed before now. It is split out of
// cleanup so the pruning rules can be exercised without waiting on the ticker.
// In-flight entries are kept regardless of age: their firstFail is only written
// once the check completes, so evicting one would lose the concurrency count.
func (rl *RateLimiter) sweepExpired(now time.Time) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	for ip, a := range rl.attempts {
		if a.inFlight == 0 && !a.firstFail.IsZero() && now.Sub(a.firstFail) > rl.window {
			delete(rl.attempts, ip)
		}
	}
	for ip, a := range rl.abuse {
		if now.Sub(a.firstFail) > rl.window {
			delete(rl.abuse, ip)
		}
	}
}

func (rl *RateLimiter) isAbuseRateLimited(ip string) bool {
	ip = limiterKey(ip)
	rl.mu.Lock()
	defer rl.mu.Unlock()
	a, ok := rl.abuse[ip]
	if !ok {
		// A source that can't be tracked is refused rather than waved through.
		return len(rl.abuse) >= rl.maxIPs
	}
	if time.Since(a.firstFail) > rl.window {
		delete(rl.abuse, ip)
		return false
	}
	return a.count >= rl.maxFails
}

func (rl *RateLimiter) recordAbuse(ip string) {
	ip = limiterKey(ip)
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if rl.abuse == nil {
		rl.abuse = make(map[string]*ipAttempts)
	}
	a, ok := rl.abuse[ip]
	if !ok {
		if len(rl.abuse) >= rl.maxIPs {
			return
		}
		rl.abuse[ip] = &ipAttempts{count: 1, firstFail: time.Now()}
		return
	}
	if time.Since(a.firstFail) > rl.window {
		a.count = 1
		a.firstFail = time.Now()
		return
	}
	a.count++
}

// IsRateLimited returns true if the given IP has exceeded the failure threshold
// within the current window.
func (rl *RateLimiter) IsRateLimited(ip string) bool {
	ip = limiterKey(ip)
	rl.mu.Lock()
	defer rl.mu.Unlock()

	a, ok := rl.attempts[ip]
	if !ok {
		return false
	}

	if a.firstFail.IsZero() {
		return false
	}

	// If the window has expired, remove the entry and allow through when no
	// verification currently holds a reservation.
	if time.Since(a.firstFail) > rl.window {
		if a.inFlight == 0 {
			delete(rl.attempts, ip)
		} else {
			a.count = 0
			a.firstFail = time.Time{}
		}
		return false
	}

	return a.count >= rl.maxFails
}

// tryBeginAuth atomically reserves one per-IP verification slot. It combines
// the failure-limit check with in-flight accounting so a concurrent cold-start
// burst cannot race every request past a completed-failure counter.
func (rl *RateLimiter) tryBeginAuth(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return rl.tryBeginAuthLocked(ip)
}

func (rl *RateLimiter) tryBeginAuthLocked(ip string) bool {
	ip = limiterKey(ip)
	a, ok := rl.attempts[ip]
	if ok && !a.firstFail.IsZero() && time.Since(a.firstFail) > rl.window {
		a.count = 0
		a.firstFail = time.Time{}
	}
	if !ok {
		if len(rl.attempts) >= rl.maxIPs {
			// Fail closed at capacity: a source that can't be tracked can't be
			// budgeted, so refusing it is the only way a flood of fresh
			// sources can't walk past the limiter. Tracked sources keep
			// working.
			return false
		}
		a = &ipAttempts{}
		rl.attempts[ip] = a
	}

	maxInFlight := rl.maxInFlight
	if maxInFlight <= 0 {
		maxInFlight = 2
	}
	if a.count >= rl.maxFails || a.inFlight >= maxInFlight {
		return false
	}
	a.inFlight++
	return true
}

// finishAuth releases a verification reservation and records a failed check.
// Successful checks do not consume the rolling failure budget.
func (rl *RateLimiter) finishAuth(ip string, success bool) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.finishAuthLocked(ip, success)
}

func (rl *RateLimiter) finishAuthLocked(ip string, success bool) {
	ip = limiterKey(ip)
	a, ok := rl.attempts[ip]
	if !ok {
		return
	}
	if a.inFlight > 0 {
		a.inFlight--
	}
	if success {
		if a.count == 0 && a.inFlight == 0 {
			delete(rl.attempts, ip)
		}
		return
	}

	now := time.Now()
	if a.firstFail.IsZero() || now.Sub(a.firstFail) > rl.window {
		a.count = 1
		a.firstFail = now
		return
	}
	a.count++
}

func (rl *RateLimiter) tryBeginEnrollment(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	maxInFlight := rl.maxEnrollmentInFlight
	if maxInFlight <= 0 {
		maxInFlight = defaultMaxEnrollmentInFlight
	}
	if rl.enrollmentInFlight >= maxInFlight || !rl.tryBeginAuthLocked(ip) {
		return false
	}
	rl.enrollmentInFlight++
	return true
}

func (rl *RateLimiter) finishEnrollment(ip string, success bool) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if rl.enrollmentInFlight > 0 {
		rl.enrollmentInFlight--
	}
	rl.finishAuthLocked(ip, success)
}

const defaultMaxPreAuth = 16

type preAuthSem struct {
	slots chan struct{}
	refs  int
}

// acquirePreAuth waits for one of the source's pre-verification slots. It
// returns a release func, or an error when ctx ends first. A request never
// gets refused for lack of a slot, it queues; the slots only bound how many
// of one source's unauthenticated reads and checks run at once. The entry is
// dropped when its last holder or waiter leaves, so the table can't grow past
// the requests in flight.
func (rl *RateLimiter) acquirePreAuth(ctx context.Context, ip string) (func(), error) {
	key := limiterKey(ip)
	rl.preAuthMu.Lock()
	if rl.preAuth == nil {
		rl.preAuth = make(map[string]*preAuthSem)
	}
	e, ok := rl.preAuth[key]
	if !ok {
		n := rl.maxPreAuth
		if n <= 0 {
			n = defaultMaxPreAuth
		}
		e = &preAuthSem{slots: make(chan struct{}, n)}
		rl.preAuth[key] = e
	}
	e.refs++
	rl.preAuthMu.Unlock()

	leave := func() {
		rl.preAuthMu.Lock()
		e.refs--
		if e.refs == 0 {
			delete(rl.preAuth, key)
		}
		rl.preAuthMu.Unlock()
	}
	select {
	case e.slots <- struct{}{}:
	case <-ctx.Done():
		leave()
		return nil, ctx.Err()
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			<-e.slots
			leave()
		})
	}, nil
}

// admitPreAuth acquires the source's pre-verification slot, waiting up to
// authBodyReadDeadline. If the wait times out it answers 503 with
// Retry-After and counts it where the old in-flight cap's rejections were
// counted. A client that goes away while waiting gets no response.
func (rl *RateLimiter) admitPreAuth(w http.ResponseWriter, r *http.Request, clientIP string, auditor *audit.Logger, reg *metrics.Registry) (func(), bool) {
	ctx, cancel := context.WithTimeout(r.Context(), authBodyReadDeadline)
	defer cancel()
	release, err := rl.acquirePreAuth(ctx, clientIP)
	if err == nil {
		return release, true
	}
	if r.Context().Err() != nil {
		return nil, false
	}
	auditor.RateLimited(clientIP, r.Method, r.URL.Path)
	if reg != nil {
		reg.IncRequest(r.Method, http.StatusServiceUnavailable)
		reg.IncRateLimited()
	}
	w.Header().Set("Retry-After", "1")
	rejectUnadmitted(w, r, "too many concurrent requests from this source", http.StatusServiceUnavailable)
	return nil, false
}

// recordAuthFailure records a failed credential check and reports whether the
// source is locked out: already at the failure limit within its window, or
// unable to be tracked because the table is full. A locked-out source's
// failure is not counted again, so a lockout can't be extended by hammering it.
// It is only ever called for requests whose credentials did not verify; a
// request with valid credentials never reaches it.
func (rl *RateLimiter) recordAuthFailure(ip string) bool {
	key := limiterKey(ip)
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	a, ok := rl.attempts[key]
	if !ok {
		if len(rl.attempts) >= rl.maxIPs {
			return true
		}
		rl.attempts[key] = &ipAttempts{count: 1, firstFail: now}
		return false
	}
	if a.firstFail.IsZero() || now.Sub(a.firstFail) > rl.window {
		a.count = 1
		a.firstFail = now
		return false
	}
	if a.count >= rl.maxFails {
		return true
	}
	a.count++
	return false
}

// lockedOut reports, without changing any state, whether recordAuthFailure
// would currently treat ip as locked out: at the failure limit within its
// window, or unable to be tracked because the table is full.
func (rl *RateLimiter) lockedOut(ip string) bool {
	key := limiterKey(ip)
	rl.mu.Lock()
	defer rl.mu.Unlock()
	a, ok := rl.attempts[key]
	if !ok {
		return len(rl.attempts) >= rl.maxIPs
	}
	if a.firstFail.IsZero() || time.Since(a.firstFail) > rl.window {
		return false
	}
	return a.count >= rl.maxFails
}

// RecordFailure records a failed authentication attempt for the given IP.
// If the total number of tracked IPs exceeds the limit, the failure is silently
// dropped to prevent memory exhaustion. The admission check refuses untracked
// sources at that point, so a dropped failure never lets one through.
func (rl *RateLimiter) RecordFailure(ip string) {
	ip = limiterKey(ip)
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

// rateLimitOnly wraps a handler with rate limiting (by IP) but no auth check.
// This is used for the enrollment endpoint which does its own credential
// check. Authentication failures and malformed-body abuse are tracked in
// distinct rolling windows.
// reg receives request counters and duration histograms; it may be nil.
func (rl *RateLimiter) rateLimitOnly(next http.Handler, reg *metrics.Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		clientIP := rl.clientIP(r)
		if rl.isAbuseRateLimited(clientIP) || !rl.tryBeginEnrollment(clientIP) {
			if reg != nil {
				reg.IncRequest(r.Method, http.StatusTooManyRequests)
				reg.IncRateLimited()
			}
			rejectUnadmitted(w, r, "too many requests", http.StatusTooManyRequests)
			return
		}
		rw := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		defer func() {
			rl.finishEnrollment(clientIP, rw.code != http.StatusUnauthorized)
		}()
		if reg != nil {
			reg.IncInFlight()
			defer reg.DecInFlight()
		}
		next.ServeHTTP(rw, r)
		switch rw.code {
		case http.StatusBadRequest, http.StatusRequestTimeout, http.StatusRequestEntityTooLarge:
			rl.recordAbuse(clientIP)
		}
		if reg != nil {
			reg.IncRequest(r.Method, rw.code)
			reg.ObserveRequestDuration(time.Since(start).Seconds())
		}
	})
}

// Ed25519Config holds the optional Ed25519 verifier parameters for
// AuthMiddlewareWithEd25519. When Registry is nil the Ed25519 path is skipped.
type Ed25519Config struct {
	Registry       *auth.KeyRegistry
	Nonces         *auth.NonceLRU
	MaxSkewSeconds int
}

const (
	// maxSignedBodyBytes caps the body buffered for signature verification on
	// every route except compose.
	maxSignedBodyBytes = 1 << 20
	// maxSignedComposeBodyBytes matches handleCompose's own 32 MiB cap, so a
	// large compose deploy can be signed and still reach the handler.
	maxSignedComposeBodyBytes = 32 << 20
	// maxLargeSignedBodies bounds how many compose-sized bodies are buffered
	// at once, agent-wide. Buffering happens before the signature is checked,
	// so without it unauthenticated callers could pin 32 MiB each.
	maxLargeSignedBodies = 4
	composePath          = "/_portwing/compose"
)

// largeSignedBodySlots holds one token per compose-sized signed body being
// buffered.
var largeSignedBodySlots = make(chan struct{}, maxLargeSignedBodies)

// wantsLargeSignedBody reports whether r is a compose request whose body may
// exceed the default signed-body cap: it declares more than the default, or
// its length is unknown (chunked).
func wantsLargeSignedBody(r *http.Request) bool {
	return r.Method == http.MethodPost && r.URL.Path == composePath &&
		(r.ContentLength > maxSignedBodyBytes || r.ContentLength < 0)
}

// authBodyReadDeadline bounds how long the Ed25519 auth path may block
// reading the (MaxBytesReader-capped) request body. This read happens before
// tryBeginAuth's per-IP concurrency gate, so without a deadline an
// unauthenticated caller could drip a signature-headed body slowly and pin
// a goroutine indefinitely, exhausting fds/goroutines with no credentials.
// Matches ReadHeaderTimeout in http.go: by the time headers (including the
// signature headers) have already arrived, reading up to 1 MiB of body
// should comfortably finish within the same 10 s budget.
// A var, not a const, so tests can shrink it rather than waiting out the
// real deadline.
var authBodyReadDeadline = 10 * time.Second

// isDeadlineExceeded reports whether err was caused by the read deadline set
// via http.ResponseController.SetReadDeadline, as opposed to some other I/O
// error (e.g. a reset connection) or the MaxBytesReader size cap.
func isDeadlineExceeded(err error) bool {
	return errors.Is(err, os.ErrDeadlineExceeded)
}

// rejectUnadmitted answers a request that was turned away before its body was
// read. net/http drains an unread request body once the handler returns
// (server.go's finishRequest, then transfer.go's body.Close), and that drain
// has no deadline of its own, so a declared-but-never-sent body pins the
// handler goroutine there indefinitely — exactly what an admission rejection
// exists to prevent. An already-elapsed read deadline aborts the drain on its
// first read from the connection, and "Connection: close" stops the connection
// being reused with a half-sent body still on the wire. HTTP/2 deadlines are
// stream-scoped; closing the shared connection would disrupt other requests.
func rejectUnadmitted(w http.ResponseWriter, r *http.Request, message string, status int) {
	// Best effort: a ResponseWriter with no underlying connection to bound
	// (httptest.ResponseRecorder, some wrappers) reports ErrNotSupported and
	// has no drain to abort either.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now())
	if r.ProtoMajor == 1 {
		w.Header().Set("Connection", "close")
	}
	http.Error(w, message, status)
}

// authDisabled is the one place the server decides authentication is off: no
// token verifier and no Ed25519 key registry. NewServer's fail-closed check, the
// auth middleware's pass-through, and the Host check all use it.
func authDisabled(verifier tokenVerifier, ed Ed25519Config) bool {
	return verifier == nil && ed.Registry == nil
}

// AuthMiddlewareWithEd25519 validates raw or Argon2id credentials and supports
// an optional Ed25519 verification path. When a request carries X-Portwing-Signature,
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
		if authDisabled(verifier, ed) {
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

		// Credentials are checked before any lockout applies. A request that
		// verifies is never refused by the failed-auth limiter, so one noisy
		// client behind a shared address can't lock out the controller. The
		// limiter only decides how a failed check is answered (401, or 429
		// once the source is locked out).

		// If Ed25519 is configured and the request carries a signature, use
		// that path exclusively. Reading the body first is required because
		// VerifyRequest needs it for the canonical message.
		if ed.Registry != nil && auth.HasSignature(r.Header) {
			// Buffer the body. The signature covers its SHA-256, so it has to be
			// held whole to verify. It is capped at 1 MiB, or 32 MiB for a
			// compose request that declares more (bounded agent-wide by
			// largeSignedBodySlots); MaxBytesReader also closes the connection
			// on overflow, preventing slow-drip memory exhaustion.
			releasePreAuth, admitted := rl.admitPreAuth(w, r, clientIP, auditor, reg)
			if !admitted {
				return
			}
			// The per-source pre-auth slot covers only the read and verify
			// phase, so it is released as soon as verification ends. The
			// large-body slot is different: it bounds resident memory, and the
			// buffered body stays referenced by the request until the
			// downstream handler returns, so that slot is held for the whole
			// request.
			defer releasePreAuth()
			var body []byte
			if r.Body != nil {
				limit := int64(maxSignedBodyBytes)
				if wantsLargeSignedBody(r) {
					select {
					case largeSignedBodySlots <- struct{}{}:
						defer func() { <-largeSignedBodySlots }()
						limit = maxSignedComposeBodyBytes
					default:
						w.Header().Set("Retry-After", "1")
						rejectUnadmitted(w, r, "too many large requests in progress", http.StatusServiceUnavailable)
						return
					}
				}
				r.Body = http.MaxBytesReader(w, r.Body, limit)

				// Deadline the read itself, not just its size: a slow drip
				// under the 1 MiB cap would otherwise pin this goroutine for
				// as long as it liked, holding the slot reserved above.
				rc := http.NewResponseController(w)
				if err := rc.SetReadDeadline(time.Now().Add(authBodyReadDeadline)); err != nil {
					slog.Warn("setting auth body read deadline", "error", err)
				}
				var err error
				body, err = io.ReadAll(r.Body)
				if closeErr := r.Body.Close(); closeErr != nil {
					slog.Warn("closing request body", "error", closeErr)
				}
				// Clear the deadline so it doesn't linger onto downstream
				// handlers, some of which are deliberately unbounded
				// streaming endpoints (logs, events, stats, exec).
				if clearErr := rc.SetReadDeadline(time.Time{}); clearErr != nil {
					slog.Warn("clearing auth body read deadline", "error", clearErr)
				}
				if err != nil {
					if isDeadlineExceeded(err) {
						http.Error(w, "request body read timed out", http.StatusRequestTimeout)
						return
					}
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
			verified, err := auth.VerifyRequestDetailed(r, body, ed.Registry, ed.Nonces, skew)
			releasePreAuth()
			keyID := verified.KeyID
			if err != nil {
				reason := auth.ReasonFor(err)
				// A full nonce cache is the agent's own capacity limit, not a
				// credential failure, so it must not spend the IP's failure
				// budget or a busy controller would lock itself out with 429.
				if !errors.Is(err, auth.ErrNonceCapacity) && rl.recordAuthFailure(clientIP) {
					rejectLockedOut(w, r, clientIP, auditor, reg)
					return
				}
				slog.Warn("ed25519 authentication failed",
					"ip", applog.Sanitize(clientIP), "reason", applog.Sanitize(reason))
				auditor.AuthFailure(clientIP, r.Method, r.URL.Path)
				if reg != nil {
					reg.IncRequest(r.Method, http.StatusUnauthorized)
					reg.IncAuthFailure(reason)
				}
				setAuthReason(w.Header(), reason)
				http.Error(w, auth.RejectionDetail(err), http.StatusUnauthorized)
				return
			}
			if verified.NewlyLocked {
				slog.Info("ed25519 key locked to signature version 3: v2 and legacy signatures are now refused for it",
					"key_id", applog.Sanitize(keyID))
				auditor.SignatureLock(clientIP, "lock", keyID, audit.OutcomeAllowed)
			}
			if verified.LockErr != nil {
				slog.Error("persisting signature version lock failed; the lock holds until restart",
					"key_id", applog.Sanitize(keyID), "error", applog.Sanitize(verified.LockErr.Error()))
			}
			slog.Debug("ed25519 authentication succeeded", "key_id", applog.Sanitize(keyID), "ip", applog.Sanitize(clientIP))
			r = r.WithContext(context.WithValue(r.Context(), verifiedKeyIDContextKey{}, keyID))
			rw := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
			if reg != nil {
				reg.IncInFlight()
				defer reg.DecInFlight()
			}
			next.ServeHTTP(rw, r)
			auditor.APIRequest(clientIP, r.Method, r.URL.Path, audit.OutcomeAllowed, rw.code, ms(start), audit.WithKeyID(keyID))
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
			if rl.recordAuthFailure(clientIP) {
				rejectLockedOut(w, r, clientIP, auditor, reg)
				return
			}
			slog.Warn("authentication failed: no credentials presented", "ip", applog.Sanitize(clientIP))
			auditor.AuthFailure(clientIP, r.Method, r.URL.Path)
			if reg != nil {
				reg.IncRequest(r.Method, http.StatusUnauthorized)
				reg.IncAuthFailure("no_credentials")
			}
			rejectUnadmitted(w, r, "unauthorized", http.StatusUnauthorized)
			return
		}

		provided := agentToken(r)
		// Credentials are verified before the lockout so a valid caller behind
		// a shared address is never refused. That order must not let a
		// locked-out source keep triggering cold Argon2 derivations, which
		// hold the agent-wide slots and starve other sources. So a locked-out
		// source is only verified when the check is cheap (a raw compare, or
		// a TOKEN_HASH whose derivation is already cached).
		lockedOutCold := func() bool {
			cold, ok := verifier.(coldTokenVerifier)
			return ok && cold.wouldDerive() && rl.lockedOut(clientIP)
		}
		if lockedOutCold() {
			rejectLockedOut(w, r, clientIP, auditor, reg)
			return
		}
		releasePreAuth, admitted := rl.admitPreAuth(w, r, clientIP, auditor, reg)
		if !admitted {
			return
		}
		// Admission can wait for seconds behind the source's other requests,
		// and those can push it into lockout meanwhile. Without this recheck
		// every queued request would still run a cold derivation.
		if lockedOutCold() {
			releasePreAuth()
			rejectLockedOut(w, r, clientIP, auditor, reg)
			return
		}
		valid, attempted := verifyTokenWithCapacity(verifier, provided)
		releasePreAuth()
		if !attempted {
			// The agent-wide cold Argon2 slots are full. That is the agent's
			// own capacity limit, not a credential verdict, so it doesn't
			// touch the source's failure budget.
			auditor.RateLimited(clientIP, r.Method, r.URL.Path)
			if reg != nil {
				reg.IncRequest(r.Method, http.StatusTooManyRequests)
				reg.IncRateLimited()
			}
			rejectUnadmitted(w, r, "authentication verification capacity exceeded", http.StatusTooManyRequests)
			return
		}

		if !valid {
			if rl.recordAuthFailure(clientIP) {
				rejectLockedOut(w, r, clientIP, auditor, reg)
				return
			}
			slog.Warn("authentication failed", "ip", applog.Sanitize(clientIP))
			auditor.AuthFailure(clientIP, r.Method, r.URL.Path)
			if reg != nil {
				reg.IncRequest(r.Method, http.StatusUnauthorized)
				reg.IncAuthFailure("bad_token")
			}
			rejectUnadmitted(w, r, "unauthorized", http.StatusUnauthorized)
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

// rejectLockedOut answers a request whose credentials failed from a source
// that is already locked out (or can't be tracked) with the 429 path.
func rejectLockedOut(w http.ResponseWriter, r *http.Request, clientIP string, auditor *audit.Logger, reg *metrics.Registry) {
	auditor.RateLimited(clientIP, r.Method, r.URL.Path)
	if reg != nil {
		reg.IncRequest(r.Method, http.StatusTooManyRequests)
		reg.IncRateLimited()
	}
	rejectUnadmitted(w, r, "too many failed attempts", http.StatusTooManyRequests)
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
				if err == http.ErrAbortHandler { //nolint:errorlint // net/http recognizes only the exact abort sentinel.
					panic(err)
				}
				stack := debug.Stack()
				slog.Error("panic recovered",
					"error", applog.Sanitize(fmt.Sprintf("%v", err)),
					"stack", string(stack),
					"method", applog.Sanitize(r.Method),
					"path", applog.Sanitize(r.URL.Path),
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
					s = ip.String() + "/32"
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
// headers are only consulted when the direct peer is a trusted proxy.
//
// Every X-Forwarded-For field line is merged in order (a proxy such as HAProxy
// with `option forwardfor` appends a separate line rather than extending the
// first), and the combined chain is walked right to left. Each hop may be a
// bare address or an ip:port / [v6]:port pair. A trusted-proxy hop continues
// the walk and the first non-trusted hop wins. A hop that does not parse is a
// boundary: the walk stops there and the direct peer is returned, because
// anything to its left was written by a party we can't vouch for. X-Real-IP is
// consulted only when the chain yields nothing (header absent or every hop a
// trusted proxy), never after an unparseable boundary.
//
// Every value taken from a forwarding header must parse as an IP address. The
// return value keys the auth rate limiter and lands in audit records as the
// actor, so accepting an arbitrary header string would let a caller behind a
// trusted proxy mint a fresh limiter bucket per request, defeating the
// failed-attempt throttle it is supposed to enforce, and write arbitrary
// actor values into the audit trail. Returned addresses are canonicalized
// (IPv4-mapped IPv6 unmapped, zone dropped) so one client keeps one bucket.
func (rl *RateLimiter) clientIP(r *http.Request) string {
	remote := r.RemoteAddr
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}

	remoteIP := net.ParseIP(remote)
	if remoteIP == nil || !ipInNets(remoteIP, rl.trustedProxies) {
		return remote
	}

	// Any X-Forwarded-For field line, even an empty one, makes the chain
	// authoritative: an empty hop is unparseable and ends the walk.
	if xffLines := r.Header.Values("X-Forwarded-For"); len(xffLines) > 0 {
		hops := strings.Split(strings.Join(xffLines, ","), ",")
		for i := len(hops) - 1; i >= 0; i-- {
			addr, ok := parseForwardedHop(hops[i])
			if !ok {
				return remote
			}
			if !ipInNets(net.IP(addr.AsSlice()), rl.trustedProxies) {
				return addr.String()
			}
		}
	}

	if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
		if addr, ok := parseForwardedHop(xri); ok && !ipInNets(net.IP(addr.AsSlice()), rl.trustedProxies) {
			return addr.String()
		}
	}

	return remote
}

// parseForwardedHop parses one forwarding-header value as a bare address or an
// ip:port pair and returns it canonicalized: IPv4-mapped IPv6 unmapped and any
// zone dropped.
func parseForwardedHop(hop string) (netip.Addr, bool) {
	hop = strings.TrimSpace(hop)
	addr, err := netip.ParseAddr(hop)
	if err != nil {
		ap, apErr := netip.ParseAddrPort(hop)
		if apErr != nil {
			return netip.Addr{}, false
		}
		addr = ap.Addr()
	}
	return addr.WithZone("").Unmap(), true
}

func ipInNets(ip net.IP, nets []*net.IPNet) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
