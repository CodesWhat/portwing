// SPDX-License-Identifier: MIT

package audit

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type exportMetricsRecorder struct {
	successes int
	errors    int
	stats     Stats
}

func (m *exportMetricsRecorder) IncAuditExport(success bool) {
	if success {
		m.successes++
		return
	}
	m.errors++
}

func (m *exportMetricsRecorder) SetAuditState(records, capacity int, sinkEnabled bool) {
	m.stats = Stats{Records: records, Capacity: capacity, SinkEnabled: sinkEnabled}
}

func newExportTestLogger(t *testing.T, capacity int) *Logger {
	t.Helper()
	logger, closeLogger, err := New("", capacity)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(closeLogger)
	return logger
}

func TestServeExportHTTPCursorAndLimit(t *testing.T) {
	t.Parallel()

	logger := newExportTestLogger(t, 8)
	logger.AuthFailure("a", http.MethodGet, "/1")
	logger.AuthFailure("b", http.MethodGet, "/2")
	logger.AuthFailure("c", http.MethodGet, "/3")
	metrics := &exportMetricsRecorder{}

	req := httptest.NewRequest(http.MethodGet, "/_portwing/audit/export?cursor="+FormatCursor(logger.Stats().Epoch, 1)+"&limit=1", nil)
	rr := httptest.NewRecorder()
	ServeExportHTTP(rr, req, logger, metrics)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "application/x-ndjson" {
		t.Fatalf("content type = %q, want application/x-ndjson", got)
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache control = %q, want no-store", got)
	}
	if got := rr.Header().Get("X-Portwing-Next-Cursor"); got != FormatCursor(logger.Stats().Epoch, 2) {
		t.Fatalf("next cursor = %q, want 2", got)
	}
	if got := rr.Header().Get("X-Portwing-Record-Count"); got != "1" {
		t.Fatalf("record count = %q, want 1", got)
	}

	scanner := bufio.NewScanner(rr.Body)
	if !scanner.Scan() {
		t.Fatal("expected one NDJSON record")
	}
	var record Record
	if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
		t.Fatalf("decode record: %v", err)
	}
	if record.Cursor != 2 || record.Path != "/2" {
		t.Fatalf("record = %+v, want cursor 2 and path /2", record)
	}
	if metrics.successes != 1 || metrics.errors != 0 {
		t.Fatalf("metrics = successes:%d errors:%d, want 1/0", metrics.successes, metrics.errors)
	}
	if metrics.stats.Records != 3 || metrics.stats.Capacity != 8 {
		t.Fatalf("audit stats = %+v, want records 3 capacity 8", metrics.stats)
	}
}

func TestServeExportHTTPRejectsInvalidParameters(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		query string
	}{
		{name: "missing cursor", query: "?cursor="},
		{name: "invalid cursor", query: "?cursor=invalid"},
		{name: "empty epoch", query: "?cursor=.5"},
		{name: "non-numeric sequence", query: "?cursor=abcd.x"},
		{name: "negative sequence", query: "?cursor=abcd.-1"},
		{name: "invalid limit", query: "?limit=invalid"},
		{name: "negative limit", query: "?limit=-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			logger := newExportTestLogger(t, 8)
			metrics := &exportMetricsRecorder{}
			rr := httptest.NewRecorder()
			ServeExportHTTP(
				rr,
				httptest.NewRequest(http.MethodGet, "/_portwing/audit/export"+tc.query, nil),
				logger,
				metrics,
			)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rr.Code)
			}
			if metrics.errors != 1 || metrics.successes != 0 {
				t.Fatalf("metrics = successes:%d errors:%d, want 0/1", metrics.successes, metrics.errors)
			}
		})
	}
}

func TestServeExportHTTPReportsCursorGenerationConflicts(t *testing.T) {
	t.Parallel()

	t.Run("overwritten", func(t *testing.T) {
		t.Parallel()
		logger := newExportTestLogger(t, 2)
		logger.AuthFailure("a", http.MethodGet, "/1")
		logger.AuthFailure("b", http.MethodGet, "/2")
		logger.AuthFailure("c", http.MethodGet, "/3")
		rr := httptest.NewRecorder()
		ServeExportHTTP(rr, httptest.NewRequest(http.MethodGet, "/export?cursor="+FormatCursor(logger.Stats().Epoch, 0), nil), logger, nil)
		if rr.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409", rr.Code)
		}
		if got := rr.Header().Get("X-Portwing-Next-Cursor"); got != FormatCursor(logger.Stats().Epoch, 1) {
			t.Fatalf("reset cursor = %q, want 1", got)
		}
	})

	t.Run("future", func(t *testing.T) {
		t.Parallel()
		logger := newExportTestLogger(t, 2)
		logger.AuthFailure("a", http.MethodGet, "/1")
		rr := httptest.NewRecorder()
		ServeExportHTTP(rr, httptest.NewRequest(http.MethodGet, "/export?cursor="+FormatCursor(logger.Stats().Epoch, 99), nil), logger, nil)
		if rr.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409", rr.Code)
		}
		if got := rr.Header().Get("X-Portwing-Next-Cursor"); got != FormatCursor(logger.Stats().Epoch, 0) {
			t.Fatalf("reset cursor = %q, want 0", got)
		}
	})
}

func TestServeExportHTTPBootstrapsWithoutCursor(t *testing.T) {
	t.Parallel()

	logger := newExportTestLogger(t, 2)
	logger.AuthFailure("a", http.MethodGet, "/1")
	logger.AuthFailure("b", http.MethodGet, "/2")
	logger.AuthFailure("c", http.MethodGet, "/3")
	rr := httptest.NewRecorder()
	ServeExportHTTP(rr, httptest.NewRequest(http.MethodGet, "/export", nil), logger, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("X-Portwing-Next-Cursor"); got != FormatCursor(logger.Stats().Epoch, 3) {
		t.Fatalf("next cursor = %q, want 3", got)
	}
}

func TestExportMetricHelpersAllowOptionalDependencies(t *testing.T) {
	t.Parallel()

	metrics := &exportMetricsRecorder{}
	recordExport(nil, metrics, false)
	if metrics.errors != 1 {
		t.Fatalf("error count = %d, want 1", metrics.errors)
	}
	setExportMetrics(nil, Stats{Records: 1})
	if got := ResetCursor(Stats{}); got != 0 {
		t.Fatalf("empty reset cursor = %d, want 0", got)
	}
	if got := ResetCursor(Stats{OldestCursor: 7}); got != 6 {
		t.Fatalf("reset cursor = %d, want 6", got)
	}
}

// WA-AUDIT-CURSOR: a cursor saved before a restart must be refused, not read as
// a position in the new process's ring.
func TestServeExportHTTPRejectsCursorFromPreviousGeneration(t *testing.T) {
	t.Parallel()

	fill := func(logger *Logger, n int) {
		for i := 0; i < n; i++ {
			logger.AuthFailure("a", http.MethodGet, "/x")
		}
	}

	before := newExportTestLogger(t, 100)
	fill(before, 50)
	rr := httptest.NewRecorder()
	ServeExportHTTP(rr, httptest.NewRequest(http.MethodGet, "/export?limit=42", nil), before, nil)
	saved := rr.Header().Get("X-Portwing-Next-Cursor")
	if rr.Code != http.StatusOK || saved == "" {
		t.Fatalf("mint: status %d, cursor %q", rr.Code, saved)
	}

	// Restart: a new ring that already holds records 1-50, so a bare sequence
	// number inside it would be accepted and silently skip records.
	after := newExportTestLogger(t, 100)
	fill(after, 50)
	if before.Stats().Epoch == after.Stats().Epoch {
		t.Fatal("two loggers share an epoch")
	}
	metrics := &exportMetricsRecorder{}
	rr = httptest.NewRecorder()
	ServeExportHTTP(rr, httptest.NewRequest(http.MethodGet, "/export?cursor="+saved, nil), after, metrics)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() == 0 || strings.Contains(rr.Body.String(), `"event"`) {
		t.Fatalf("409 body = %q, want an error message and no records", rr.Body.String())
	}
	if got, want := rr.Header().Get("X-Portwing-Next-Cursor"), FormatCursor(after.Stats().Epoch, 0); got != want {
		t.Fatalf("reset cursor = %q, want %q", got, want)
	}
	if metrics.errors != 1 {
		t.Fatalf("metrics errors = %d, want 1", metrics.errors)
	}

	// A bare number, the pre-generation format, has no generation to match.
	rr = httptest.NewRecorder()
	ServeExportHTTP(rr, httptest.NewRequest(http.MethodGet, "/export?cursor=42", nil), after, nil)
	if rr.Code != http.StatusConflict {
		t.Fatalf("bare cursor status = %d, want 409", rr.Code)
	}

	// The reset cursor the 409 handed out works against the new process.
	reset := FormatCursor(after.Stats().Epoch, 0)
	rr = httptest.NewRecorder()
	ServeExportHTTP(rr, httptest.NewRequest(http.MethodGet, "/export?cursor="+reset, nil), after, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("reset cursor status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
}

// Cursors minted by the current process keep paging without gaps or repeats.
func TestServeExportHTTPSameGenerationCursorsPage(t *testing.T) {
	t.Parallel()

	logger := newExportTestLogger(t, 100)
	for i := 0; i < 7; i++ {
		logger.AuthFailure("a", http.MethodGet, "/x")
	}
	var seen []uint64
	query := "?"
	for range 10 {
		rr := httptest.NewRecorder()
		ServeExportHTTP(rr, httptest.NewRequest(http.MethodGet, "/export"+query+"limit=3", nil), logger, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
		}
		scanner := bufio.NewScanner(rr.Body)
		n := 0
		for scanner.Scan() {
			var rec Record
			if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
				t.Fatal(err)
			}
			seen = append(seen, rec.Cursor)
			n++
		}
		if n == 0 {
			break
		}
		next := rr.Header().Get("X-Portwing-Next-Cursor")
		if next == "" {
			t.Fatal("empty next cursor")
		}
		query = "?cursor=" + next + "&"
	}
	if len(seen) != 7 {
		t.Fatalf("paged %v, want records 1-7 once each", seen)
	}
	for i, c := range seen {
		if c != uint64(i+1) {
			t.Fatalf("paged %v, want records 1-7 in order", seen)
		}
	}
}
