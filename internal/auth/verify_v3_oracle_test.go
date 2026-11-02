// SPDX-License-Identifier: MIT

package auth

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func forgeSignature(req *http.Request) {
	req.Header.Set(HeaderSignature, base64.RawURLEncoding.EncodeToString(make([]byte, 64)))
}

// Every v3-specific rejection reason is reachable only with a valid signature;
// an unsigned or forged caller always sees invalid-signature with no hint.
func TestVerifyRequestV3_ForgedSignatureNeverGetsV3SpecificReason(t *testing.T) {
	for _, tc := range []struct {
		name     string
		policy   SignaturePolicy
		audience string
	}{
		{"wrong audience header", SignaturePolicy{}, "some-other-agent"},
		{"matching audience header", SignaturePolicy{}, testAgentID},
		{"v3 unavailable, not persistent", SignaturePolicy{V3Unavailable: "agent-id-not-persistent"}, ""},
		{"v3 unavailable, state not writable", SignaturePolicy{V3Unavailable: "state-not-writable"}, ""},
		{"v3 unavailable, incompatible ID", SignaturePolicy{V3Unavailable: "agent-id-not-v3-compatible"}, ""},
		{"v3 unavailable with audience header", SignaturePolicy{V3Unavailable: "agent-id-not-persistent"}, "some-other-agent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg, lru, pub, priv := v3Setup(t, tc.policy)
			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			signRequestV3(t, req, nil, priv, pub, testAgentID, time.Now().Unix(), randomNonce(t))
			forgeSignature(req)
			if tc.audience != "" {
				req.Header.Set(HeaderAudience, tc.audience)
			}
			_, err := verifyAt(t, req, nil, reg, lru)
			if !errors.Is(err, ErrBadSignature) {
				t.Fatalf("err = %v, want ErrBadSignature", err)
			}
			if ReasonFor(err) != "invalid-signature" || RejectionDetail(err) != "unauthorized" {
				t.Fatalf("reason %q detail %q; a forged signature must get no hint", ReasonFor(err), RejectionDetail(err))
			}
		})
	}
}

func TestVerifyRequestV3_ForgedSignatureOnAgentWithoutIDGetsNoHint(t *testing.T) {
	reg, lru, pub, priv := testSetup(t) // no policy: no agent ID
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	signRequestV3(t, req, nil, priv, pub, "", time.Now().Unix(), randomNonce(t))
	forgeSignature(req)
	if _, err := verifyAt(t, req, nil, reg, lru); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

// A caller holding the key can prove it signed for another agent: the signature
// verifies under the audience it names, and only then is the mismatch reported.
func TestVerifyRequestV3_AudienceMismatchRequiresValidSignatureUnderNamedAudience(t *testing.T) {
	reg, lru, pub, priv := v3Setup(t, SignaturePolicy{})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	signRequestV3(t, req, nil, priv, pub, "agent-a", time.Now().Unix(), randomNonce(t))
	req.Header.Set(HeaderAudience, "agent-a")
	_, err := verifyAt(t, req, nil, reg, lru)
	if !errors.Is(err, ErrAudienceMismatch) {
		t.Fatalf("err = %v, want ErrAudienceMismatch", err)
	}
	if lru.Seen(req.Header.Get(HeaderNonce)) {
		t.Fatal("audience mismatch recorded the nonce")
	}
}

func TestVerifyRequestV3_UnavailableDoesNotRecordNonce(t *testing.T) {
	reg, lru, pub, priv := v3Setup(t, SignaturePolicy{V3Unavailable: "state-not-writable"})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	signRequestV3(t, req, nil, priv, pub, testAgentID, time.Now().Unix(), randomNonce(t))
	if _, err := verifyAt(t, req, nil, reg, lru); !errors.Is(err, ErrV3Unavailable) {
		t.Fatalf("err = %v", err)
	}
	if ReasonFor(&V3UnavailableError{Reason: "state-not-writable"}) != "state-not-writable" {
		t.Fatal("reason")
	}
	if lru.Seen(req.Header.Get(HeaderNonce)) {
		t.Fatal("refused v3 recorded the nonce")
	}
}
