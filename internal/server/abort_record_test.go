// SPDX-License-Identifier: MIT

package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/metrics"
)

// An aborted proxy response commits its status and then panics with
// http.ErrAbortHandler. The request must still be audited and counted once,
// with the committed status, and the panic must keep propagating.
func TestAuthMiddlewareRecordsAbortedResponses(t *testing.T) {
	t.Parallel()

	ed, priv := setupEd25519(t)
	paths := map[string]func(t *testing.T, next http.Handler, l *audit.Logger, reg *metrics.Registry) (http.Handler, func(*http.Request)){
		"auth disabled": func(t *testing.T, next http.Handler, l *audit.Logger, reg *metrics.Registry) (http.Handler, func(*http.Request)) {
			rl := NewRateLimiter()
			t.Cleanup(rl.Stop)
			return rl.AuthMiddlewareWithEd25519(nil, Ed25519Config{}, l, reg, next), func(*http.Request) {}
		},
		"token": func(t *testing.T, next http.Handler, l *audit.Logger, reg *metrics.Registry) (http.Handler, func(*http.Request)) {
			rl := NewRateLimiter()
			t.Cleanup(rl.Stop)
			return rl.AuthMiddlewareWithEd25519(newRawTokenVerifier("right"), Ed25519Config{}, l, reg, next),
				func(r *http.Request) { r.Header.Set(headerPortwingToken, "right") }
		},
		"ed25519": func(t *testing.T, next http.Handler, l *audit.Logger, reg *metrics.Registry) (http.Handler, func(*http.Request)) {
			rl := NewRateLimiter()
			t.Cleanup(rl.Stop)
			return rl.AuthMiddlewareWithEd25519(nil, ed, l, reg, next),
				func(r *http.Request) { signEd25519Request(t, r, nil, priv, time.Now().Unix(), freshNonce(t)) }
		},
	}

	for name, build := range paths {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			logPath := t.TempDir() + "/audit.log"
			l, closeLog, err := audit.New(logPath, 0)
			if err != nil {
				t.Fatal(err)
			}
			reg := metrics.NewRegistry()
			aborting := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTeapot)
				panic(http.ErrAbortHandler)
			})
			h, prepare := build(t, aborting, l, reg)
			req := httptest.NewRequest(http.MethodGet, "/containers/json", nil)
			prepare(req)

			func() {
				defer func() {
					rec := recover()
					if err, _ := rec.(error); !errors.Is(err, http.ErrAbortHandler) {
						t.Fatalf("recovered %v, want http.ErrAbortHandler", rec)
					}
				}()
				h.ServeHTTP(httptest.NewRecorder(), req)
			}()
			closeLog()

			data, _ := readFile(logPath)
			if got := strings.Count(data, audit.EventAPIRequest); got != 1 || !strings.Contains(data, "418") {
				t.Fatalf("want one api_request record with status 418, got %d:\n%s", got, data)
			}
			var out strings.Builder
			reg.WritePrometheus(&out, metrics.EscapeLabelValue)
			if !strings.Contains(out.String(), `portwing_http_requests_total{method="GET",code="418"} 1`) {
				t.Fatalf("completion metric missing:\n%s", out.String())
			}
		})
	}
}

// Any other panic is turned into a 500 by RecoveryMiddleware, so it must not
// be recorded as an allowed request at the recorder's default 200.
func TestAuthMiddlewareDoesNotRecordOtherPanicsAsAllowed(t *testing.T) {
	t.Parallel()

	logPath := t.TempDir() + "/audit.log"
	l, closeLog, err := audit.New(logPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	reg := metrics.NewRegistry()
	rl := NewRateLimiter()
	t.Cleanup(rl.Stop)
	h := rl.AuthMiddlewareWithEd25519(nil, Ed25519Config{}, l, reg, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	func() {
		defer func() {
			if rec := recover(); rec != "boom" {
				t.Fatalf("recovered %v, want boom", rec)
			}
		}()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	}()
	closeLog()

	if data, _ := readFile(logPath); strings.Contains(data, audit.EventAPIRequest) {
		t.Fatalf("panic was audited as an allowed request:\n%s", data)
	}
	var out strings.Builder
	reg.WritePrometheus(&out, metrics.EscapeLabelValue)
	if strings.Contains(out.String(), `portwing_http_requests_total{method="GET"`) {
		t.Fatalf("panic was counted as a completed request:\n%s", out.String())
	}
}

// net/http only treats the exact http.ErrAbortHandler value as an abort, so a
// wrapped sentinel is a real panic and must not be recorded as allowed.
func TestAuthMiddlewareDoesNotRecordWrappedAbortAsAllowed(t *testing.T) {
	t.Parallel()

	logPath := t.TempDir() + "/audit.log"
	l, closeLog, err := audit.New(logPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	reg := metrics.NewRegistry()
	rl := NewRateLimiter()
	t.Cleanup(rl.Stop)
	wrapped := fmt.Errorf("wrap: %w", http.ErrAbortHandler)
	h := rl.AuthMiddlewareWithEd25519(nil, Ed25519Config{}, l, reg, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(wrapped)
	}))

	func() {
		defer func() {
			if rec := recover(); rec != any(wrapped) {
				t.Fatalf("recovered %v, want the wrapped error", rec)
			}
		}()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	}()
	closeLog()

	if data, _ := readFile(logPath); strings.Contains(data, audit.EventAPIRequest) {
		t.Fatalf("wrapped abort was audited as an allowed request:\n%s", data)
	}
	var out strings.Builder
	reg.WritePrometheus(&out, metrics.EscapeLabelValue)
	if strings.Contains(out.String(), `portwing_http_requests_total{method="GET"`) {
		t.Fatalf("wrapped abort was counted as a completed request:\n%s", out.String())
	}
}
