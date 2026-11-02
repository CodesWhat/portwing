// SPDX-License-Identifier: MIT

package server

import (
	"os"
	"testing"

	"github.com/codeswhat/portwing/internal/audit"
)

// A lock that cannot be written is logged and still audited, and the request
// that triggered it succeeds.
func TestV3LockPersistFailureStillAuditsAndServes(t *testing.T) {
	dir := t.TempDir()
	s, priv := newV3Server(t, dir, 0, false)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if rec := doV3(s, infoRequest(t, "3", priv, v3TestAgentID)); rec.Code != 200 {
		t.Fatalf("v3 with an unwritable state dir: %d %s", rec.Code, rec.Body.String())
	}
	var locks int
	for _, r := range s.auditor.Records(0) {
		if r.Event == audit.EventSignatureLock {
			locks++
		}
	}
	if locks != 1 {
		t.Fatalf("signature_lock events = %d, want 1", locks)
	}
	// The lock holds in memory, so the downgrade stays closed.
	if rec := doV3(s, infoRequest(t, "2", priv, "")); rec.Code != 401 {
		t.Fatalf("v2 after an unpersisted lock: %d", rec.Code)
	}
}
