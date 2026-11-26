// SPDX-License-Identifier: MIT

package edge

// request_path_cheap_checks_test.go covers two cheap parity checks on the Edge
// request path: encoded dot and slash spellings must reach the daemon verbatim
// under the version prefix (never decoded, cleaned or followed out of it), and
// an over-cap buffered response must be refused without reading past the cap.

import (
	"context"
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/codeswhat/portwing/internal/protocol"
)

func TestEdgeEncodedDotSegmentsStayVerbatimUnderVersionPrefix(t *testing.T) {
	t.Parallel()

	paths := []string{
		"/%2e%2e/containers/json",
		"/%2E%2E/containers/json",
		"/%2e%2E/containers/json",
		"/%2e./containers/json",
		"/.%2e/containers/json",
		"/containers/%2e%2e/%2e%2e/info",
		"/containers%2f..%2finfo",
		"/containers%2F%2e%2e%2Finfo",
		"/..%2fcontainers/json",
		"/%252e%252e/containers/json",
		"/containers/%252e%252e/info",
		"/containers/%2e%2e%2f%2e%2e%2finfo",
		"/%2e%2e",
		"/%2e%2e/_portwing/compose",
		"/%2e%2e%2f_portwing%2fcompose",
	}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			t.Parallel()
			daemon := &edgeCleaningDaemon{}
			c, _, next := newAuditedTestClient(t)
			c.dockerClient = newEdgeCleaningDockerClient(t, daemon)

			c.handleRequest(context.Background(), protocol.RequestMessage{
				RequestID: "enc", Method: http.MethodGet, Path: p,
			})
			next()

			if hits := daemon.forbiddenHits(); len(hits) != 0 {
				t.Fatalf("daemon reached a forbidden route for %q: %v", p, hits)
			}
			got := daemon.seen()
			if len(got) != 1 {
				t.Fatalf("daemon saw %v, want exactly one request and no followed redirect", got)
			}
			if got[0] != "/v1.44"+p {
				t.Fatalf("daemon saw %q, want the verbatim version-prefixed %q", got[0], "/v1.44"+p)
			}
		})
	}
}

// endlessReader yields filler forever and counts what was actually read.
type endlessReader struct{ read atomic.Int64 }

func (r *endlessReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	r.read.Add(int64(len(p)))
	return len(p), nil
}

func TestOverCapResponseIsNeverBufferedPastTheCap(t *testing.T) {
	t.Parallel()

	reader := &endlessReader{}
	c, ctrl := newTestClient(t)
	//nolint:bodyclose // closed by handleRequest, the code under test.
	c.dockerClient = &fakeDocker{doResp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(reader),
	}}

	c.handleRequest(context.Background(), protocol.RequestMessage{RequestID: "endless", Method: "GET", Path: "/images/json"})

	var e protocol.ErrorMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeError), &e)
	if e.RequestID != "endless" || e.Code != protocol.ErrorCodeResponseTooLarge {
		t.Fatalf("error = %+v, want response_too_large for request endless", e)
	}
	// The LimitReader hands the source exactly maxResponseBody+1 bytes: the cap
	// plus the one overflow probe byte. endlessReader counts what it was asked
	// to fill, and io.ReadAll's buffer only requests what the limit still
	// allows, so any read past the probe byte is a loosened cap.
	if got := reader.read.Load(); got > maxResponseBody+1 {
		t.Fatalf("read %d bytes, want at most %d (cap plus the overflow probe byte)", got, int64(maxResponseBody)+1)
	}
}
