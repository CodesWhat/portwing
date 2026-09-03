package server

// http_mutants_test.go adds tests that specifically target Gremlins mutants
// surviving in http.go: boundary/negation/arithmetic conditions that existing
// tests exercised but did not pin down at the exact boundary or on both sides
// of a negated comparison.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/adapter"
	"github.com/codeswhat/portwing/internal/audit"
)

// TestMaxHijackBodyBytesValue pins the exact computed value of
// maxHijackBodyBytes, killing the two ARITHMETIC_BASE mutants on its
// `10 * 1024 * 1024` literal (http.go:69:31, http.go:69:38). Either mutated
// multiplication (to division) collapses the constant to 0 or 10.
func TestMaxHijackBodyBytesValue(t *testing.T) {
	t.Parallel()

	const want = 10 * 1024 * 1024
	if maxHijackBodyBytes != want {
		t.Fatalf("maxHijackBodyBytes = %d, want %d", maxHijackBodyBytes, want)
	}
}

// TestNewServerHTTPTimeouts pins the exact ReadHeaderTimeout and IdleTimeout
// NewServer configures on the underlying http.Server, killing the
// ARITHMETIC_BASE mutants at http.go:295:25 (`10 * time.Second`) and
// http.go:296:26 (`120 * time.Second`).
func TestNewServerHTTPTimeouts(t *testing.T) {
	t.Parallel()

	client, stop := newStubDockerClient(t)
	defer stop()

	s, err := NewServer(minimalConfig(), client, &stubServerAdapter{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	}()

	if got, want := s.httpServer.ReadHeaderTimeout, 10*time.Second; got != want {
		t.Errorf("ReadHeaderTimeout = %v, want %v", got, want)
	}
	if got, want := s.httpServer.IdleTimeout, 120*time.Second; got != want {
		t.Errorf("IdleTimeout = %v, want %v", got, want)
	}
}

// TestNewServerTLSConfigGuard exercises both sides of the two
// CONDITIONALS_NEGATION mutants at http.go:300 (`cfg.TLSCert != "" &&
// cfg.TLSKey != ""`). Only when both fields are non-empty must TLSConfig be
// set; either field alone must leave it nil.
func TestNewServerTLSConfigGuard(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		cert, key      string
		wantTLSEnabled bool
	}{
		{"neither set", "", "", false},
		{"cert only", "cert.pem", "", false}, // kills mutant on cfg.TLSKey != ""
		{"key only", "", "key.pem", false},   // kills mutant on cfg.TLSCert != ""
		{"both set", "cert.pem", "key.pem", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			client, stop := newStubDockerClient(t)
			defer stop()

			cfg := minimalConfig()
			cfg.TLSCert = tc.cert
			cfg.TLSKey = tc.key

			s, err := NewServer(cfg, client, &stubServerAdapter{})
			if err != nil {
				t.Fatalf("NewServer: %v", err)
			}
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = s.Shutdown(ctx)
			}()

			gotEnabled := s.httpServer.TLSConfig != nil
			if gotEnabled != tc.wantTLSEnabled {
				t.Fatalf("TLSConfig set = %v, want %v", gotEnabled, tc.wantTLSEnabled)
			}
		})
	}
}

// TestOversizedAttachBodyAtExactLimitIsNotRejected verifies a body of exactly
// maxHijackBodyBytes is NOT rejected as too large, killing the
// CONDITIONALS_BOUNDARY mutant at http.go:594:15 (`len(body) > maxHijackBodyBytes`
// -> `>=`). The request still fails past the size check (httptest.ResponseRecorder
// doesn't support hijacking), but it must fail with 500 "hijacking not
// supported", not 413.
func TestOversizedAttachBodyAtExactLimitIsNotRejected(t *testing.T) {
	t.Parallel()

	s := &Server{}
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1.44/containers/abc123/attach",
		io.LimitReader(zeroReader{}, maxHijackBodyBytes),
	)
	rec := httptest.NewRecorder()
	s.handleDockerHijack(rec, req)

	if rec.Code == http.StatusRequestEntityTooLarge {
		t.Fatalf("body exactly at maxHijackBodyBytes was rejected as too large")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d (hijacking not supported)", rec.Code, http.StatusInternalServerError)
	}
}

// TestIsASCIIDigitsBoundary exercises isASCIIDigits at the '0' and '9'
// boundaries and their immediate neighbors, killing the two
// CONDITIONALS_BOUNDARY mutants at http.go:759 (`value[i] < '0'` and
// `value[i] > '9'`).
func TestIsASCIIDigitsBoundary(t *testing.T) {
	t.Parallel()

	cases := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"0", true}, // exact lower boundary
		{"9", true}, // exact upper boundary
		{"09", true},
		{"/", false}, // '/' == '0'-1, just below lower boundary
		{":", false}, // ':' == '9'+1, just above upper boundary
		{"123", true},
		{"12a", false},
	}

	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			t.Parallel()
			if got := isASCIIDigits(tc.value); got != tc.want {
				t.Errorf("isASCIIDigits(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

// flushTrackingResponseWriter records whether Flush was called, to observe
// streamResponse's internal `if n > 0` guard.
type flushTrackingResponseWriter struct {
	hdr     http.Header
	body    []byte
	flushed bool
}

func (w *flushTrackingResponseWriter) Header() http.Header { return w.hdr }
func (w *flushTrackingResponseWriter) Write(p []byte) (int, error) {
	w.body = append(w.body, p...)
	return len(p), nil
}
func (w *flushTrackingResponseWriter) WriteHeader(int) {}
func (w *flushTrackingResponseWriter) Flush()          { w.flushed = true }

// zeroByteThenEOFReader returns (0, io.EOF) on every Read, simulating a
// stream that ends without ever producing a byte.
type zeroByteThenEOFReader struct{}

func (zeroByteThenEOFReader) Read([]byte) (int, error) { return 0, io.EOF }

// TestStreamResponseSkipsWriteAndFlushOnZeroRead verifies that a zero-byte
// read does not trigger a Write or a Flush, killing the CONDITIONALS_BOUNDARY
// mutant at http.go:803:8 (`if n > 0` -> `n >= 0`).
func TestStreamResponseSkipsWriteAndFlushOnZeroRead(t *testing.T) {
	t.Parallel()

	w := &flushTrackingResponseWriter{hdr: make(http.Header)}
	s := &Server{}
	s.streamResponse(w, zeroByteThenEOFReader{})

	if w.flushed {
		t.Fatal("streamResponse flushed on a zero-byte read, want no flush")
	}
	if len(w.body) != 0 {
		t.Fatalf("streamResponse wrote %d bytes on a zero-byte read, want 0", len(w.body))
	}
}

// TestShutdownWithNilHupDoneDoesNotPanic verifies Shutdown is a no-op on the
// hupCh/hupDone SIGHUP-reload cleanup when neither field was populated (a
// Server built without NewServer's authorized_keys setup). This kills the
// CONDITIONALS_NEGATION mutant at http.go:898:16 (`s.hupDone != nil` ->
// `== nil`): the mutated code would enter `select { case <-s.hupDone: default:
// close(s.hupDone) }` with hupDone nil, and `close` of a nil channel panics.
func TestShutdownWithNilHupDoneDoesNotPanic(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter()
	auditor, closeAudit, err := audit.New("", 0)
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}
	t.Cleanup(closeAudit)

	httpSrv := &http.Server{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}

	s := &Server{
		rateLimiter: rl,
		auditor:     auditor,
		httpServer:  httpSrv,
		// hupCh and hupDone are left at their zero value (nil), matching a
		// Server with no AuthorizedKeysFile configured.
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := s.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown with nil hupCh/hupDone: %v", err)
	}
}

// tickErrorCountingAdapter succeeds on the initial RefreshContainers call
// (so pollContainers reaches its ticker loop) and errors on every ticker
// call, while counting OnContainerRefresh invocations.
type tickErrorCountingAdapter struct {
	stubServerAdapter
	initialDone        atomic.Bool
	onRefreshCallCount atomic.Int32
}

func (a *tickErrorCountingAdapter) RefreshContainers(_ context.Context) ([]adapter.Container, []adapter.Container, []adapter.Container, error) {
	if a.initialDone.CompareAndSwap(false, true) {
		return nil, nil, nil, nil // initial refresh succeeds
	}
	return nil, nil, nil, context.DeadlineExceeded // every ticker refresh fails
}

func (a *tickErrorCountingAdapter) OnContainerRefresh(_ context.Context, _ adapter.MessageSender, _, _, _ []adapter.Container) error {
	a.onRefreshCallCount.Add(1)
	return nil
}

// TestPollContainersSkipsNotifyAfterTickRefreshError verifies that a
// RefreshContainers error on the ticker path causes pollContainers to
// `continue` (skip OnContainerRefresh) rather than fall through to call it,
// killing the CONDITIONALS_NEGATION mutant at http.go:970:11 (`if err != nil`
// -> `== nil`). A mutated build would keep calling OnContainerRefresh with
// the zero-value (nil) added/updated/removed slices after every failed
// ticker refresh, in addition to the single legitimate call the successful
// initial refresh makes.
func TestPollContainersSkipsNotifyAfterTickRefreshError(t *testing.T) {
	t.Parallel()

	client, stop := newStubDockerClient(t)
	defer stop()

	auditor, closeAudit, err := audit.New("", 0)
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}
	defer closeAudit()

	cfg := minimalConfig()
	cfg.DDPollInterval = 1 // 1-second ticker

	a := &tickErrorCountingAdapter{}
	s := &Server{
		dockerClient: client,
		adapter:      a,
		cfg:          cfg,
		rateLimiter:  NewRateLimiter(),
		auditor:      auditor,
	}
	defer s.rateLimiter.Stop()

	// Give the ticker time to fire a few times.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.pollContainers(ctx)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pollContainers did not exit in time")
	}

	// The single legitimate call comes from the successful initial refresh
	// (before the ticker loop starts); every ticker refresh failed, so no
	// further calls should follow.
	if got := a.onRefreshCallCount.Load(); got != 1 {
		t.Fatalf("OnContainerRefresh called %d times (want exactly 1, from the initial refresh) after every ticker RefreshContainers failed", got)
	}
}
