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

// handleAuditExport streams retained records as newline-delimited JSON in
// oldest-first order. cursor is the last successfully consumed record cursor;
// responses contain only records after it. A 409 reports when that cursor fell
// behind the ring's retained window, allowing exporters to surface data loss
// instead of silently skipping overwritten records.
func (s *Server) handleAuditExport(w http.ResponseWriter, r *http.Request) {
	cursor := uint64(0)
	rawCursor, cursorProvided := r.URL.Query()["cursor"]
	if cursorProvided {
		raw := ""
		if len(rawCursor) > 0 {
			raw = rawCursor[0]
		}
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			s.recordAuditExport(false)
			http.Error(w, "invalid cursor", http.StatusBadRequest)
			return
		}
		cursor = parsed
	}

	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			s.recordAuditExport(false)
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		limit = parsed
	}

	records, stats := s.auditor.RecordsAfter(cursor, limit)
	s.setAuditMetrics(stats)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Portwing-Oldest-Cursor", strconv.FormatUint(stats.OldestCursor, 10))
	w.Header().Set("X-Portwing-Latest-Cursor", strconv.FormatUint(stats.LatestCursor, 10))
	if cursorProvided && stats.OldestCursor > 0 && cursor < stats.OldestCursor-1 {
		w.Header().Set("X-Portwing-Next-Cursor", strconv.FormatUint(auditResetCursor(stats), 10))
		s.recordAuditExport(false)
		http.Error(w, "audit cursor is older than the retained buffer", http.StatusConflict)
		return
	}
	if cursorProvided && cursor > stats.LatestCursor {
		w.Header().Set("X-Portwing-Next-Cursor", strconv.FormatUint(auditResetCursor(stats), 10))
		s.recordAuditExport(false)
		http.Error(w, "audit cursor is newer than the current buffer generation", http.StatusConflict)
		return
	}

	nextCursor := cursor
	if len(records) > 0 {
		nextCursor = records[len(records)-1].Cursor
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Portwing-Next-Cursor", strconv.FormatUint(nextCursor, 10))
	w.Header().Set("X-Portwing-Record-Count", strconv.Itoa(len(records)))

	encoder := json.NewEncoder(w)
	for i := range records {
		if err := encoder.Encode(records[i]); err != nil {
			s.recordAuditExport(false)
			return
		}
	}
	s.recordAuditExport(true)
}

func auditResetCursor(stats audit.Stats) uint64 {
	if stats.OldestCursor == 0 {
		return 0
	}
	return stats.OldestCursor - 1
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
