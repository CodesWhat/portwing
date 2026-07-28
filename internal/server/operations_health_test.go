package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/protocol"
)

type operationalHealthResponse struct {
	Status        string  `json:"status"`
	Live          bool    `json:"live"`
	Ready         bool    `json:"ready"`
	Mode          string  `json:"mode"`
	Version       string  `json:"version"`
	UptimeSeconds float64 `json:"uptimeSeconds"`
	Docker        string  `json:"docker"`
	Controller    string  `json:"controller"`
}

func TestLivenessResponseIsProcessOnly(t *testing.T) {
	t.Parallel()

	s := &Server{startTime: time.Now().Add(-3 * time.Second)}
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	s.handleSimpleHealth(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var got operationalHealthResponse
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode liveness response: %v", err)
	}
	if got.Status != "ok" || !got.Live || got.Ready {
		t.Fatalf("liveness = %+v, want live process without readiness claim", got)
	}
	if got.Mode != "standard" || got.Version != protocol.AgentVersion {
		t.Fatalf("mode/version = %q/%q, want standard/%q", got.Mode, got.Version, protocol.AgentVersion)
	}
	if got.UptimeSeconds < 2.5 || got.Docker != "unknown" || got.Controller != "not_applicable" {
		t.Fatalf("operational fields = %+v", got)
	}
}

func TestReadinessResponseIncludesDockerState(t *testing.T) {
	t.Parallel()

	client, stop := newDockerClientWithPing(t, true)
	defer stop()

	s := &Server{dockerClient: client, startTime: time.Now().Add(-2 * time.Second)}
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rr := httptest.NewRecorder()
	s.handleHealth(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var got operationalHealthResponse
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode readiness response: %v", err)
	}
	if got.Status != "healthy" || !got.Live || !got.Ready {
		t.Fatalf("readiness = %+v, want healthy/live/ready", got)
	}
	if got.Mode != "standard" || got.Docker != "connected" || got.Controller != "not_applicable" {
		t.Fatalf("operational fields = %+v", got)
	}
}
