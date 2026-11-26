// SPDX-License-Identifier: MIT

package edge

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/config"
	"github.com/codeswhat/portwing/internal/protocol"
)

// readHookBody runs onRead on every Read and returns what it says.
type readHookBody struct {
	onRead func(call int) (int, error)
	calls  int
}

func (b *readHookBody) Read(p []byte) (int, error) {
	b.calls++
	n, err := b.onRead(b.calls)
	if n > 0 {
		copy(p, strings.Repeat("x", n))
	}
	return n, err
}
func (b *readHookBody) Close() error { return nil }

// hookDocker serves one canned response for stream and buffered calls.
type hookDocker struct {
	*fakeDocker
	body io.ReadCloser
}

func (d *hookDocker) resp() *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: d.body}
}

func (d *hookDocker) DoStreamWithHeaders(context.Context, string, string, http.Header, io.Reader) (*http.Response, error) {
	return d.resp(), nil
}

func (d *hookDocker) DoWithDeadline(context.Context, string, string, http.Header, io.Reader, time.Duration) (*http.Response, error) {
	return d.resp(), nil
}

func newHookClient(t *testing.T, body io.ReadCloser) *Client {
	t.Helper()
	c, _ := newTestClient(t)
	c.dockerClient = &hookDocker{fakeDocker: &fakeDocker{}, body: body}
	auditor, closeAudit, err := audit.New("", 32)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeAudit)
	c.auditor = auditor
	return c
}

func assertCancelledAudit(t *testing.T, c *Client, reason string) {
	t.Helper()
	recs := c.auditor.Records(5)
	if len(recs) == 0 {
		t.Fatal("no audit records")
	}
	if recs[0].Outcome != audit.OutcomeCancelled || recs[0].Reason != reason {
		t.Fatalf("audit = %+v, want cancelled with reason %q", recs[0], reason)
	}
}

// A dd:cancel that lands while a chunk is being read stops the stream at the
// top of the next iteration, with no further chunk.
func TestStreamStopsAtLoopTopWhenCancelledBetweenChunks(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	body := &readHookBody{onRead: func(call int) (int, error) {
		if call > 1 {
			t.Error("stream kept reading after the cancel")
		}
		cancel(&controllerCancel{reason: "between-chunks"})
		return 5, nil
	}}
	c := newHookClient(t, body)
	target := outboundTarget{ch: make(chan protocol.Envelope, 8), state: &outboundQueueState{}}

	c.handleRequestTo(ctx, protocol.RequestMessage{RequestID: "s", Method: "GET", Path: "/events"}, target)
	assertCancelledAudit(t, c, "between-chunks")
}

// A dd:cancel while the send queue is full ends the stream as cancelled, not
// as a failed send.
func TestStreamCancelWhileSendQueueFullIsCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	ch := make(chan protocol.Envelope, 4)
	target := outboundTarget{ch: ch, state: &outboundQueueState{}}
	body := &readHookBody{onRead: func(int) (int, error) {
		// Fill the queue to the bulk high-water mark so the next bulk send
		// has to wait, then cancel while it waits.
		for len(ch) < bulkFrameLimit(ch) {
			ch <- protocol.Envelope{Type: protocol.TypePing}
		}
		cancel(&controllerCancel{reason: "queue-full"})
		return 5, nil
	}}
	c := newHookClient(t, body)

	c.handleRequestTo(ctx, protocol.RequestMessage{RequestID: "s", Method: "GET", Path: "/events"}, target)
	assertCancelledAudit(t, c, "queue-full")
}

// A body read that fails because of a dd:cancel on a buffered response is a
// cancellation, not a daemon error.
func TestBufferedBodyReadFailureFromCancelIsCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	body := &readHookBody{onRead: func(int) (int, error) {
		cancel(&controllerCancel{reason: "body-read"})
		return 0, errors.New("read aborted")
	}}
	c := newHookClient(t, body)
	target := outboundTarget{ch: make(chan protocol.Envelope, 8), state: &outboundQueueState{}}

	c.handleRequestTo(ctx, protocol.RequestMessage{RequestID: "b", Method: "GET", Path: "/containers/json"}, target)
	assertCancelledAudit(t, c, "body-read")
	select {
	case env := <-target.ch:
		t.Fatalf("a cancelled request sent its own frame: %+v", env)
	default:
	}
}

func TestControllerCancelErrorText(t *testing.T) {
	t.Parallel()
	if got := (&controllerCancel{reason: "r"}).Error(); got != "request cancelled by controller" {
		t.Fatalf("Error() = %q", got)
	}
}

func TestConfirmEnrollmentKeepsStateWhenMarkerRemovalFails(t *testing.T) {
	t.Parallel()
	notADir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(notADir, "enrolled-edge-key.pending")
	c := &Client{cfg: &config.Config{EnrollMarkerPath: marker, EnrollCode: testEnrollCode}}

	c.confirmEnrollment()

	if c.cfg.EnrollMarkerPath != marker || c.cfg.EnrollCode != testEnrollCode {
		t.Fatalf("a failed confirmation must keep the pending state, got marker=%q code set=%v",
			c.cfg.EnrollMarkerPath, c.cfg.EnrollCode != "")
	}
}

func TestEnrollRejectionHintIgnoresUnrelatedCodes(t *testing.T) {
	t.Parallel()
	c := &Client{cfg: &config.Config{EnrollMarkerPath: "/state/pending"}}
	for _, code := range []string{"unauthorized", "rate-limited", ""} {
		if got := c.enrollRejectionHint(code); got != "" {
			t.Errorf("enrollRejectionHint(%q) = %q, want none", code, got)
		}
	}
	if got := c.enrollRejectionHint("enroll-key-invalid"); got == "" {
		t.Error("an enroll- code must get a hint")
	}
}
