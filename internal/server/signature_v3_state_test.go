// SPDX-License-Identifier: MIT

package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codeswhat/portwing/internal/auth"
)

func TestServerWithoutStateDirRefusesV3(t *testing.T) {
	t.Parallel()
	s, priv := newV3Server(t, "", 0, false)

	rec := doV3(s, infoRequest(t, "3", priv, v3TestAgentID))
	if rec.Code != 401 || rec.Header().Get(auth.HeaderReason) != "state-not-writable" {
		t.Fatalf("v3 without a state dir: %d reason %q", rec.Code, rec.Header().Get(auth.HeaderReason))
	}
	rec = doV3(s, infoRequest(t, "2", priv, ""))
	if rec.Code != 200 {
		t.Fatalf("v2: %d", rec.Code)
	}
	var info map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info["v3"] != false || info["v3Reason"] != "state-not-writable" {
		t.Fatalf("info = %v", info)
	}
}

func TestServerForgedV3SignatureGetsNoHintWhenV3Unavailable(t *testing.T) {
	t.Parallel()
	s, priv := newV3ServerWith(t, t.TempDir(), 0, false, "agent-id-not-persistent")
	req := infoRequest(t, "3", priv, v3TestAgentID)
	req.Header.Set(auth.HeaderSignature, base64.RawURLEncoding.EncodeToString(make([]byte, 64)))
	rec := doV3(s, req)
	if rec.Code != 401 || rec.Header().Get(auth.HeaderReason) != "invalid-signature" {
		t.Fatalf("%d reason %q", rec.Code, rec.Header().Get(auth.HeaderReason))
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "unauthorized" {
		t.Fatalf("body = %q, want no hint", body)
	}
}

func TestServerForgedV3SignatureWithWrongAudienceHeaderGetsNoHint(t *testing.T) {
	t.Parallel()
	s, priv := newV3Server(t, t.TempDir(), 0, false)
	req := httptest.NewRequest(http.MethodGet, "/_portwing/info", nil)
	signV3(t, req, nil, priv, v3TestAgentID)
	req.Header.Set(auth.HeaderAudience, "some-other-agent")
	rec := doV3(s, req)
	if rec.Code != 401 || rec.Header().Get(auth.HeaderReason) != "invalid-signature" {
		t.Fatalf("%d reason %q", rec.Code, rec.Header().Get(auth.HeaderReason))
	}
}
