package edge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/config"
	"github.com/codeswhat/portwing/internal/protocol"
)

func TestHealthServerConfiguresReadHeaderTimeout(t *testing.T) {
	t.Parallel()

	c := &Client{
		cfg: &config.Config{
			BindAddress: "127.0.0.1",
			Port:        "0",
		},
	}

	c.startHealthServer()
	t.Cleanup(func() {
		if c.healthServer != nil {
			_ = c.healthServer.Close()
		}
	})

	if c.healthServer == nil {
		t.Fatal("healthServer was not initialized")
	}
	if c.healthServer.ReadHeaderTimeout < 5*time.Second {
		t.Fatalf("ReadHeaderTimeout = %s, want at least 5s", c.healthServer.ReadHeaderTimeout)
	}
}

func TestHealthServerSeparatesLivenessFromDisconnectedReadiness(t *testing.T) {
	t.Parallel()

	c := &Client{
		cfg: &config.Config{
			BindAddress: "127.0.0.1",
			Port:        "0",
		},
	}
	c.startHealthServer()
	t.Cleanup(func() {
		if c.healthServer != nil {
			_ = c.healthServer.Close()
		}
	})

	liveness := httptest.NewRecorder()
	c.healthServer.Handler.ServeHTTP(
		liveness,
		httptest.NewRequest(http.MethodGet, "/health", nil),
	)
	if liveness.Code != http.StatusOK {
		t.Fatalf("liveness status = %d, want 200", liveness.Code)
	}
	var live protocol.HealthResponse
	if err := json.NewDecoder(liveness.Body).Decode(&live); err != nil {
		t.Fatalf("decode liveness: %v", err)
	}
	if !live.Live || live.Ready || live.Status != "ok" ||
		live.Docker != "unknown" || live.Controller != "disconnected" {
		t.Fatalf("liveness response = %+v", live)
	}

	readiness := httptest.NewRecorder()
	c.healthServer.Handler.ServeHTTP(
		readiness,
		httptest.NewRequest(http.MethodGet, "/ready", nil),
	)
	if readiness.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want 503", readiness.Code)
	}
	var ready protocol.HealthResponse
	if err := json.NewDecoder(readiness.Body).Decode(&ready); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	if ready.Ready || ready.Status != "unhealthy" ||
		ready.Docker != "disconnected" || ready.Controller != "disconnected" {
		t.Fatalf("readiness response = %+v", ready)
	}
}
