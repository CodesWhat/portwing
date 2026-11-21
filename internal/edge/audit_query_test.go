// SPDX-License-Identifier: MIT

package edge

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/protocol"
)

const (
	secretQuery   = "buildargs=%7B%22TOKEN%22%3A%22hunter2%22%7D&remote=https%3A%2F%2Fuser%3Apassw0rd%40git.example%2Frepo.git"
	secretNeedle1 = "hunter2"
	secretNeedle2 = "passw0rd"
)

// TestAuditAndWarningsDropRequestQuery verifies the edge request path recorded
// by the audit sink, the in-memory ring, the export output and the warning log
// carries no query string.
// Deliberately NOT t.Parallel(): captures the process-global slog default.
func TestAuditAndWarningsDropRequestQuery(t *testing.T) {
	sink := filepath.Join(t.TempDir(), "audit.log")
	logger, closeAudit, err := audit.New(sink, 64)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeAudit)

	c, _ := newTestClient(t)
	c.auditor = logger

	logBuf := &bytes.Buffer{}
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(oldLogger)

	// Non-stream success, the daemon error path, and a stream that dies early
	// (which logs a warning and an error record).
	c.dockerClient = &fakeDocker{doResp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("{}")),
	}}
	c.handleRequest(context.Background(), protocol.RequestMessage{RequestID: "ok", Method: "GET", Path: "/images/json?" + secretQuery})

	c.dockerClient = &fakeDocker{doErr: io.ErrUnexpectedEOF}
	c.handleRequest(context.Background(), protocol.RequestMessage{RequestID: "err", Method: "GET", Path: "/images/json?" + secretQuery})

	c.dockerClient = &fakeDocker{streamResp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(iotest.ErrReader(io.ErrUnexpectedEOF)),
	}}
	c.handleRequest(context.Background(), protocol.RequestMessage{RequestID: "stream", Method: "GET", Path: "/containers/abc/logs?follow=1&" + secretQuery})

	records := logger.Records(0)
	if len(records) < 3 {
		t.Fatalf("expected at least 3 audit records, got %d: %+v", len(records), records)
	}
	for _, r := range records {
		if strings.Contains(r.Path, "?") || strings.Contains(r.Path, "=") {
			t.Errorf("audit record path kept a query: %q", r.Path)
		}
	}

	rec := httptest.NewRecorder()
	audit.ServeExportHTTP(rec, httptest.NewRequest(http.MethodGet, "/audit", nil), logger, nil)
	raw, err := os.ReadFile(sink) // #nosec G304 -- test temp file
	if err != nil {
		t.Fatal(err)
	}

	for name, out := range map[string]string{
		"export":     rec.Body.String(),
		"audit sink": string(raw),
		"warn log":   logBuf.String(),
	} {
		for _, needle := range []string{secretNeedle1, secretNeedle2, "buildargs", "remote="} {
			if strings.Contains(out, needle) {
				t.Errorf("%s leaked %q:\n%s", name, needle, out)
			}
		}
	}
	if !strings.Contains(logBuf.String(), "docker response stream ended early") {
		t.Fatalf("stream warning was not logged; log = %q", logBuf.String())
	}
}
