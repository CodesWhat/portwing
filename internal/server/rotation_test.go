// SPDX-License-Identifier: MIT

package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/auth"
)

func newRotationServer(t *testing.T, enabled bool) (*Server, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cfg := minimalConfig()
	cfg.AuthorizedKeysFile = writeAuthorizedKeys(t, pub)
	cfg.Token = "secondary-credential"
	cfg.AllowUnauthenticated = false
	cfg.AuditBufferSize = 64
	if enabled {
		cfg.KeyRotationDir = filepath.Join(t.TempDir(), "rotation")
	}
	client, stop := newStubDockerClient(t)
	t.Cleanup(stop)
	s, err := NewServer(cfg, client, &stubServerAdapter{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	return s, priv
}

func rotationRequest(t *testing.T, s *Server, priv ed25519.PrivateKey, method, path, payload string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(payload))
	req.Header.Set(auth.HeaderSignatureVersion, auth.SignatureVersion2)
	signEd25519Request(t, req, []byte(payload), priv, time.Now().Unix(), freshNonce(t))
	rec := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(rec, req)
	return rec
}

func TestRotationHTTPRequiresReplacementSignature(t *testing.T) {
	t.Parallel()
	s, old := newRotationServer(t, true)
	pub, next, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload := fmt.Sprintf(`{"public_key":%q}`, base64.StdEncoding.EncodeToString(pub))
	for _, tc := range []struct {
		key    ed25519.PrivateKey
		method string
		path   string
		body   string
		code   int
	}{
		{old, "POST", "/_portwing/auth/rotation/prepare", payload, 200},
		{old, "POST", "/_portwing/auth/rotation/commit", "{}", 403},
		{next, "GET", "/_portwing/auth/rotation", "", 200},
		{next, "POST", "/_portwing/auth/rotation/commit", "{}", 200},
		{old, "GET", "/_portwing/auth/rotation", "", 401},
		{next, "POST", "/_portwing/auth/rotation/commit", "{}", 200},
	} {
		rec := rotationRequest(t, s, tc.key, tc.method, tc.path, tc.body)
		if rec.Code != tc.code {
			t.Fatalf("%s %s: %d %s, want %d", tc.method, tc.path, rec.Code, rec.Body.String(), tc.code)
		}
		if rec.Code == 401 && rec.Header().Get(auth.HeaderReason) != "unknown-key" {
			t.Fatalf("retired key reason: %v", rec.Header())
		}
	}
	status := rotationRequest(t, s, next, "GET", "/_portwing/auth/rotation", "")
	var got auth.RotationStatus
	if err := json.Unmarshal(status.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.CurrentKeyID != auth.KeyIDForPublicKey(pub) || got.PendingKeyID != "" {
		t.Fatalf("unexpected committed status %+v", got)
	}
	var committed, denied bool
	for _, record := range s.auditor.Records(64) {
		if record.Event == "key_rotation" && record.Operation == "commit" {
			committed = committed || (record.Outcome == "allowed" && record.KeyID == got.CurrentKeyID)
			denied = denied || (record.Outcome == "denied" && record.KeyID == auth.KeyIDForPublicKey(old.Public().(ed25519.PublicKey)))
		}
	}
	if !committed || !denied {
		t.Fatalf("rotation audit missing allowed or denied decision: %v", s.auditor.Records(64))
	}
}

func TestRotationHTTPRejectsBearerSpoofAndReplay(t *testing.T) {
	t.Parallel()
	s, old := newRotationServer(t, true)
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload := fmt.Sprintf(`{"public_key":%q}`, base64.StdEncoding.EncodeToString(pub))
	spoof := httptest.NewRequest("POST", "/_portwing/auth/rotation/prepare", bytes.NewBufferString(payload))
	spoof.Header.Set("Authorization", "Bearer secondary-credential")
	spoof.Header.Set(auth.HeaderKeyID, auth.KeyIDForPublicKey(old.Public().(ed25519.PublicKey)))
	spoof.Header.Set(auth.HeaderSignatureVersion, auth.SignatureVersion2)
	rec := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(rec, spoof)
	if rec.Code != 403 {
		t.Fatalf("bearer caller spoofing a key: %d %s", rec.Code, rec.Body.String())
	}
	request := httptest.NewRequest("POST", "/_portwing/auth/rotation/prepare", bytes.NewBufferString(payload))
	request.Header.Set(auth.HeaderSignatureVersion, auth.SignatureVersion2)
	signEd25519Request(t, request, []byte(payload), old, time.Now().Unix(), freshNonce(t))
	for _, code := range []int{200, 401} {
		copy := request.Clone(context.Background())
		copy.Body = httptest.NewRequest("POST", "/", bytes.NewBufferString(payload)).Body
		rec = httptest.NewRecorder()
		s.httpServer.Handler.ServeHTTP(rec, copy)
		if rec.Code != code {
			t.Fatalf("replay: %d, want %d: %s", rec.Code, code, rec.Body.String())
		}
	}
	if rec.Header().Get(auth.HeaderReason) != "replay" {
		t.Fatalf("replay rejection reason: %v", rec.Header())
	}
}

func TestRotationReservedRoutesAndValidation(t *testing.T) {
	t.Parallel()
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("enabled=%v", enabled), func(t *testing.T) {
			s, key := newRotationServer(t, enabled)
			cases := []struct {
				method, path, body string
				code               int
			}{
				{"GET", "/_portwing/auth/rotation", "", 200},
				{"GET", "/_portwing/auth/rotation/prepare", "", 405},
				{"POST", "/_portwing/auth/rotation/prepare", `{}`, 400},
				{"POST", "/_portwing/auth/rotation/prepare", `{"public_key":"bad"}`, 400},
				{"POST", "/_portwing/auth/rotation/abort", `{} {}`, 400},
				{"POST", "/_portwing/auth/rotation/abort", `{"unknown":true}`, 400},
				{"POST", "/_portwing/auth/rotation/commit", "{}", 200},
				{"GET", "/_portwing/auth/rotation/unknown", "", 404},
				{"GET", "/_portwing/auth/rotation?other=1", "", 400},
			}
			for _, tc := range cases {
				want := tc.code
				if !enabled {
					want = 404
				}
				rec := rotationRequest(t, s, key, tc.method, tc.path, tc.body)
				if rec.Code != want {
					t.Fatalf("%s %s: %d %s, want %d", tc.method, tc.path, rec.Code, rec.Body.String(), want)
				}
			}
		})
	}
}
