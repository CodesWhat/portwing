package server

import (
	"net/http"
	"testing"

	"github.com/codeswhat/portwing/internal/auth"
)

// TestStripPortwingAuthHeaders verifies that every Portwing credential header is
// removed before a request is proxied to the Docker socket, while unrelated
// headers are preserved.
func TestStripPortwingAuthHeaders(t *testing.T) {
	h := http.Header{}
	stripped := []string{
		"Authorization",
		"X-Portwing-Token",
		"X-Dd-Agent-Secret",
		auth.HeaderKeyID,
		auth.HeaderTimestamp,
		auth.HeaderNonce,
		auth.HeaderSignature,
		auth.HeaderSignatureVersion,
	}
	for _, name := range stripped {
		h.Set(name, "secret")
	}
	h.Set("Content-Type", "application/json")
	h.Set("X-Registry-Auth", "keep-me") // a legitimate Docker header

	stripPortwingAuthHeaders(h)

	for _, name := range stripped {
		if got := h.Get(name); got != "" {
			t.Errorf("auth header %q leaked to Docker: %q", name, got)
		}
	}
	if h.Get("Content-Type") != "application/json" {
		t.Error("Content-Type was wrongly stripped")
	}
	if h.Get("X-Registry-Auth") != "keep-me" {
		t.Error("X-Registry-Auth (a Docker header) was wrongly stripped")
	}
}
