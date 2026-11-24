// SPDX-License-Identifier: MIT

package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/audit"
)

// A rejected Ed25519 signature writes one auth_failure audit record naming the
// client, method and path; a signature that verifies writes an allowed
// api_request and no auth_failure.
func TestEd25519MiddlewareAuditsAuthFailure(t *testing.T) {
	t.Parallel()
	ed, priv := setupEd25519(t)
	logger, closeAudit, err := audit.New("", 16)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeAudit)
	h := NewRateLimiter().AuthMiddlewareWithEd25519(nil, ed, logger, nil, http.HandlerFunc(okHandler))

	// A key that is not in the registry.
	_, unknown, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bad := httptest.NewRequest(http.MethodPost, "/_portwing/compose", nil)
	bad.RemoteAddr = "192.0.2.10:5555"
	signEd25519Request(t, bad, nil, unknown, time.Now().Unix(), freshNonce(t))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, bad)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown key = %d, want 401", rec.Code)
	}

	records := logger.Records(0)
	if len(records) != 1 {
		t.Fatalf("records after rejected signature = %+v, want exactly one", records)
	}
	got := records[0]
	if got.Event != audit.EventAuthFailure || got.Actor != "192.0.2.10" || got.Method != http.MethodPost || got.Path != "/_portwing/compose" || got.Outcome != audit.OutcomeDenied {
		t.Fatalf("auth_failure record = %+v", got)
	}

	good := httptest.NewRequest(http.MethodGet, "/_portwing/info", nil)
	good.RemoteAddr = "192.0.2.11:5555"
	signEd25519Request(t, good, nil, priv, time.Now().Unix(), freshNonce(t))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, good)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid signature = %d, want 200", rec.Code)
	}
	for _, r := range logger.Records(0) {
		if r.Actor == "192.0.2.11" && r.Event != audit.EventAPIRequest {
			t.Fatalf("valid signature produced %q record: %+v", r.Event, r)
		}
	}
}
