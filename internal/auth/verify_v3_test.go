// SPDX-License-Identifier: MIT

package auth

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const testAgentID = "0123456789abcdef0123456789abcdef"

// signRequestV3 signs r for audience with signature version 3.
func signRequestV3(t *testing.T, r *http.Request, body []byte, priv ed25519.PrivateKey, pub ed25519.PublicKey, audience string, tsUnix int64, nonce string) {
	t.Helper()
	signRequest(t, r, body, priv, pub, tsUnix, nonce)
	msg := CanonicalMessageV3(audience, r.Method, CanonicalRequestTarget(r.URL), BodyHashHex(body), tsUnix, nonce)
	r.Header.Set(HeaderSignature, base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, msg)))
	r.Header.Set(HeaderSignatureVersion, SignatureVersion3)
}

// v3Setup returns a registry whose policy audience is testAgentID.
func v3Setup(t *testing.T, policy SignaturePolicy) (*KeyRegistry, *NonceLRU, ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	reg, lru, pub, priv := testSetup(t)
	if policy.AgentID == "" {
		policy.AgentID = testAgentID
	}
	reg.SetSignaturePolicy(policy)
	return reg, lru, pub, priv
}

func verifyAt(t *testing.T, r *http.Request, body []byte, reg *KeyRegistry, lru *NonceLRU) (VerifyResult, error) {
	t.Helper()
	return VerifyRequestDetailed(r, body, reg, lru, 60)
}

func TestCanonicalMessageV3_Format(t *testing.T) {
	t.Parallel()
	got := string(CanonicalMessageV3("agent-1", "GET", "/api/x?a=1", emptyBodyHash, 1749600000, "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6"))
	want := "portwing-sig-v3\nagent-1\nGET\n/api/x?a=1\n" + emptyBodyHash + "\n1749600000\na1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestVerifyRequestV3_HappyPathWithQueryAndBody(t *testing.T) {
	reg, lru, pub, priv := v3Setup(t, SignaturePolicy{})
	body := []byte(`{"a":1}`)
	req := httptest.NewRequest(http.MethodPost, "/v1.44/containers/a%2Fb/logs?tail=10&stdout=1", nil)
	signRequestV3(t, req, body, priv, pub, testAgentID, time.Now().Unix(), randomNonce(t))
	res, err := verifyAt(t, req, body, reg, lru)
	if err != nil {
		t.Fatalf("v3 verify: %v", err)
	}
	if res.Version != 3 || res.KeyID != req.Header.Get(HeaderKeyID) {
		t.Fatalf("result = %+v", res)
	}
}

func TestVerifyRequestV3_QueryTamperFails(t *testing.T) {
	reg, lru, pub, priv := v3Setup(t, SignaturePolicy{})
	req := httptest.NewRequest(http.MethodGet, "/x?tail=10", nil)
	signRequestV3(t, req, nil, priv, pub, testAgentID, time.Now().Unix(), randomNonce(t))
	req.URL.RawQuery = "tail=11"
	if _, err := verifyAt(t, req, nil, reg, lru); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

func TestVerifyRequestV3_BodyTamperFails(t *testing.T) {
	reg, lru, pub, priv := v3Setup(t, SignaturePolicy{})
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	signRequestV3(t, req, []byte("a"), priv, pub, testAgentID, time.Now().Unix(), randomNonce(t))
	if _, err := verifyAt(t, req, []byte("b"), reg, lru); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

func TestVerifyRequestV3_NeverVerifiesAsOtherVersions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header string
		query  string
	}{
		{"as v2", SignatureVersion2, "?a=1"},
		{"as legacy", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg, lru, pub, priv := v3Setup(t, SignaturePolicy{})
			req := httptest.NewRequest(http.MethodGet, "/x"+tc.query, nil)
			signRequestV3(t, req, nil, priv, pub, testAgentID, time.Now().Unix(), randomNonce(t))
			if tc.header == "" {
				req.Header.Del(HeaderSignatureVersion)
			} else {
				req.Header.Set(HeaderSignatureVersion, tc.header)
			}
			if _, err := verifyAt(t, req, nil, reg, lru); !errors.Is(err, ErrBadSignature) {
				t.Fatalf("v3 signature accepted as %s: err = %v", tc.name, err)
			}
		})
	}
}

func TestVerifyRequestV3_V2AndLegacyNeverVerifyAsV3(t *testing.T) {
	reg, lru, pub, priv := v3Setup(t, SignaturePolicy{})
	req := httptest.NewRequest(http.MethodGet, "/x?a=1", nil)
	signRequest(t, req, nil, priv, pub, time.Now().Unix(), randomNonce(t))
	req.Header.Set(HeaderSignatureVersion, SignatureVersion3)
	if _, err := verifyAt(t, req, nil, reg, lru); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("v2 signature accepted as v3: err = %v", err)
	}
}

func TestVerifyRequestV3_CrossAgentReplayRejected(t *testing.T) {
	// Signed for agent A, presented to agent B (same controller key).
	reg, lru, pub, priv := v3Setup(t, SignaturePolicy{AgentID: "agent-b"})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	signRequestV3(t, req, nil, priv, pub, "agent-a", time.Now().Unix(), randomNonce(t))
	_, err := verifyAt(t, req, nil, reg, lru)
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("without an audience header the mismatch is an invalid signature, got %v", err)
	}
}

func TestVerifyRequestV3_AudienceHeaderMismatchHasDistinctReason(t *testing.T) {
	reg, lru, pub, priv := v3Setup(t, SignaturePolicy{AgentID: "agent-b"})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	signRequestV3(t, req, nil, priv, pub, "agent-a", time.Now().Unix(), randomNonce(t))
	req.Header.Set(HeaderAudience, "agent-a")
	_, err := verifyAt(t, req, nil, reg, lru)
	if !errors.Is(err, ErrAudienceMismatch) {
		t.Fatalf("err = %v, want ErrAudienceMismatch", err)
	}
	if got := ReasonFor(err); got != "audience-mismatch" {
		t.Fatalf("reason = %q", got)
	}
	// The rejected request must not burn its nonce.
	if lru.Seen(req.Header.Get(HeaderNonce)) {
		t.Fatal("audience mismatch recorded the nonce")
	}
}

func TestVerifyRequestV3_AudienceHeaderMatchingIsAccepted(t *testing.T) {
	reg, lru, pub, priv := v3Setup(t, SignaturePolicy{})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	signRequestV3(t, req, nil, priv, pub, testAgentID, time.Now().Unix(), randomNonce(t))
	req.Header.Set(HeaderAudience, testAgentID)
	if _, err := verifyAt(t, req, nil, reg, lru); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestVerifyRequestV3_AudienceHeaderWithValidSignatureForOtherIDRejected(t *testing.T) {
	// A header naming this agent cannot rescue a signature made for another.
	reg, lru, pub, priv := v3Setup(t, SignaturePolicy{AgentID: "agent-b"})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	signRequestV3(t, req, nil, priv, pub, "agent-a", time.Now().Unix(), randomNonce(t))
	req.Header.Set(HeaderAudience, "agent-b")
	if _, err := verifyAt(t, req, nil, reg, lru); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

func TestVerifyRequestV3_NoAudienceConfiguredRejects(t *testing.T) {
	reg, lru, pub, priv := testSetup(t) // no policy
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	signRequestV3(t, req, nil, priv, pub, "", time.Now().Unix(), randomNonce(t))
	if _, err := verifyAt(t, req, nil, reg, lru); !errors.Is(err, ErrAudienceMismatch) {
		t.Fatalf("err = %v, want ErrAudienceMismatch", err)
	}
}

func TestVerifyRequest_UnknownVersionRejected(t *testing.T) {
	reg, lru, pub, priv := v3Setup(t, SignaturePolicy{})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	signRequestV3(t, req, nil, priv, pub, testAgentID, time.Now().Unix(), randomNonce(t))
	req.Header.Set(HeaderSignatureVersion, "4")
	if _, err := verifyAt(t, req, nil, reg, lru); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v", err)
	}
}

func TestVerifyRequestV3_LocksKeyAndRefusesDowngrade(t *testing.T) {
	locks, err := OpenSignatureLocks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg, lru, pub, priv := v3Setup(t, SignaturePolicy{Locks: locks})

	v2 := func() error {
		req := httptest.NewRequest(http.MethodGet, "/x?a=1", nil)
		signRequest(t, req, nil, priv, pub, time.Now().Unix(), randomNonce(t))
		_, err := verifyAt(t, req, nil, reg, lru)
		return err
	}
	legacy := func() error {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		signRequest(t, req, nil, priv, pub, time.Now().Unix(), randomNonce(t))
		req.Header.Del(HeaderSignatureVersion)
		// legacy signs the query-free path; signRequest used the v2 target,
		// which is identical for a query-free request.
		_, err := verifyAt(t, req, nil, reg, lru)
		return err
	}
	v3 := func() (VerifyResult, error) {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		signRequestV3(t, req, nil, priv, pub, testAgentID, time.Now().Unix(), randomNonce(t))
		return verifyAt(t, req, nil, reg, lru)
	}

	if err := v2(); err != nil {
		t.Fatalf("v2 before any v3: %v", err)
	}
	if err := legacy(); err != nil {
		t.Fatalf("legacy before any v3: %v", err)
	}
	res, err := v3()
	if err != nil || !res.NewlyLocked {
		t.Fatalf("first v3 = %+v, %v; want NewlyLocked", res, err)
	}
	res, err = v3()
	if err != nil || res.NewlyLocked {
		t.Fatalf("second v3 = %+v, %v; want not NewlyLocked", res, err)
	}
	for name, fn := range map[string]func() error{"v2": v2, "legacy": legacy} {
		err := fn()
		if !errors.Is(err, ErrSignatureLocked) {
			t.Fatalf("%s after lock: err = %v, want ErrSignatureLocked", name, err)
		}
		if got := ReasonFor(err); got != "signature-locked" {
			t.Fatalf("reason = %q", got)
		}
		detail := RejectionDetail(err)
		kid := reg.keysForTest()
		if !strings.Contains(detail, "key locked to v3 since ") || !strings.Contains(detail, "portwing signature-lock clear "+kid) {
			t.Fatalf("detail = %q", detail)
		}
	}
	if _, locked := locks.LockedSince(mustKeyID(t, pub)); !locked {
		t.Fatal("lock missing from the store")
	}

	// Clearing re-opens v2 until the next v3 request.
	if _, err := locks.Clear(mustKeyID(t, pub)); err != nil {
		t.Fatal(err)
	}
	if err := v2(); err != nil {
		t.Fatalf("v2 after clear: %v", err)
	}
}

func mustKeyID(t *testing.T, pub ed25519.PublicKey) string {
	t.Helper()
	return KeyIDForPublicKey(pub)
}

// keysForTest returns the only key ID in the registry.
func (r *KeyRegistry) keysForTest() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for id := range r.keys {
		return id
	}
	return ""
}

func TestVerifyRequestV3_LockIsPerKey(t *testing.T) {
	locks, _ := OpenSignatureLocks(t.TempDir())
	reg, lru, pub, priv := v3Setup(t, SignaturePolicy{Locks: locks})
	otherPub, otherPriv, _ := ed25519.GenerateKey(nil)
	reg.mu.Lock()
	otherID := KeyIDForPublicKey(otherPub)
	reg.keys[otherID] = &AuthorizedKey{KeyID: otherID, PubKey: otherPub}
	reg.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	signRequestV3(t, req, nil, priv, pub, testAgentID, time.Now().Unix(), randomNonce(t))
	if _, err := verifyAt(t, req, nil, reg, lru); err != nil {
		t.Fatal(err)
	}
	req2 := httptest.NewRequest(http.MethodGet, "/x", nil)
	signRequest(t, req2, nil, otherPriv, otherPub, time.Now().Unix(), randomNonce(t))
	if _, err := verifyAt(t, req2, nil, reg, lru); err != nil {
		t.Fatalf("the other key must still sign v2: %v", err)
	}
}

func TestVerifyRequest_LockStateNotLeakedWithoutValidSignature(t *testing.T) {
	locks, _ := OpenSignatureLocks(t.TempDir())
	reg, lru, pub, priv := v3Setup(t, SignaturePolicy{Locks: locks})
	if _, err := locks.Lock(KeyIDForPublicKey(pub), time.Now()); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	signRequest(t, req, nil, priv, pub, time.Now().Unix(), randomNonce(t))
	req.Header.Set(HeaderSignature, base64.RawURLEncoding.EncodeToString(make([]byte, 64)))
	_, err := verifyAt(t, req, nil, reg, lru)
	if !errors.Is(err, ErrBadSignature) || errors.Is(err, ErrSignatureLocked) {
		t.Fatalf("forged signature on a locked key: err = %v, want plain ErrBadSignature", err)
	}
}

func TestVerifyRequest_LockedRejectionDoesNotBurnNonce(t *testing.T) {
	locks, _ := OpenSignatureLocks(t.TempDir())
	reg, lru, pub, priv := v3Setup(t, SignaturePolicy{Locks: locks})
	_, _ = locks.Lock(KeyIDForPublicKey(pub), time.Now())
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	signRequest(t, req, nil, priv, pub, time.Now().Unix(), randomNonce(t))
	if _, err := verifyAt(t, req, nil, reg, lru); !errors.Is(err, ErrSignatureLocked) {
		t.Fatalf("err = %v", err)
	}
	if lru.Seen(req.Header.Get(HeaderNonce)) {
		t.Fatal("a policy rejection recorded the nonce")
	}
}

func TestVerifyRequest_MinVersion(t *testing.T) {
	for _, tc := range []struct {
		min       int
		legacyOK  bool
		v2OK      bool
		v3OK      bool
		wantWords string
	}{
		{0, true, true, true, ""},
		{1, true, true, true, ""},
		{2, false, true, true, "minimum 2"},
		{3, false, false, true, "minimum 3"},
	} {
		reg, lru, pub, priv := v3Setup(t, SignaturePolicy{MinVersion: tc.min})
		check := func(name string, ok bool, req *http.Request) {
			t.Helper()
			_, err := verifyAt(t, req, nil, reg, lru)
			if ok && err != nil {
				t.Errorf("min=%d %s: %v", tc.min, name, err)
			}
			if !ok {
				if !errors.Is(err, ErrSignatureVersionTooLow) {
					t.Errorf("min=%d %s: err = %v, want ErrSignatureVersionTooLow", tc.min, name, err)
					return
				}
				if ReasonFor(err) != "signature-version-too-low" || !strings.Contains(RejectionDetail(err), tc.wantWords) {
					t.Errorf("min=%d %s: reason %q detail %q", tc.min, name, ReasonFor(err), RejectionDetail(err))
				}
			}
		}
		legacy := httptest.NewRequest(http.MethodGet, "/x", nil)
		signRequest(t, legacy, nil, priv, pub, time.Now().Unix(), randomNonce(t))
		legacy.Header.Del(HeaderSignatureVersion)
		check("legacy", tc.legacyOK, legacy)
		v2 := httptest.NewRequest(http.MethodGet, "/x?a=1", nil)
		signRequest(t, v2, nil, priv, pub, time.Now().Unix(), randomNonce(t))
		check("v2", tc.v2OK, v2)
		v3 := httptest.NewRequest(http.MethodGet, "/x?a=1", nil)
		signRequestV3(t, v3, nil, priv, pub, testAgentID, time.Now().Unix(), randomNonce(t))
		check("v3", tc.v3OK, v3)
	}
}

func TestVerifyRequest_MinVersionDoesNotLeakToForgedSignatures(t *testing.T) {
	reg, lru, pub, priv := v3Setup(t, SignaturePolicy{MinVersion: 3})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	signRequest(t, req, nil, priv, pub, time.Now().Unix(), randomNonce(t))
	req.Header.Set(HeaderSignature, base64.RawURLEncoding.EncodeToString(make([]byte, 64)))
	if _, err := verifyAt(t, req, nil, reg, lru); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

func TestVerifyRequestV3_LockPersistErrorSurfacedNotFatal(t *testing.T) {
	dir := t.TempDir()
	locks, _ := OpenSignatureLocks(dir)
	reg, lru, pub, priv := v3Setup(t, SignaturePolicy{Locks: locks})
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	signRequestV3(t, req, nil, priv, pub, testAgentID, time.Now().Unix(), randomNonce(t))
	res, err := verifyAt(t, req, nil, reg, lru)
	if err != nil {
		t.Fatalf("a failed lock write must not fail the request: %v", err)
	}
	if !res.NewlyLocked || res.LockErr == nil {
		t.Fatalf("result = %+v, want NewlyLocked with LockErr", res)
	}
	if _, locked := locks.LockedSince(KeyIDForPublicKey(pub)); !locked {
		t.Fatal("lock must hold in memory")
	}
}

func TestVerifyRequest_WrapperMatchesDetailed(t *testing.T) {
	reg, lru, pub, priv := v3Setup(t, SignaturePolicy{})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	signRequestV3(t, req, nil, priv, pub, testAgentID, time.Now().Unix(), randomNonce(t))
	kid, err := VerifyRequest(req, nil, reg, lru, 60)
	if err != nil || kid != KeyIDForPublicKey(pub) {
		t.Fatalf("VerifyRequest = %q, %v", kid, err)
	}
	req2 := httptest.NewRequest(http.MethodGet, "/x", nil)
	kid, err = VerifyRequest(req2, nil, reg, lru, 60)
	if kid != "" || !errors.Is(err, ErrMissingHeaders) {
		t.Fatalf("VerifyRequest failure = %q, %v", kid, err)
	}
}

func TestReasonForAndDetail_NewErrors(t *testing.T) {
	t.Parallel()
	since := time.Date(2026, 11, 2, 12, 30, 0, 0, time.UTC)
	locked := &SignatureLockedError{KeyID: "0123456789abcdef", Since: since}
	if ReasonFor(locked) != "signature-locked" {
		t.Fatalf("locked reason = %q", ReasonFor(locked))
	}
	if got, want := RejectionDetail(locked), "key locked to v3 since 2026-11-02T12:30:00Z; clear with portwing signature-lock clear 0123456789abcdef"; got != want {
		t.Fatalf("detail = %q, want %q", got, want)
	}
	low := &SignatureVersionError{Got: 2, Min: 3}
	if ReasonFor(low) != "signature-version-too-low" || !strings.Contains(RejectionDetail(low), "minimum 3") {
		t.Fatalf("low reason %q detail %q", ReasonFor(low), RejectionDetail(low))
	}
	if ReasonFor(ErrAudienceMismatch) != "audience-mismatch" {
		t.Fatal("audience reason")
	}
	for _, err := range []error{ErrBadSignature, ErrNonceReplay, nil} {
		if RejectionDetail(err) != "unauthorized" {
			t.Fatalf("detail for %v = %q, want unauthorized", err, RejectionDetail(err))
		}
	}
}

func TestSetSignaturePolicy_ReplacesPolicy(t *testing.T) {
	t.Parallel()
	reg := NewKeyRegistry("")
	if p := reg.signaturePolicy(); p.AgentID != "" || p.MinVersion != 0 || p.Locks != nil {
		t.Fatalf("zero registry policy = %+v", p)
	}
	locks := NewMemorySignatureLocks()
	reg.SetSignaturePolicy(SignaturePolicy{AgentID: "a", MinVersion: 3, Locks: locks})
	if p := reg.signaturePolicy(); p.AgentID != "a" || p.MinVersion != 3 || p.Locks != locks {
		t.Fatalf("policy = %+v", p)
	}
}
