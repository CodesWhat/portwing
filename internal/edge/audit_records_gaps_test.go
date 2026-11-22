// SPDX-License-Identifier: MIT

package edge

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/docker"
	"github.com/codeswhat/portwing/internal/protocol"
)

// An Edge compose request writes one compose_op record carrying the
// operation and stack, allowed on a zero exit and error on a failed one.
// Not parallel: the fake docker binary is found through PATH.
func TestEdgeComposeAuditsOutcome(t *testing.T) {
	for _, tc := range []struct {
		mode, outcome string
	}{
		{"stderr-ok", audit.OutcomeAllowed},
		{"exit3", audit.OutcomeError},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			installFakeDocker(t, tc.mode)
			c, logger, next := newAuditedTestClient(t)
			stacks := t.TempDir()
			if err := os.MkdirAll(filepath.Join(stacks, "app"), 0o750); err != nil {
				t.Fatal(err)
			}
			c.compose = docker.NewComposeManager(stacks, "1.44", "")

			body, err := json.Marshal(docker.ComposeRequest{Operation: "up", StackName: "app"})
			if err != nil {
				t.Fatal(err)
			}
			c.handleRequest(context.Background(), protocol.RequestMessage{
				RequestID: "compose-audit", Method: http.MethodPost, Path: "/_portwing/compose", Body: body,
			})
			if env := next(); env.Type != protocol.TypeResponse {
				t.Fatalf("frame = %q, want response", env.Type)
			}

			records := recordsOfEvent(logger, audit.EventComposeOp)
			if len(records) != 1 {
				t.Fatalf("compose_op records = %+v, want one", records)
			}
			r := records[0]
			if r.Outcome != tc.outcome || r.Operation != "up" || r.Stack != "app" {
				t.Fatalf("compose_op record = %+v, want outcome %q, operation up, stack app", r, tc.outcome)
			}
			if got := len(recordsOfEvent(logger, audit.EventAPIRequest)); got != 0 {
				t.Fatalf("compose request also wrote %d api_request records", got)
			}
		})
	}
}

// A buffered (non-stream) Edge request writes one api_request record when the
// response is relayed: allowed with the daemon's status, the query stripped
// from the path, and a duration; a transport failure writes an error record.
func TestEdgeBufferedRequestAuditsAPIRequest(t *testing.T) {
	t.Parallel()

	c, logger, next := newAuditedTestClient(t)
	c.dockerClient = &fakeDocker{doResp: mkResp(http.StatusOK, "application/json", `[]`)} //nolint:bodyclose // handleRequest closes the body

	c.handleRequest(context.Background(), protocol.RequestMessage{
		RequestID: "buffered", Method: http.MethodGet, Path: "/containers/json?all=1&secret=x",
	})
	if env := next(); env.Type != protocol.TypeResponse {
		t.Fatalf("frame = %q, want response", env.Type)
	}
	records := recordsOfEvent(logger, audit.EventAPIRequest)
	if len(records) != 1 {
		t.Fatalf("api_request records = %+v, want one", records)
	}
	r := records[0]
	if r.Outcome != audit.OutcomeAllowed || r.Status != http.StatusOK || r.Method != http.MethodGet || r.Path != "/containers/json" || r.Actor == "" {
		t.Fatalf("api_request record = %+v", r)
	}

	c2, logger2, next2 := newAuditedTestClient(t)
	c2.dockerClient = &fakeDocker{doErr: context.DeadlineExceeded}
	c2.handleRequest(context.Background(), protocol.RequestMessage{RequestID: "failed", Method: http.MethodGet, Path: "/containers/json"})
	if env := next2(); env.Type != protocol.TypeError {
		t.Fatalf("frame = %q, want error", env.Type)
	}
	records = recordsOfEvent(logger2, audit.EventAPIRequest)
	if len(records) != 1 || records[0].Outcome != audit.OutcomeError {
		t.Fatalf("failed request records = %+v, want one error record", records)
	}
}
