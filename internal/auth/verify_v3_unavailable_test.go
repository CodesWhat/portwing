// SPDX-License-Identifier: MIT

package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestVerifyRequestV3_UnavailableRefusedWithSpecificReason(t *testing.T) {
	for _, tc := range []struct {
		reason string
		hint   string
	}{
		{"agent-id-not-v3-compatible", "AGENT_ID"},
		{"agent-id-not-persistent", "/data/credentials"},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			locks := NewMemorySignatureLocks()
			reg, lru, pub, priv := v3Setup(t, SignaturePolicy{V3Unavailable: tc.reason, Locks: locks})
			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			signRequestV3(t, req, nil, priv, pub, testAgentID, time.Now().Unix(), randomNonce(t))
			_, err := verifyAt(t, req, nil, reg, lru)
			if !errors.Is(err, ErrV3Unavailable) {
				t.Fatalf("err = %v, want ErrV3Unavailable", err)
			}
			if got := ReasonFor(err); got != tc.reason {
				t.Fatalf("reason = %q, want %q", got, tc.reason)
			}
			if detail := RejectionDetail(err); !strings.Contains(detail, tc.hint) || strings.Contains(detail, testAgentID) {
				t.Fatalf("detail = %q, want a fix mentioning %q and no ID", detail, tc.hint)
			}
			if lru.Seen(req.Header.Get(HeaderNonce)) {
				t.Fatal("refused v3 recorded the nonce")
			}
			if len(locks.List()) != 0 {
				t.Fatal("a refused v3 request must not lock the key")
			}
		})
	}
}

func TestVerifyRequestV3_UnavailableLeavesV2AndLegacyWorking(t *testing.T) {
	reg, lru, pub, priv := v3Setup(t, SignaturePolicy{V3Unavailable: "agent-id-not-persistent", Locks: NewMemorySignatureLocks()})
	v2 := httptest.NewRequest(http.MethodGet, "/x?a=1", nil)
	signRequest(t, v2, nil, priv, pub, time.Now().Unix(), randomNonce(t))
	if _, err := verifyAt(t, v2, nil, reg, lru); err != nil {
		t.Fatalf("v2: %v", err)
	}
	legacy := httptest.NewRequest(http.MethodGet, "/x", nil)
	signRequest(t, legacy, nil, priv, pub, time.Now().Unix(), randomNonce(t))
	legacy.Header.Del(HeaderSignatureVersion)
	if _, err := verifyAt(t, legacy, nil, reg, lru); err != nil {
		t.Fatalf("legacy: %v", err)
	}
}

func TestV3UnavailableErrorDetail(t *testing.T) {
	t.Parallel()
	if d := RejectionDetail(&V3UnavailableError{Reason: "something-else"}); d != "signature version 3 is unavailable on this agent: something-else" {
		t.Fatalf("detail = %q", d)
	}
}
