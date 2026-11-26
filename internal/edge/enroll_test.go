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
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
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
	const freshCodeHint = "retry with a fresh ENROLL_CODE"
	tests := []struct {
		code      string
		wantFatal bool
		wantHint  bool
	}{
		{"enroll-code-invalid", true, true},
		{"enroll-code-expired", true, true},
		{"enroll-code-used", true, true},
		{"enroll-code-revoked", true, true},
		{"enroll-key-invalid", true, true},
		{"enroll-disabled", true, true},
		{"enroll-rate-limited", false, false},
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
			if got := strings.Contains(err.Error(), freshCodeHint); got != tc.wantHint {
				t.Fatalf("hint present = %v, want %v (%v)", got, tc.wantHint, err)
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

func TestEnrollRejectionEchoingCodeIsRedacted(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	srv := newControllerServer(t, func(ctrl *websocket.Conn) {
		readHello(t, ctrl)
		sendEnvelope(t, ctrl, protocol.TypeError, protocol.ErrorMessage{Code: "enroll-code-invalid", Message: "bad code " + testEnrollCode})
	})
	c := newWireClient(t, enrollConfig(t, srv, testEnrollCode))
	_, err := c.connect(context.Background())
	if err == nil {
		t.Fatal("connect returned nil")
	}
	if strings.Contains(err.Error(), testEnrollCode) || strings.Contains(buf.String(), testEnrollCode) {
		t.Fatalf("echoed code leaked: err=%v logs=%s", err, buf.String())
	}
}

func TestEnrollWelcomeWithoutAckKeepsPending(t *testing.T) {
	welcomes := map[string]protocol.WelcomeMessage{
		"no-config": {PollInterval: 30, Capabilities: []string{protocol.CapEnroll}},
		"no-cap":    {PollInterval: 30, Config: map[string]string{"enrolled": "true"}},
		"not-true":  {PollInterval: 30, Config: map[string]string{"enrolled": "false"}, Capabilities: []string{protocol.CapEnroll}},
	}
	for name, w := range welcomes {
		t.Run(name, func(t *testing.T) {
			srv := newControllerServer(t, func(ctrl *websocket.Conn) {
				readHello(t, ctrl)
				sendWelcomeMsg(t, ctrl, w)
			})
			cfg := enrollConfig(t, srv, testEnrollCode)
			marker := cfg.EnrollMarkerPath
			c := newWireClient(t, cfg)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _ = c.connect(ctx)
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("marker must stay pending without an ack: %v", err)
			}
		})
	}
}

func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestEnrollUnparseableRejectionEchoingCodeIsRedacted(t *testing.T) {
	buf := captureSlog(t)

	srv := newControllerServer(t, func(ctrl *websocket.Conn) {
		readHello(t, ctrl)
		// A JSON string, not an ErrorMessage object.
		sendEnvelope(t, ctrl, protocol.TypeError, "bad code "+testEnrollCode)
	})
	c := newWireClient(t, enrollConfig(t, srv, testEnrollCode))
	_, err := c.connect(context.Background())
	if err == nil {
		t.Fatal("connect returned nil")
	}
	if errors.Is(err, errFatal) {
		t.Fatalf("an unparseable rejection must stay retryable: %v", err)
	}
	logs := buf.String()
	if !strings.Contains(logs, "unparseable error payload") || !strings.Contains(logs, "bad code ") || !strings.Contains(logs, "[redacted]") {
		t.Fatalf("expected the redacted unparseable-payload warning, got:\n%s", logs)
	}
	if strings.Contains(logs, testEnrollCode) || strings.Contains(err.Error(), testEnrollCode) {
		t.Fatalf("echoed code leaked: err=%v logs=%s", err, logs)
	}
}

func TestEnrollRejectionEchoingCodeInCodeFieldIsRedacted(t *testing.T) {
	for name, code := range map[string]string{
		"unknown code":  testEnrollCode,
		"prefixed code": "oops " + testEnrollCode,
		"terminal code": "enroll-code-used",
	} {
		t.Run(name, func(t *testing.T) {
			buf := captureSlog(t)
			srv := newControllerServer(t, func(ctrl *websocket.Conn) {
				readHello(t, ctrl)
				sendEnvelope(t, ctrl, protocol.TypeError, protocol.ErrorMessage{Code: code, Message: "echo " + testEnrollCode})
			})
			c := newWireClient(t, enrollConfig(t, srv, testEnrollCode))
			_, err := c.connect(context.Background())
			if err == nil {
				t.Fatal("connect returned nil")
			}
			if wantFatal := code == "enroll-code-used"; errors.Is(err, errFatal) != wantFatal {
				t.Fatalf("fatal = %v, want %v (%v)", errors.Is(err, errFatal), wantFatal, err)
			}
			if strings.Contains(err.Error(), testEnrollCode) || strings.Contains(buf.String(), testEnrollCode) {
				t.Fatalf("echoed code leaked: err=%v logs=%s", err, buf.String())
			}
		})
	}
}

func TestPendingKeyUnparseableWelcomeKeepsPending(t *testing.T) {
	for name, code := range map[string]string{"no code": "", "with code": testEnrollCode} {
		t.Run(name, func(t *testing.T) {
			srv := newControllerServer(t, func(ctrl *websocket.Conn) {
				readHello(t, ctrl)
				sendEnvelope(t, ctrl, protocol.TypeWelcome, "notanobject")
			})
			cfg := enrollConfig(t, srv, code)
			marker := cfg.EnrollMarkerPath
			c := newWireClient(t, cfg)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _ = c.connect(ctx)
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("marker must stay pending after an unparseable welcome: %v", err)
			}
			if cfg.EnrollMarkerPath != marker || cfg.EnrollCode != code {
				t.Fatal("enrollment must not be marked confirmed")
			}
		})
	}
}

func TestPendingKeyWithoutCodeUnknownKeyHint(t *testing.T) {
	srv := newControllerServer(t, func(ctrl *websocket.Conn) {
		readHello(t, ctrl)
		sendEnvelope(t, ctrl, protocol.TypeError, protocol.ErrorMessage{Code: "unknown-key", Message: "nope"})
	})
	c := newWireClient(t, enrollConfig(t, srv, ""))
	_, err := c.connect(context.Background())
	if !errors.Is(err, errFatal) {
		t.Fatalf("want terminal, got %v", err)
	}
	if !strings.Contains(err.Error(), "no ENROLL_CODE is set") {
		t.Fatalf("missing the no-code hint: %v", err)
	}
}

// enrollLoadEnv points config.Load at a fresh state dir and the given
// controller URL with a code set.
func enrollLoadEnv(t *testing.T, url string, kv ...string) {
	t.Helper()
	t.Setenv("DRYDOCK_URL", url)
	t.Setenv("STATE_DIR", t.TempDir())
	t.Setenv("ENROLL_CODE", testEnrollCode)
	for i := 0; i+1 < len(kv); i += 2 {
		t.Setenv(kv[i], kv[i+1])
	}
}

func TestEnrollTLSSkipVerifyRefusedSendsNoHello(t *testing.T) {
	_, caCert, caKey := generateTestCA(t)
	var hellos atomic.Int32
	srv := newTLSControllerServer(t, generateTestLeafCert(t, caCert, caKey), func(ctrl *websocket.Conn) {
		hellos.Add(1)
		readHello(t, ctrl)
	})
	enrollLoadEnv(t, srv, "TLS_SKIP_VERIFY", "true")

	_, err := config.Load()
	if err == nil || !strings.Contains(err.Error(), "TLS_SKIP_VERIFY") || !strings.Contains(err.Error(), "ALLOW_INSECURE_ENROLLMENT") {
		t.Fatalf("want a startup refusal naming TLS_SKIP_VERIFY and the override, got %v", err)
	}
	if strings.Contains(err.Error(), testEnrollCode) {
		t.Fatal("error leaks the code")
	}
	time.Sleep(100 * time.Millisecond)
	if n := hellos.Load(); n != 0 {
		t.Fatalf("controller saw %d connections, want 0", n)
	}
}

func TestEnrollTLSSkipVerifyAllowedReachesController(t *testing.T) {
	_, caCert, caKey := generateTestCA(t)
	var got protocol.HelloMessage
	srv := newTLSControllerServer(t, generateTestLeafCert(t, caCert, caKey), func(ctrl *websocket.Conn) {
		got, _ = readHello(t, ctrl)
		sendWelcomeMsg(t, ctrl, protocol.WelcomeMessage{
			PollInterval: 30,
			Config:       map[string]string{"enrolled": "true"},
			Capabilities: []string{protocol.CapEnroll},
		})
	})
	enrollLoadEnv(t, srv, "TLS_SKIP_VERIFY", "true", "ALLOW_INSECURE_ENROLLMENT", "true")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("explicitly allowed insecure enrollment: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = newWireClient(t, cfg).connect(ctx)
	if got.Enroll == nil || got.Enroll.Code != testEnrollCode {
		t.Fatalf("controller did not receive the enrollment hello: %+v", got.Enroll)
	}
}

func TestEnrollWithTrustedCAAndVerificationOn(t *testing.T) {
	caPEM, caCert, caKey := generateTestCA(t)
	var got protocol.HelloMessage
	srv := newTLSControllerServer(t, generateTestLeafCert(t, caCert, caKey), func(ctrl *websocket.Conn) {
		got, _ = readHello(t, ctrl)
		sendWelcomeMsg(t, ctrl, protocol.WelcomeMessage{PollInterval: 30})
	})
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	enrollLoadEnv(t, srv, "CA_CERT", caFile)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("verified TLS enrollment: %v", err)
	}
	if cfg.TLSSkipVerify {
		t.Fatal("verification must stay on")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	established, err := newWireClient(t, cfg).connect(ctx)
	if !established {
		t.Fatalf("connect against the trusted CA failed: %v", err)
	}
	if got.Enroll == nil || got.Enroll.Code != testEnrollCode {
		t.Fatal("controller did not receive the enrollment code")
	}
}
