package audit

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// ExportMetrics is the operational metrics surface used by ServeExportHTTP.
// *metrics.Registry satisfies it without coupling the audit package to the
// concrete metrics implementation.
type ExportMetrics interface {
	IncAuditExport(success bool)
	SetAuditState(records, capacity int, sinkEnabled bool)
}

// ServeExportHTTP streams retained audit records as newline-delimited JSON in
// oldest-first order. cursor is the last successfully consumed record cursor;
// responses contain only records after it. A 409 reports when that cursor fell
// behind the ring's retained window, allowing exporters to surface data loss
// instead of silently skipping overwritten records.
func ServeExportHTTP(w http.ResponseWriter, r *http.Request, logger *Logger, metrics ExportMetrics) {
	cursor := uint64(0)
	rawCursor, cursorProvided := r.URL.Query()["cursor"]
	if cursorProvided {
		raw := ""
		if len(rawCursor) > 0 {
			raw = rawCursor[0]
		}
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			recordExport(logger, metrics, false)
			http.Error(w, "invalid cursor", http.StatusBadRequest)
			return
		}
		cursor = parsed
	}

	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			recordExport(logger, metrics, false)
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		limit = parsed
	}

	records, stats := logger.RecordsAfter(cursor, limit)
	setExportMetrics(metrics, stats)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Portwing-Oldest-Cursor", strconv.FormatUint(stats.OldestCursor, 10))
	w.Header().Set("X-Portwing-Latest-Cursor", strconv.FormatUint(stats.LatestCursor, 10))
	if cursorProvided && stats.OldestCursor > 0 && cursor < stats.OldestCursor-1 {
		w.Header().Set("X-Portwing-Next-Cursor", strconv.FormatUint(ResetCursor(stats), 10))
		recordExport(logger, metrics, false)
		http.Error(w, "audit cursor is older than the retained buffer", http.StatusConflict)
		return
	}
	if cursorProvided && cursor > stats.LatestCursor {
		w.Header().Set("X-Portwing-Next-Cursor", strconv.FormatUint(ResetCursor(stats), 10))
		recordExport(logger, metrics, false)
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
			recordExport(logger, metrics, false)
			return
		}
	}
	recordExport(logger, metrics, true)
}

// ResetCursor returns the cursor an exporter should persist when its requested
// cursor is outside the current buffer generation.
func ResetCursor(stats Stats) uint64 {
	if stats.OldestCursor == 0 {
		return 0
	}
	return stats.OldestCursor - 1
}

func recordExport(logger *Logger, metrics ExportMetrics, success bool) {
	if metrics == nil {
		return
	}
	metrics.IncAuditExport(success)
	if logger != nil {
		setExportMetrics(metrics, logger.Stats())
	}
}

func setExportMetrics(metrics ExportMetrics, stats Stats) {
	if metrics != nil {
		metrics.SetAuditState(stats.Records, stats.Capacity, stats.SinkEnabled)
	}
}
