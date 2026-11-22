// SPDX-License-Identifier: MIT

package audit

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// keyedWrites runs each attributable record type through l with opts.
func keyedWrites(l *Logger, opts ...Option) {
	l.APIRequest("actor", "GET", "/x", OutcomeAllowed, 200, 1.5, opts...)
	l.ComposeOp("actor", "up", "app", OutcomeAllowed, opts...)
	l.ExecStart("actor", "c1", "e1", OutcomeAllowed, opts...)
}

func sinkKeyIDs(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		id, _ := raw["key_id"].(string)
		ids = append(ids, id)
	}
	return ids
}

func TestKeyAttribution(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		opts []Option
		want string
	}{
		{"with key", []Option{WithKeyID("k1")}, "k1"},
		{"no option", nil, ""},
		{"empty id", []Option{WithKeyID("")}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sink := t.TempDir() + "/audit.log"
			l, cleanup, err := New(sink, 16)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			defer cleanup()

			keyedWrites(l, tc.opts...)
			l.Close()

			recs := l.Records(0)
			if len(recs) != 3 {
				t.Fatalf("ring records = %d, want 3", len(recs))
			}
			for _, r := range recs {
				if r.KeyID != tc.want {
					t.Errorf("%s ring key_id = %q, want %q", r.Event, r.KeyID, tc.want)
				}
			}
			ids := sinkKeyIDs(t, sink)
			if len(ids) != 3 {
				t.Fatalf("sink lines = %d, want 3", len(ids))
			}
			for _, id := range ids {
				if id != tc.want {
					t.Errorf("sink key_id = %q, want %q", id, tc.want)
				}
			}
		})
	}
}
