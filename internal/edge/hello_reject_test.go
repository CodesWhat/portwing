package edge

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/codeswhat/portwing/internal/config"
	"github.com/codeswhat/portwing/internal/protocol"
)

func TestHelloRejectionClassifiers(t *testing.T) {
	t.Parallel()

	terminalCodes := []string{
		"ed25519-required",
		"unknown-key",
		"bad-signature",
		"protocol-mismatch",
		"no-auth",
		"invalid-agent-name",
		"parse-error",
		"expected-hello",
		"agent-name-claimed",
	}
	for _, code := range terminalCodes {
		code := code
		t.Run("terminal/"+code, func(t *testing.T) {
			t.Parallel()

			if !isTerminalHelloRejection(code) {
				t.Fatalf("isTerminalHelloRejection(%q) = false, want true", code)
			}
			if !isKnownHelloRejection(code) {
				t.Fatalf("isKnownHelloRejection(%q) = false, want true", code)
			}
		})
	}

	transientCodes := []string{
		"timestamp-skew",
		"bad-nonce",
		"replay",
		"internal-error",
		"rate-limited",
		"registry-full",
		"agent-already-connected",
	}
	for _, code := range transientCodes {
		code := code
		t.Run("transient/"+code, func(t *testing.T) {
			t.Parallel()

			if isTerminalHelloRejection(code) {
				t.Fatalf("isTerminalHelloRejection(%q) = true, want false", code)
			}
			if !isKnownHelloRejection(code) {
				t.Fatalf("isKnownHelloRejection(%q) = false, want true", code)
			}
		})
	}

	t.Run("unknown", func(t *testing.T) {
		t.Parallel()

		code := "totally-made-up"
		if isTerminalHelloRejection(code) {
			t.Fatalf("isTerminalHelloRejection(%q) = true, want false", code)
		}
		if isKnownHelloRejection(code) {
			t.Fatalf("isKnownHelloRejection(%q) = true, want false", code)
		}
	})
}

func TestConnectHelloRejectionClassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		code      string
		rawData   json.RawMessage
		wantFatal bool
	}{
		{
			name:      "terminal",
			code:      "unknown-key",
			wantFatal: true,
		},
		{
			name: "transient",
			code: "rate-limited",
		},
		{
			name: "unknown",
			code: "future-code",
		},
		{
			name:    "unparseable error payload",
			rawData: json.RawMessage(`"not an object"`),
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := newControllerServer(t, func(ctrl *websocket.Conn) {
				readAndAckHello(t, ctrl)
				if tc.rawData != nil {
					env := protocol.Envelope{Type: protocol.TypeError, Data: tc.rawData}
					if err := ctrl.WriteJSON(env); err != nil {
						t.Fatalf("write error envelope: %v", err)
					}
					return
				}
				sendEnvelope(t, ctrl, protocol.TypeError, protocol.ErrorMessage{
					Code:    tc.code,
					Message: "hello rejected for test",
				})
			})

			c := newWireClient(t, newHelloRejectConfig(srv))
			established, err := c.connect(context.Background())
			if established {
				t.Fatal("established = true, want false")
			}
			if err == nil {
				t.Fatal("connect returned nil error, want rejection error")
			}
			if gotFatal := errors.Is(err, errFatal); gotFatal != tc.wantFatal {
				t.Fatalf("errors.Is(err, errFatal) = %v, want %v (err=%v)", gotFatal, tc.wantFatal, err)
			}
		})
	}
}

func TestRunTerminalHelloRejectionNoRetry(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	srv := newControllerServer(t, func(ctrl *websocket.Conn) {
		attempts.Add(1)
		readAndAckHello(t, ctrl)
		sendEnvelope(t, ctrl, protocol.TypeError, protocol.ErrorMessage{
			Code:    "unknown-key",
			Message: "key is not enrolled",
		})
	})

	addr := freeAddr(t)
	cfg := newHelloRejectConfig(srv)
	cfg.BindAddress = "127.0.0.1"
	cfg.Port = portFrom(addr)
	cfg.ReconnectDelay = 5
	cfg.MaxReconnectDelay = 10

	c := newWireClient(t, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := c.Run(ctx)
	if err == nil {
		t.Fatal("Run returned nil, want errFatal")
	}
	if !errors.Is(err, errFatal) {
		t.Fatalf("Run returned %v, want an error wrapping errFatal", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("controller saw %d connection attempts, want 1", got)
	}
}

func newHelloRejectConfig(drydockURL string) *config.Config {
	return &config.Config{
		DrydockURL:        drydockURL,
		HeartbeatInterval: 30,
		WelcomeTimeout:    5,
		ReconnectDelay:    1,
		MaxReconnectDelay: 60,
		DDPollInterval:    300,
		SkipDFCollection:  true,
	}
}
