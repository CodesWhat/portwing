// SPDX-License-Identifier: MIT

package server

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/auth"
)

func TestNewServerWarnsWhenV3UnavailableWithPersistedLocks(t *testing.T) {
	// Not parallel: installLogCapture swaps the default logger.
	for _, tc := range []struct {
		name     string
		reason   string
		locked   int
		wantWarn bool
		remedy   string
	}{
		{"unavailable with locks", "state-not-writable", 2, true, "restore a writable state directory"},
		{"unavailable without locks", "state-not-writable", 0, false, ""},
		{"available with locks", "", 2, false, ""},
		{"agent id not v3 compatible", "agent-id-not-v3-compatible", 2, true, "fix AGENT_ID"},
		{"agent id not persistent", "agent-id-not-persistent", 2, true, "mount a writable persistent state directory"},
		{"unknown reason", "something-else", 2, true, "resolve the cause named in"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			// #nosec G302 -- private test fixture.
			if err := os.Chmod(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			seed, err := auth.OpenSignatureLocks(dir)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < tc.locked; i++ {
				if _, err := seed.Lock(fmt.Sprintf("%016x", i+1), time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			h := installLogCapture(t)
			newV3ServerWith(t, dir, 0, false, tc.reason)
			if got := h.has("signature-lock clear"); got != tc.wantWarn {
				t.Fatalf("warned = %v, want %v", got, tc.wantWarn)
			}
			if tc.wantWarn && !h.has("2 key(s) are locked") {
				t.Fatal("warning must name the locked key count")
			}
			if tc.wantWarn {
				if !h.has(tc.remedy) {
					t.Fatalf("warning must carry the %q remedy", tc.remedy)
				}
				if !h.has("reopens the downgrade") {
					t.Fatal("warning must say clearing a lock reopens the downgrade")
				}
				if tc.reason != "state-not-writable" && h.has("restore a writable state directory") {
					t.Fatalf("reason %q must not get the state-directory remedy", tc.reason)
				}
			}
		})
	}
}
