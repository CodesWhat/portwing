// SPDX-License-Identifier: MIT

package edge

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/codeswhat/portwing/internal/auth"
	"github.com/codeswhat/portwing/internal/config"
	"github.com/codeswhat/portwing/internal/protocol"
)

const testEnrollCode = "Zx9-test_enroll-CODE-0123456789"

// enrollConfig builds an Edge config pointing at a fake controller with a
// freshly generated, pending enrolled key in a temp state dir.
func enrollConfig(t *testing.T, url, code string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	key, err := auth.LoadOrCreateEnrolledKey(dir, true)
	if err != nil {
		t.Fatalf("LoadOrCreateEnrolledKey: %v", err)
	}
	cfg := newHelloRejectConfig(url)
	cfg.StateDir = dir
	cfg.PrivateKeyFile = key.Path
	cfg.EnrollMarkerPath = key.MarkerPath
	cfg.EnrollCode = code
	return cfg
}

func readHello(t *testing.T, ctrl *websocket.Conn) (protocol.HelloMessage, []byte) {
	t.Helper()
	_ = ctrl.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, raw, err := ctrl.ReadMessage()
	if err != nil {
		t.Errorf("read hello: %v", err)
		return protocol.HelloMessage{}, nil
	}
	var env protocol.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Errorf("hello envelope: %v", err)
	}
	var hello protocol.HelloMessage
	if err := json.Unmarshal(env.Data, &hello); err != nil {
		t.Errorf("hello data: %v", err)
	}
	return hello, raw
}

func TestEnrollHelloShapeAndConfirmation(t *testing.T) {
	var got protocol.HelloMessage
	var raw []byte
	srv := newControllerServer(t, func(ctrl *websocket.Conn) {
		got, raw = readHello(t, ctrl)
		sendWelcomeMsg(t, ctrl, protocol.WelcomeMessage{
			PollInterval: 30,
			Config:       map[string]string{"enrolled": "true"},
			Capabilities: []string{protocol.CapEnroll},
		})
	})
	cfg := enrollConfig(t, srv, testEnrollCode)
	marker := cfg.EnrollMarkerPath
	c := newWireClient(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = c.connect(ctx)

	if got.Enroll == nil {
		t.Fatalf("hello has no enroll block: %s", raw)
	}
	if got.Enroll.Code != testEnrollCode {
		t.Fatalf("enroll code = %q", got.Enroll.Code)
	}
	if !slices.Contains(got.Capabilities, protocol.CapEnroll) {
		t.Fatalf("capabilities %v lack %s", got.Capabilities, protocol.CapEnroll)
	}
	pubRaw, err := base64.StdEncoding.DecodeString(got.Enroll.PublicKey)
	if err != nil || len(pubRaw) != ed25519.PublicKeySize || len(got.Enroll.PublicKey) != 44 {
		t.Fatalf("publicKey %q is not 44-char base64 of 32 bytes (err=%v)", got.Enroll.PublicKey, err)
	}
	pub := ed25519.PublicKey(pubRaw)
	if got.PubKeyID != auth.KeyIDForPublicKey(pub) {
		t.Fatalf("pubKeyId %q does not match the enrolled key id %q", got.PubKeyID, auth.KeyIDForPublicKey(pub))
	}
	sig, err := base64.RawURLEncoding.DecodeString(got.Signature)
	if err != nil {
		t.Fatalf("signature: %v", err)
	}
	canonical := auth.CanonicalMessage("GET", "/api/portwing/ws", auth.BodyHashHex(nil), got.Timestamp, got.Nonce)
	if !ed25519.Verify(pub, canonical, sig) {
		t.Fatal("hello signature does not verify against enroll.publicKey")
	}

	// The welcome confirmed the key: marker gone, code dropped.
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending marker still present after welcome (err=%v)", err)
	}
	if cfg.EnrollCode != "" || cfg.EnrollMarkerPath != "" {
		t.Fatal("code or marker path retained after confirmation")
	}
}

func TestHelloWithoutEnrollmentHasNoEnrollBlock(t *testing.T) {
	var got protocol.HelloMessage
	var raw []byte
	srv := newControllerServer(t, func(ctrl *websocket.Conn) {
		got, raw = readHello(t, ctrl)
		sendWelcomeMsg(t, ctrl, protocol.WelcomeMessage{PollInterval: 30})
	})
	cfg := enrollConfig(t, srv, "")
	cfg.EnrollMarkerPath = "" // confirmed key: later start, no code
	c := newWireClient(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = c.connect(ctx)

	if got.Enroll != nil || bytes.Contains(raw, []byte(`"enroll"`)) {
		t.Fatalf("non-enrolling hello carries an enroll block: %s", raw)
	}
	if slices.Contains(got.Capabilities, protocol.CapEnroll) {
		t.Fatal("dd:enroll advertised while not enrolling")
	}
	if got.PubKeyID == "" || got.Signature == "" {
		t.Fatal("hello must still be signed")
	}
}

func TestPendingKeyWithoutCodeConfirmsOnWelcome(t *testing.T) {
	srv := newControllerServer(t, func(ctrl *websocket.Conn) {
		readHello(t, ctrl)
		sendWelcomeMsg(t, ctrl, protocol.WelcomeMessage{PollInterval: 30})
	})
	cfg := enrollConfig(t, srv, "")
	marker := cfg.EnrollMarkerPath
	c := newWireClient(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = c.connect(ctx)

	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker should be cleared once the controller welcomes the key (err=%v)", err)
	}
}

func TestEnrollRejectionMatrix(t *testing.T) {
	tests := []struct {
		code      string
		wantFatal bool
	}{
		{"enroll-code-invalid", true},
		{"enroll-code-expired", true},
		{"enroll-code-used", true},
		{"enroll-code-revoked", true},
		{"enroll-key-invalid", true},
		{"enroll-disabled", true},
		{"enroll-rate-limited", false},
	}
	for _, tc := range tests {
		t.Run(tc.code, func(t *testing.T) {
			srv := newControllerServer(t, func(ctrl *websocket.Conn) {
				readHello(t, ctrl)
				sendEnvelope(t, ctrl, protocol.TypeError, protocol.ErrorMessage{Code: tc.code, Message: "no"})
			})
			cfg := enrollConfig(t, srv, testEnrollCode)
			keyPath, marker := cfg.PrivateKeyFile, cfg.EnrollMarkerPath
			c := newWireClient(t, cfg)

			_, err := c.connect(context.Background())
			if err == nil {
				t.Fatal("connect returned nil")
			}
			if got := errors.Is(err, errFatal); got != tc.wantFatal {
				t.Fatalf("fatal = %v, want %v (%v)", got, tc.wantFatal, err)
			}
			if strings.Contains(err.Error(), testEnrollCode) {
				t.Fatal("error leaks the code")
			}
			// The key stays, unconfirmed, so a fresh code reuses it.
			if _, err := os.Stat(keyPath); err != nil {
				t.Fatalf("key removed: %v", err)
			}
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("marker removed: %v", err)
			}
		})
	}
}

func TestEnrollAgainstOldControllerExplainsUnknownKey(t *testing.T) {
	srv := newControllerServer(t, func(ctrl *websocket.Conn) {
		readHello(t, ctrl)
		sendEnvelope(t, ctrl, protocol.TypeError, protocol.ErrorMessage{Code: "unknown-key", Message: "nope"})
	})
	cfg := enrollConfig(t, srv, testEnrollCode)
	c := newWireClient(t, cfg)

	_, err := c.connect(context.Background())
	if !errors.Is(err, errFatal) {
		t.Fatalf("want terminal, got %v", err)
	}
	if !strings.Contains(err.Error(), "did not accept enrollment") {
		t.Fatalf("missing clarified message: %v", err)
	}
	if strings.Contains(err.Error(), testEnrollCode) {
		t.Fatal("error leaks the code")
	}
}

func TestEnrollCodeNeverLogged(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	for _, reply := range []func(*websocket.Conn){
		func(ctrl *websocket.Conn) { sendWelcomeMsg(t, ctrl, protocol.WelcomeMessage{PollInterval: 30}) },
		func(ctrl *websocket.Conn) {
			sendEnvelope(t, ctrl, protocol.TypeError, protocol.ErrorMessage{Code: "enroll-code-used", Message: "used"})
		},
	} {
		srv := newControllerServer(t, func(ctrl *websocket.Conn) {
			readHello(t, ctrl)
			reply(ctrl)
		})
		c := newWireClient(t, enrollConfig(t, srv, testEnrollCode))
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _ = c.connect(ctx)
		cancel()
	}

	if buf.Len() == 0 {
		t.Fatal("expected some log output to inspect")
	}
	if strings.Contains(buf.String(), testEnrollCode) {
		t.Fatalf("enrollment code leaked into logs:\n%s", buf.String())
	}
}

func TestEnrollBlockRedactsInFormatting(t *testing.T) {
	b := protocol.EnrollBlock{Code: testEnrollCode, PublicKey: "k"}
	for _, s := range []string{b.String(), strings.TrimSpace(slogValue(b)), strings.TrimSpace(sprintGo(b))} {
		if strings.Contains(s, testEnrollCode) {
			t.Fatalf("formatting leaks the code: %q", s)
		}
	}
}

func slogValue(v any) string { return slog.AnyValue(v).String() }
func sprintGo(v any) string  { return protocol.EnrollBlock.GoString(v.(protocol.EnrollBlock)) }

func TestEnrollRejectionClassifiers(t *testing.T) {
	for _, code := range []string{"enroll-code-invalid", "enroll-code-expired", "enroll-code-used", "enroll-code-revoked", "enroll-key-invalid", "enroll-disabled"} {
		if !isTerminalHelloRejection(code) {
			t.Errorf("%s should be terminal", code)
		}
	}
	if isTerminalHelloRejection("enroll-rate-limited") || !isKnownHelloRejection("enroll-rate-limited") {
		t.Error("enroll-rate-limited should be a known transient code")
	}
}
