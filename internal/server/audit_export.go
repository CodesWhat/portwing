package server

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/codeswhat/portwing/internal/audit"
)

// handleAudit returns recent audit records from the in-memory ring buffer as JSON.
// Accepts an optional ?limit= query param; defaults to all buffered records.
//
// Response shape:
//
//	{"records": [...], "count": <n>}
//
// Records are ordered newest-first.
func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}

	records := s.auditor.Records(limit)
	if records == nil {
		records = []audit.Record{}
	}

	resp := struct {
		Records []audit.Record `json:"records"`
		Count   int            `json:"count"`
	}{
		Records: records,
		Count:   len(records),
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		if s.metrics != nil {
			s.metrics.IncAuditExport(false)
		}
		return
	}
	s.recordAuditExport(true)
}

func (s *Server) handleAuditExport(w http.ResponseWriter, r *http.Request) {
	var exportMetrics audit.ExportMetrics
	if s.metrics != nil {
		exportMetrics = s.metrics
	}
	audit.ServeExportHTTP(w, r, s.auditor, exportMetrics)
}

func auditResetCursor(stats audit.Stats) uint64 {
	return audit.ResetCursor(stats)
}

func (s *Server) recordAuditExport(success bool) {
	if s.metrics == nil {
		return
	}
	s.metrics.IncAuditExport(success)
	if s.auditor != nil {
		s.setAuditMetrics(s.auditor.Stats())
	}
}

func (s *Server) setAuditMetrics(stats audit.Stats) {
	if s.metrics != nil {
		s.metrics.SetAuditState(stats.Records, stats.Capacity, stats.SinkEnabled)
	}
}
