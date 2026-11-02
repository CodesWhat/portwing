// SPDX-License-Identifier: MIT

package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/auth"
)

const v3TestAgentID = "v3-test-agent-0001"

// newV3Server starts a standard-mode server with Ed25519 auth, a fixed agent
// ID and a state directory. mutate may adjust the config before NewServer.
func newV3Server(t *testing.T, stateDir string, minVersion int, rotation bool) (*Server, ed25519.PrivateKey) {
	t.Helper()
	return newV3ServerWith(t, stateDir, minVersion, rotation, "")
}

func newV3ServerWith(t *testing.T, stateDir string, minVersion int, rotation bool, v3Unavailable string) (*Server, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cfg := minimalConfig()
	cfg.AuthorizedKeysFile = writeAuthorizedKeys(t, pub)
	cfg.AllowUnauthenticated = false
	cfg.AuditBufferSize = 64
	cfg.AgentID = v3TestAgentID
	cfg.StateDir = stateDir
	cfg.SignatureMinVersion = minVersion
	cfg.SignatureV3Unavailable = v3Unavailable
	if rotation {
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

func signV3(t *testing.T, req *http.Request, body []byte, priv ed25519.PrivateKey, audience string) {
	t.Helper()
	signEd25519Request(t, req, body, priv, time.Now().Unix(), freshNonce(t))
	msg := auth.CanonicalMessageV3(audience, req.Method, auth.CanonicalRequestTarget(req.URL),
		auth.BodyHashHex(body), mustUnix(t, req), req.Header.Get(auth.HeaderNonce))
	req.Header.Set(auth.HeaderSignature, base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, msg)))
	req.Header.Set(auth.HeaderSignatureVersion, auth.SignatureVersion3)
}

func mustUnix(t *testing.T, req *http.Request) int64 {
	t.Helper()
	var n int64
	if err := json.Unmarshal([]byte(req.Header.Get(auth.HeaderTimestamp)), &n); err != nil {
		t.Fatal(err)
	}
	return n
}

func doV3(s *Server, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(rec, req)
	return rec
}

func infoRequest(t *testing.T, version string, priv ed25519.PrivateKey, audience string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/_portwing/info", nil)
	switch version {
	case "3":
		signV3(t, req, nil, priv, audience)
	case "2":
		signEd25519Request(t, req, nil, priv, time.Now().Unix(), freshNonce(t))
	case "1":
		signEd25519Request(t, req, nil, priv, time.Now().Unix(), freshNonce(t))
		req.Header.Del(auth.HeaderSignatureVersion)
	}
	return req
}

func TestInfoReturnsAgentIDToV2AndV3Signers(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ version, audience string }{{"2", ""}, {"1", ""}, {"3", v3TestAgentID}} {
		// A fresh server per version: once v3 locks the key, v2 and legacy
		// are refused by design (see the lock tests below).
		s, priv := newV3Server(t, t.TempDir(), 0, false)
		rec := doV3(s, infoRequest(t, tc.version, priv, tc.audience))
		if rec.Code != 200 {
			t.Fatalf("version %s: %d %s", tc.version, rec.Code, rec.Body.String())
		}
		var info map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
			t.Fatal(err)
		}
		if info["agentId"] != v3TestAgentID {
			t.Fatalf("version %s: agentId = %v, want %s", tc.version, info["agentId"], v3TestAgentID)
		}
	}
}

func TestInfoRequiresAuthentication(t *testing.T) {
	t.Parallel()
	s, _ := newV3Server(t, t.TempDir(), 0, false)
	rec := doV3(s, httptest.NewRequest(http.MethodGet, "/_portwing/info", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated info = %d, want 401", rec.Code)
	}
}

func TestV3WrongAudienceIsRejectedWithDistinctReason(t *testing.T) {
	t.Parallel()
	s, priv := newV3Server(t, t.TempDir(), 0, false)

	req := infoRequest(t, "3", priv, "some-other-agent")
	rec := doV3(s, req)
	if rec.Code != 401 || rec.Header().Get(auth.HeaderReason) != "invalid-signature" {
		t.Fatalf("cross-agent replay without header: %d reason %q", rec.Code, rec.Header().Get(auth.HeaderReason))
	}

	req = infoRequest(t, "3", priv, "some-other-agent")
	req.Header.Set(auth.HeaderAudience, "some-other-agent")
	rec = doV3(s, req)
	if rec.Code != 401 || rec.Header().Get(auth.HeaderReason) != "audience-mismatch" {
		t.Fatalf("cross-agent replay with header: %d reason %q", rec.Code, rec.Header().Get(auth.HeaderReason))
	}
}

func TestV3LocksKeyAndRefusesDowngradeWithAudit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, priv := newV3Server(t, dir, 0, false)

	if rec := doV3(s, infoRequest(t, "2", priv, "")); rec.Code != 200 {
		t.Fatalf("v2 before lock: %d", rec.Code)
	}
	if rec := doV3(s, infoRequest(t, "3", priv, v3TestAgentID)); rec.Code != 200 {
		t.Fatalf("v3: %d", rec.Code)
	}
	// The second v3 request must not record a second lock event.
	if rec := doV3(s, infoRequest(t, "3", priv, v3TestAgentID)); rec.Code != 200 {
		t.Fatalf("second v3: %d", rec.Code)
	}

	for _, version := range []string{"2", "1"} {
		rec := doV3(s, infoRequest(t, version, priv, ""))
		if rec.Code != 401 || rec.Header().Get(auth.HeaderReason) != "signature-locked" {
			t.Fatalf("v%s after lock: %d reason %q", version, rec.Code, rec.Header().Get(auth.HeaderReason))
		}
		body := rec.Body.String()
		if !strings.Contains(body, "key locked to v3 since ") || !strings.Contains(body, "portwing signature-lock clear ") {
			t.Fatalf("lock body = %q", body)
		}
		if strings.Contains(body, base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey))) {
			t.Fatalf("lock body leaks key material: %q", body)
		}
	}

	var locks int
	for _, r := range s.auditor.Records(0) {
		if r.Event == audit.EventSignatureLock {
			locks++
			if r.Operation != "lock" || r.Outcome != audit.OutcomeAllowed || !auth.ValidKeyID(r.KeyID) {
				t.Fatalf("lock audit record = %+v", r)
			}
		}
	}
	if locks != 1 {
		t.Fatalf("signature_lock audit events = %d, want exactly 1", locks)
	}

	// Durable: a new server over the same state directory still refuses v2.
	pubKeyFile := s.cfg.AuthorizedKeysFile
	cfg := *s.cfg
	cfg.AuthorizedKeysFile = pubKeyFile
	client, stop := newStubDockerClient(t)
	t.Cleanup(stop)
	restarted, err := NewServer(&cfg, client, &stubServerAdapter{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Shutdown(context.Background()) })
	rec := doV3(restarted, infoRequest(t, "2", priv, ""))
	if rec.Code != 401 || rec.Header().Get(auth.HeaderReason) != "signature-locked" {
		t.Fatalf("v2 after restart: %d reason %q", rec.Code, rec.Header().Get(auth.HeaderReason))
	}
}

func TestV3LockClearedByOperatorTakesEffectWithoutRestart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, priv := newV3Server(t, dir, 0, false)
	if rec := doV3(s, infoRequest(t, "3", priv, v3TestAgentID)); rec.Code != 200 {
		t.Fatalf("v3: %d", rec.Code)
	}
	if rec := doV3(s, infoRequest(t, "2", priv, "")); rec.Code != 401 {
		t.Fatalf("v2 should be locked, got %d", rec.Code)
	}
	cli, err := auth.OpenSignatureLocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := cli.ClearAll(); err != nil || n != 1 {
		t.Fatalf("ClearAll = %d, %v", n, err)
	}
	if rec := doV3(s, infoRequest(t, "2", priv, "")); rec.Code != 200 {
		t.Fatalf("v2 after operator clear: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSignatureMinVersionRefusesLowerVersionsForAllKeys(t *testing.T) {
	t.Parallel()
	s, priv := newV3Server(t, t.TempDir(), 3, false)
	for _, version := range []string{"2", "1"} {
		rec := doV3(s, infoRequest(t, version, priv, ""))
		if rec.Code != 401 || rec.Header().Get(auth.HeaderReason) != "signature-version-too-low" {
			t.Fatalf("v%s under min 3: %d reason %q", version, rec.Code, rec.Header().Get(auth.HeaderReason))
		}
		if !strings.Contains(rec.Body.String(), "minimum 3") {
			t.Fatalf("body = %q", rec.Body.String())
		}
	}
	if rec := doV3(s, infoRequest(t, "3", priv, v3TestAgentID)); rec.Code != 200 {
		t.Fatalf("v3 under min 3: %d", rec.Code)
	}
}

func TestRotationAcceptsV3Signatures(t *testing.T) {
	t.Parallel()
	s, priv := newV3Server(t, t.TempDir(), 0, true)
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload := `{"public_key":"` + base64.StdEncoding.EncodeToString(pub) + `"}`

	for _, tc := range []struct {
		method, path, body string
		code               int
	}{
		{"GET", "/_portwing/auth/rotation", "", 200},
		{"POST", "/_portwing/auth/rotation/prepare", payload, 200},
		{"POST", "/_portwing/auth/rotation/abort", "{}", 200},
	} {
		req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
		signV3(t, req, []byte(tc.body), priv, v3TestAgentID)
		if rec := doV3(s, req); rec.Code != tc.code {
			t.Fatalf("v3 %s %s: %d %s, want %d", tc.method, tc.path, rec.Code, rec.Body.String(), tc.code)
		}
	}

	// Versions below 2 stay refused for rotation.
	req := httptest.NewRequest("GET", "/_portwing/auth/rotation", nil)
	signEd25519Request(t, req, nil, priv, time.Now().Unix(), freshNonce(t))
	req.Header.Set(auth.HeaderSignatureVersion, "4")
	if rec := doV3(s, req); rec.Code != 401 {
		t.Fatalf("unknown version: %d", rec.Code)
	}
}

func TestRotationStillRefusesLegacyUnversionedSignatures(t *testing.T) {
	t.Parallel()
	s, priv := newV3Server(t, t.TempDir(), 0, true)
	req := httptest.NewRequest("GET", "/_portwing/auth/rotation", nil)
	signEd25519Request(t, req, nil, priv, time.Now().Unix(), freshNonce(t))
	req.Header.Del(auth.HeaderSignatureVersion)
	if rec := doV3(s, req); rec.Code != http.StatusBadRequest {
		t.Fatalf("legacy rotation request: %d, want 400", rec.Code)
	}
}

func TestNewServerFailsOnCorruptSignatureLocks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// #nosec G302 -- private test fixture.
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, auth.SignatureLocksFileName), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	cfg := minimalConfig()
	cfg.AuthorizedKeysFile = writeAuthorizedKeys(t, pub)
	cfg.AllowUnauthenticated = false
	cfg.StateDir = dir
	client, stop := newStubDockerClient(t)
	t.Cleanup(stop)
	if _, err := NewServer(cfg, client, &stubServerAdapter{}); err == nil || !strings.Contains(err.Error(), "signature locks") {
		t.Fatalf("NewServer error = %v, want signature locks error", err)
	}
}

func TestInfoReportsV3AvailabilityAndReason(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		reason string
		v3     bool
	}{
		{"", true},
		{"agent-id-not-persistent", false},
		{"agent-id-not-v3-compatible", false},
	} {
		s, priv := newV3ServerWith(t, t.TempDir(), 0, false, tc.reason)
		rec := doV3(s, infoRequest(t, "2", priv, ""))
		if rec.Code != 200 {
			t.Fatalf("%q: %d", tc.reason, rec.Code)
		}
		var info map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
			t.Fatal(err)
		}
		if info["v3"] != tc.v3 {
			t.Fatalf("%q: v3 = %v, want %v", tc.reason, info["v3"], tc.v3)
		}
		reason, has := info["v3Reason"]
		if tc.v3 && has {
			t.Fatalf("v3Reason must be absent when v3 is available, got %v", reason)
		}
		if !tc.v3 && reason != tc.reason {
			t.Fatalf("v3Reason = %v, want %q", reason, tc.reason)
		}
		if info["agentId"] != v3TestAgentID {
			t.Fatalf("agentId = %v", info["agentId"])
		}
	}
}

func TestV3RefusedWhenUnavailableButV2StillWorks(t *testing.T) {
	t.Parallel()
	s, priv := newV3ServerWith(t, t.TempDir(), 0, false, "agent-id-not-persistent")
	rec := doV3(s, infoRequest(t, "3", priv, v3TestAgentID))
	if rec.Code != 401 || rec.Header().Get(auth.HeaderReason) != "agent-id-not-persistent" {
		t.Fatalf("v3: %d reason %q", rec.Code, rec.Header().Get(auth.HeaderReason))
	}
	if !strings.Contains(rec.Body.String(), "AGENT_ID") || !strings.Contains(rec.Body.String(), "/data/credentials") {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if rec := doV3(s, infoRequest(t, "2", priv, "")); rec.Code != 200 {
		t.Fatalf("v2: %d", rec.Code)
	}
	if rec := doV3(s, infoRequest(t, "1", priv, "")); rec.Code != 200 {
		t.Fatalf("legacy: %d", rec.Code)
	}
}
