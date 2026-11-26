// SPDX-License-Identifier: MIT

package server

import (
	"errors"
	"strings"
	"testing"
)

type failingWriter struct{ writes int }

func (w *failingWriter) Write([]byte) (int, error) {
	w.writes++
	return 0, errors.New("client went away")
}

// A failed client write is a dropped connection, not a daemon failure, so the
// body copy stops quietly and reports success rather than aborting the handler.
func TestCopyProxyBodyClientWriteFailureIsNotADaemonFailure(t *testing.T) {
	t.Parallel()

	w := &failingWriter{}
	if !copyProxyBody(w, strings.NewReader("rest"), []byte("first")) {
		t.Fatal("a failed write of the first chunk must not report a daemon failure")
	}
	if w.writes != 1 {
		t.Fatalf("writes after a failed first chunk = %d, want 1", w.writes)
	}

	w = &failingWriter{}
	if !copyProxyBody(w, strings.NewReader("rest"), nil) {
		t.Fatal("a failed write mid-body must not report a daemon failure")
	}
	if w.writes != 1 {
		t.Fatalf("writes after a failed body write = %d, want 1", w.writes)
	}
}
