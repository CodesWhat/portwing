package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codeswhat/portwing/internal/audit"
)

// makeAuditTestServer builds a minimal Server with a real audit Logger that
// has an in-memory ring buffer. The Docker client field is not needed by
// handleAudit, so we pass nil.
func makeAuditTestServer(t *testing.T, bufferSize int) *Server {
	t.Helper()
	l, cleanup, err := audit.New("", bufferSize)
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}
	t.Cleanup(cleanup)
	return &Server{auditor: l}
}

// auditResponse matches the JSON envelope returned by handleAudit.
type auditResponse struct {
	Records []audit.Record `json:"records"`
	Count   int            `json:"count"`
}

func TestHandleAuditEmpty(t *testing.T) {
	t.Parallel()

	s := makeAuditTestServer(t, 8)

	req := httptest.NewRequest(http.MethodGet, "/_portwing/audit", nil)
	rr := httptest.NewRecorder()
	s.handleAudit(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected application/json, got %q", ct)
	}

	var resp auditResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Count != 0 {
		t.Errorf("expected count=0, got %d", resp.Count)
	}
	if resp.Records == nil {
		t.Error("records must not be null on empty buffer")
	}
	if len(resp.Records) != 0 {
		t.Errorf("expected empty records slice, got %d entries", len(resp.Records))
	}
}

func TestHandleAuditRecordsNewestFirst(t *testing.T) {
	t.Parallel()

	s := makeAuditTestServer(t, 8)

	// Push two events in order: auth_failure then api_request.
	s.auditor.AuthFailure("1.2.3.4", "GET", "/first")
	s.auditor.APIRequest("1.2.3.4", "GET", "/second", audit.OutcomeAllowed, 200, 1.5)

	req := httptest.NewRequest(http.MethodGet, "/_portwing/audit", nil)
	rr := httptest.NewRecorder()
	s.handleAudit(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp auditResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Count != 2 {
		t.Fatalf("expected count=2, got %d", resp.Count)
	}
	// Newest-first: api_request was pushed last.
	if resp.Records[0].Event != audit.EventAPIRequest {
		t.Errorf("records[0].Event = %q, want %q", resp.Records[0].Event, audit.EventAPIRequest)
	}
	if resp.Records[1].Event != audit.EventAuthFailure {
		t.Errorf("records[1].Event = %q, want %q", resp.Records[1].Event, audit.EventAuthFailure)
	}
}

func TestHandleAuditLimitParam(t *testing.T) {
	t.Parallel()

	s := makeAuditTestServer(t, 8)

	s.auditor.AuthFailure("a", "GET", "/1")
	s.auditor.AuthFailure("b", "GET", "/2")
	s.auditor.APIRequest("c", "GET", "/3", audit.OutcomeAllowed, 200, 0.5)

	req := httptest.NewRequest(http.MethodGet, "/_portwing/audit?limit=1", nil)
	rr := httptest.NewRecorder()
	s.handleAudit(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp auditResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Count != 1 {
		t.Fatalf("expected count=1 with limit=1, got %d", resp.Count)
	}
	// Should be the newest record.
	if resp.Records[0].Event != audit.EventAPIRequest {
		t.Errorf("records[0].Event = %q, want api_request (newest)", resp.Records[0].Event)
	}
}

func TestHandleAuditDisabledBuffer(t *testing.T) {
	t.Parallel()

	// bufferSize=0 disables the buffer entirely.
	s := makeAuditTestServer(t, 0)

	s.auditor.AuthFailure("x", "GET", "/y")

	req := httptest.NewRequest(http.MethodGet, "/_portwing/audit", nil)
	rr := httptest.NewRecorder()
	s.handleAudit(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp auditResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Count != 0 {
		t.Errorf("expected count=0 with disabled buffer, got %d", resp.Count)
	}
}

func TestHandleAuditInvalidLimitFallsBackToAll(t *testing.T) {
	t.Parallel()

	s := makeAuditTestServer(t, 8)

	s.auditor.AuthFailure("a", "GET", "/1")
	s.auditor.AuthFailure("b", "GET", "/2")

	// Invalid limit should fall back to all records.
	req := httptest.NewRequest(http.MethodGet, "/_portwing/audit?limit=notanumber", nil)
	rr := httptest.NewRecorder()
	s.handleAudit(rr, req)

	var resp auditResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Count != 2 {
		t.Errorf("expected count=2 for invalid limit, got %d", resp.Count)
	}
}
