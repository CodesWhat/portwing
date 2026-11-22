// SPDX-License-Identifier: MIT

package edge

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	"context"
	"errors"
	"github.com/codeswhat/portwing/internal/docker"
	"testing"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/protocol"
)

// TestEdgeAuditRecordsCarrySigningKeyID covers WA-AUDIT-KEYID on the Edge path:
// exec_start and the failed-start api_request carry the session's signing key,
// and a token session (no signing key) stays unattributed.
func TestEdgeAuditRecordsCarrySigningKeyID(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, keyID string }{
		{"signed", "0123456789abcdef"},
		{"token", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, logger, next := newAuditedTestClient(t)
			c.dockerClient = &fakeDocker{createExecErr: errors.New("boom")}
			c.signedHelloKeyID = tc.keyID

			c.StartExec(context.Background(), protocol.ExecStartMessage{ExecID: "e1", ContainerID: "c1"})
			next()

			for _, event := range []string{audit.EventExecStart, audit.EventAPIRequest} {
				recs := recordsOfEvent(logger, event)
				if len(recs) != 1 || recs[0].KeyID != tc.keyID {
					t.Fatalf("%s records = %+v, want one with key_id %q", event, recs, tc.keyID)
				}
			}
		})
	}
}

// A request keeps the signing key of the session that dispatched it. When the
// client reconnects under another key (rotation) before the request finishes,
// its api_request, compose_op and exec_start records still carry the old key.
// Not parallel: the fake docker binary is found through PATH.
func TestEdgeAuditKeyIDFollowsDispatchingSession(t *testing.T) {
	installFakeDocker(t, "stderr-ok")
	const keyA, keyB = "aaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbb"

	c, logger, _ := newAuditedTestClient(t)
	c.dockerClient = &fakeDocker{doErr: errors.New("boom"), streamErr: errors.New("boom")}
	stacks := t.TempDir()
	if err := os.MkdirAll(filepath.Join(stacks, "app"), 0o750); err != nil {
		t.Fatal(err)
	}
	c.compose = docker.NewComposeManager(stacks, "1.44", "")

	// Session A reads the requests off its connection.
	c.signedHelloKeyID = keyA
	target := c.currentOutboundTarget()
	// The client reconnects with key B while those requests are still running.
	c.keyIDMu.Lock()
	c.signedHelloKeyID = keyB
	c.keyIDMu.Unlock()

	body, err := json.Marshal(docker.ComposeRequest{Operation: "up", StackName: "app"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	c.handleRequestTo(ctx, protocol.RequestMessage{RequestID: "r1", Method: http.MethodPost, Path: "/_portwing/compose", Body: body}, target)
	c.handleRequestTo(ctx, protocol.RequestMessage{RequestID: "r2", Method: http.MethodPost, Path: "/exec/e1/start"}, target)
	c.handleRequestTo(ctx, protocol.RequestMessage{RequestID: "r3", Method: http.MethodGet, Path: "no-slash"}, target)

	for _, event := range []string{audit.EventComposeOp, audit.EventExecStart, audit.EventAPIRequest} {
		recs := recordsOfEvent(logger, event)
		if len(recs) == 0 {
			t.Fatalf("no %s records", event)
		}
		for _, r := range recs {
			if r.KeyID != keyA {
				t.Fatalf("%s record key_id = %q, want %q (dispatching session)", event, r.KeyID, keyA)
			}
		}
	}
}

// A typed exec that fails to come up after the client reconnects under another
// key still records its exec_start error under the key it was admitted with.
func TestTypedExecBringUpFailureKeepsAdmissionKeyID(t *testing.T) {
	t.Parallel()
	const keyA, keyB = "aaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbb"

	c, logger, next := newAuditedTestClient(t)
	c.signedHelloKeyID = keyA
	c.dockerClient = &fakeDocker{
		createExecErr: errors.New("boom"),
		createHook: func() {
			c.keyIDMu.Lock()
			c.signedHelloKeyID = keyB
			c.keyIDMu.Unlock()
		},
	}

	c.StartExec(context.Background(), protocol.ExecStartMessage{ExecID: "e1", ContainerID: "c1"})
	next()

	recs := recordsOfEvent(logger, audit.EventAPIRequest)
	if len(recs) != 1 || recs[0].KeyID != keyA {
		t.Fatalf("api_request records = %+v, want one with key_id %q", recs, keyA)
	}
}
