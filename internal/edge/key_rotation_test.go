// SPDX-License-Identifier: MIT

package edge

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/codeswhat/portwing/internal/auth"
	"github.com/codeswhat/portwing/internal/config"
	"github.com/codeswhat/portwing/internal/protocol"
)

func managedEdgeClient(t *testing.T) (*Client, *websocket.Conn, protocol.HelloMessage) {
	t.Helper()
	c, ctrl := newTestClient(t)
	pem, _, err := auth.GenerateKeyPair("operator")
	if err != nil {
		t.Fatal(err)
	}
	c.cfg.PrivateKeyFile = filepath.Join(t.TempDir(), "bootstrap.pem")
	if err := os.WriteFile(c.cfg.PrivateKeyFile, pem, 0o400); err != nil {
		t.Fatal(err)
	}
	c.cfg.KeyRotationDir = filepath.Join(t.TempDir(), "managed")
	response := mkResp(200, "application/json", `{}`)
	t.Cleanup(func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	})
	c.dockerClient = &fakeDocker{doResp: response}
	c.adapter = &fakeAdapter{}
	if err := c.sendHello(context.Background()); err != nil {
		t.Fatal(err)
	}
	var hello protocol.HelloMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeHello), &hello)
	return c, ctrl, hello
}

func edgeRotationRequest(t *testing.T, c *Client, ctrl *websocket.Conn, method, path, body string) protocol.ResponseMessage {
	t.Helper()
	c.handleRequest(context.Background(), protocol.RequestMessage{RequestID: "identity-operation", Method: method, Path: path, Body: json.RawMessage(body)})
	var response protocol.ResponseMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeResponse), &response)
	if response.RequestID != "identity-operation" {
		t.Fatalf("lost response correlation: %+v", response)
	}
	return response
}

func TestEdgeRotationStagesAndSignsReplacement(t *testing.T) {
	t.Parallel()
	c, ctrl, hello := managedEdgeClient(t)
	if !slices.Contains(hello.Capabilities, "edge-key-rotation") {
		t.Fatal("enabled agent did not advertise key rotation")
	}
	response := edgeRotationRequest(t, c, ctrl, "POST", "/_portwing/auth/rotation/edge/prepare", "{}")
	if response.StatusCode != 200 {
		t.Fatalf("prepare: %d %s", response.StatusCode, response.Body)
	}
	var status auth.EdgeRotationStatus
	if err := json.Unmarshal(response.Body, &status); err != nil {
		t.Fatal(err)
	}
	if status.CurrentKeyID != hello.PubKeyID || status.PendingKeyID == "" {
		t.Fatalf("bad prepared identity: %+v", status)
	}
	pub, err := base64.StdEncoding.DecodeString(status.PendingPublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize || auth.KeyIDForPublicKey(pub) != status.PendingKeyID {
		t.Fatalf("bad replacement public key: %+v", status)
	}
	bad := edgeRotationRequest(t, c, ctrl, "POST", "/_portwing/auth/rotation/edge/activate", `{"key_id":"wrong"}`)
	if bad.StatusCode != 400 {
		t.Fatalf("unbound activation: %+v", bad)
	}
	response = edgeRotationRequest(t, c, ctrl, "POST", "/_portwing/auth/rotation/edge/activate", fmt.Sprintf(`{"key_id":%q}`, status.PendingKeyID))
	if response.StatusCode != 200 {
		t.Fatalf("activate: %d %s", response.StatusCode, response.Body)
	}
	trial := newHello()
	if err := c.signHello(context.Background(), &trial); err != nil {
		t.Fatal(err)
	}
	if trial.PubKeyID != status.PendingKeyID {
		t.Fatalf("trial used %s instead of %s", trial.PubKeyID, status.PendingKeyID)
	}
	sig, err := base64.RawURLEncoding.DecodeString(trial.Signature)
	if err != nil {
		t.Fatal(err)
	}
	message := auth.CanonicalMessage("GET", "/api/portwing/ws", auth.BodyHashHex(nil), trial.Timestamp, trial.Nonce)
	if !ed25519.Verify(pub, message, sig) {
		t.Fatal("replacement hello did not prove possession")
	}
}

func TestEdgeRotationDisabledNamespaceNeverReachesDocker(t *testing.T) {
	t.Parallel()
	c, ctrl := newTestClient(t)
	responseBody := mkResp(200, "application/json", `{}`)
	defer responseBody.Body.Close()
	fd := &fakeDocker{doResp: responseBody}
	c.dockerClient = fd
	for _, path := range []string{"/_portwing/auth/rotation", "/_portwing/auth/rotation/edge", "/_portwing/auth/rotation/edge/prepare"} {
		response := edgeRotationRequest(t, c, ctrl, "POST", path, "{}")
		if response.StatusCode != 404 {
			t.Fatalf("disabled %s returned %d", path, response.StatusCode)
		}
	}
	fd.mu.Lock()
	defer fd.mu.Unlock()
	if len(fd.doCalls) != 0 {
		t.Fatalf("identity requests reached Docker: %+v", fd.doCalls)
	}
}

func TestEdgeRotationRejectsStaleConnectionMutations(t *testing.T) {
	t.Parallel()
	c, ctrl, _ := managedEdgeClient(t)
	old := c.currentOutboundTarget()
	c.connMu.Lock()
	c.conn = nil
	c.connMu.Unlock()
	c.handleRequestTo(context.Background(), protocol.RequestMessage{RequestID: "stale", Method: "POST", Path: "/_portwing/auth/rotation/edge/prepare", Body: json.RawMessage(`{}`)}, old)
	var response protocol.ResponseMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeResponse), &response)
	if response.StatusCode != 409 {
		t.Fatalf("stale connection mutated credentials: %+v", response)
	}
}

func TestEdgeRotationHonorsResponseEncodingCapability(t *testing.T) {
	t.Parallel()
	c, ctrl, _ := managedEdgeClient(t)
	c.controllerCaps = []string{protocol.CapResponseBodyBase64}
	response := edgeRotationRequest(t, c, ctrl, "GET", "/_portwing/auth/rotation/edge", "")
	if response.StatusCode != 200 || response.BodyBase64 == "" || len(response.Body) != 0 {
		t.Fatalf("rotation response ignored negotiated encoding: %+v", response)
	}
}

func TestEdgeRotationRequestValidationAndAudit(t *testing.T) {
	c, ctrl, _ := managedEdgeClient(t)
	for _, tc := range []struct {
		method, path, body string
		code               int
	}{
		{"GET", "/_portwing/auth/rotation/edge?query=1", "", 400},
		{"POST", "/_portwing/auth/rotation/edge/prepare", strings.Repeat("x", 4097), 400},
		{"POST", "/_portwing/auth/rotation/unknown", "{}", 404},
		{"POST", "/_portwing/auth/rotation/edge/unknown", "{}", 404},
		{"GET", "/_portwing/auth/rotation/edge/prepare", "", 405},
		{"POST", "/_portwing/auth/rotation/edge/prepare", "{", 400},
		{"POST", "/_portwing/auth/rotation/edge/prepare", `{"extra":true}`, 400},
		{"POST", "/_portwing/auth/rotation/edge/prepare", `{} {}`, 400},
		{"POST", "/_portwing/auth/rotation/edge/prepare", `{"key_id":"wrong"}`, 400},
		{"POST", "/_portwing/auth/rotation/edge/prepare", "{}", 200},
		{"POST", "/_portwing/auth/rotation/edge/abort", "{}", 200},
	} {
		response := edgeRotationRequest(t, c, ctrl, tc.method, tc.path, tc.body)
		if response.StatusCode != tc.code {
			t.Fatalf("%s %s %s: got %d, want %d", tc.method, tc.path, tc.body, response.StatusCode, tc.code)
		}
	}
	c.connMu.Lock()
	c.signedHelloKeyID = "superseded"
	c.connMu.Unlock()
	response := edgeRotationRequest(t, c, ctrl, "POST", "/_portwing/auth/rotation/edge/prepare", "{}")
	if response.StatusCode != 409 {
		t.Fatalf("superseded signer mutation: %d", response.StatusCode)
	}
}

func TestEdgeRotationWriteFailureIsReportedAndPreservesCurrentSigner(t *testing.T) {
	c, ctrl, hello := managedEdgeClient(t)
	if err := os.Rename(c.cfg.KeyRotationDir, c.cfg.KeyRotationDir+"-offline"); err != nil {
		t.Fatal(err)
	}
	response := edgeRotationRequest(t, c, ctrl, "POST", "/_portwing/auth/rotation/edge/prepare", "{}")
	if response.StatusCode != 500 {
		t.Fatalf("failed write: %d", response.StatusCode)
	}
	if c.keyRotation.Status().CurrentKeyID != hello.PubKeyID || c.keyRotation.Status().PendingKeyID != "" {
		t.Fatal("failed write changed identity")
	}
}

func TestEdgeRotationHandshakeProofAndRecovery(t *testing.T) {
	for _, scenario := range []string{"welcome", "lost-welcome", "reject", "reject-write-fails", "confirm-write-fails"} {
		t.Run(scenario, func(t *testing.T) {
			pem, _, err := auth.GenerateKeyPair("bootstrap")
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(t.TempDir(), "bootstrap.pem")
			if err := os.WriteFile(file, pem, 0o400); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(t.TempDir(), "managed")
			cfg := &config.Config{PrivateKeyFile: file, KeyRotationDir: dir, HeartbeatInterval: 30, WelcomeTimeout: 3, DDPollInterval: 300, SkipDFCollection: true}
			c := newWireClient(t, cfg)
			if err := c.initKeyRotation(); err != nil {
				t.Fatal(err)
			}
			old := c.keyRotation.Status().CurrentKeyID
			pending, err := c.keyRotation.Prepare()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.keyRotation.Activate(pending.PendingKeyID); err != nil {
				t.Fatal(err)
			}
			cfg.DrydockURL = newControllerServer(t, func(ctrl *websocket.Conn) {
				var hello protocol.HelloMessage
				decodeData(t, expectType(t, ctrl, protocol.TypeHello), &hello)
				if hello.PubKeyID != pending.PendingKeyID {
					t.Errorf("trial did not prove replacement: %s", hello.PubKeyID)
				}
				if scenario == "lost-welcome" {
					return
				}
				if strings.HasSuffix(scenario, "write-fails") {
					if err := os.Rename(dir, dir+"-offline"); err != nil {
						t.Error(err)
						return
					}
				}
				if strings.HasPrefix(scenario, "reject") {
					if err := ctrl.WriteJSON(protocol.Envelope{Type: protocol.TypeError, Data: json.RawMessage(`{"code":"unknown-key","message":"replacement rejected"}`)}); err != nil {
						t.Error(err)
					}
					return
				}
				sendWelcomeMsg(t, ctrl, protocol.WelcomeMessage{})
				_ = ctrl.SetReadDeadline(time.Now().Add(3 * time.Second))
				_, _, _ = ctrl.ReadMessage()
			})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			established, err := c.connect(ctx)
			status := c.keyRotation.Status()
			switch scenario {
			case "welcome":
				if !established || status.CurrentKeyID != pending.PendingKeyID || status.PendingKeyID != "" {
					t.Fatalf("welcome failed to confirm: %+v %v", status, err)
				}
			case "confirm-write-fails":
				if !established || !status.ActivationPending || status.PendingKeyID != pending.PendingKeyID {
					t.Fatalf("failed confirmation lost durable candidate: %+v %v", status, err)
				}
			case "reject":
				if established || err == nil || errors.Is(err, errFatal) || status.ActivationPending || status.CurrentKeyID != old {
					t.Fatalf("terminal trial did not safely restore predecessor: %+v %v", status, err)
				}
			default:
				if established || err == nil || !status.ActivationPending || status.PendingKeyID != pending.PendingKeyID {
					t.Fatalf("interrupted proof lost durable trial: %+v %v", status, err)
				}
			}
		})
	}
}

func TestEdgeInvalidManagedStorageFailsBeforeConnectionOrServing(t *testing.T) {
	c, ctrl := newTestClient(t)
	c.dockerClient = &fakeDocker{}
	c.adapter = &fakeAdapter{}
	c.cfg.PrivateKeyFile = filepath.Join(t.TempDir(), "absent.pem")
	c.cfg.KeyRotationDir = filepath.Join(t.TempDir(), "managed")
	if _, err := c.connect(context.Background()); err == nil {
		t.Fatal("connected with invalid credentials")
	}
	if err := c.Run(context.Background()); err == nil {
		t.Fatal("started with invalid credentials")
	}
	if err := c.sendHello(context.Background()); err == nil {
		t.Fatal("sent hello with invalid credentials")
	}
	hello := newHello()
	if err := c.signHello(context.Background(), &hello); err == nil {
		t.Fatal("signed hello with invalid credentials")
	}
	response := edgeRotationRequest(t, c, ctrl, "GET", "/_portwing/auth/rotation/edge", "")
	if response.StatusCode != 500 {
		t.Fatalf("invalid storage response: %d", response.StatusCode)
	}
}
