// SPDX-License-Identifier: MIT

package edge

import (
	"context"
	"errors"
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
