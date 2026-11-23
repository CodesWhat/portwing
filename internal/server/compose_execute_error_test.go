// SPDX-License-Identifier: MIT

package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/auth"
	"github.com/codeswhat/portwing/internal/docker"
)

// failingCompose returns an error from Execute, which the real manager never does.
type failingCompose struct{}

func (failingCompose) Execute(context.Context, docker.ComposeRequest) (*docker.ComposeResponse, error) {
	return nil, errors.New("compose exploded")
}

// An Execute error answers 500 and records an error-outcome compose_op
// attributed to the Ed25519 key that signed the request.
func TestHandleComposeExecuteErrorAudits(t *testing.T) {
	t.Parallel()

	ed, priv := setupEd25519(t)
	auditor, closeAudit, err := audit.New("", 16)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeAudit)
	rl := NewRateLimiter()
	t.Cleanup(rl.Stop)
	s := &Server{compose: failingCompose{}, rateLimiter: rl, auditor: auditor, cfg: minimalConfig()}

	h := rl.AuthMiddlewareWithEd25519(nil, ed, auditor, nil, http.HandlerFunc(s.handleCompose))
	body := []byte(`{"operation":"up","stackName":"app"}`)
	req := httptest.NewRequest(http.MethodPost, "/_portwing/compose", bytes.NewReader(body))
	req.RemoteAddr = "192.0.2.7:4000"
	signEd25519Request(t, req, body, priv, time.Now().Unix(), freshNonce(t))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
	wantID := auth.KeyIDForPublicKey(priv.Public().(ed25519.PublicKey))
	var found bool
	for _, r := range auditor.Records(0) {
		if r.Event != audit.EventComposeOp {
			continue
		}
		found = true
		if r.Outcome != audit.OutcomeError || r.KeyID != wantID {
			t.Fatalf("compose_op record = %+v, want error outcome with key_id %q", r, wantID)
		}
	}
	if !found {
		t.Fatal("no compose_op record")
	}
}

// cancelledCompose reports a deploy whose request ended while compose ran.
type cancelledCompose struct{}

func (cancelledCompose) Execute(context.Context, docker.ComposeRequest) (*docker.ComposeResponse, error) {
	return &docker.ComposeResponse{Cancelled: true, Error: "signal: terminated"}, nil
}

// A deploy stopped by a client disconnect is audited as cancelled, not error.
func TestHandleComposeCancelledAudits(t *testing.T) {
	t.Parallel()

	auditor, closeAudit, err := audit.New("", 16)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeAudit)
	rl := NewRateLimiter()
	t.Cleanup(rl.Stop)
	s := &Server{compose: cancelledCompose{}, rateLimiter: rl, auditor: auditor, cfg: minimalConfig()}

	req := httptest.NewRequest(http.MethodPost, "/_portwing/compose", bytes.NewReader([]byte(`{"operation":"up","stackName":"app"}`)))
	req.RemoteAddr = "192.0.2.7:4000"
	s.handleCompose(httptest.NewRecorder(), req)

	for _, r := range auditor.Records(0) {
		if r.Event == audit.EventComposeOp {
			if r.Outcome != audit.OutcomeCancelled {
				t.Fatalf("compose_op outcome = %q, want %q", r.Outcome, audit.OutcomeCancelled)
			}
			return
		}
	}
	t.Fatal("no compose_op record")
}
