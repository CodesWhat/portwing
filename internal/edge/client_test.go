package edge

import (
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/config"
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
