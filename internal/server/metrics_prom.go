package server

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/codeswhat/portwing/internal/metrics"
	"github.com/codeswhat/portwing/internal/protocol"
)

// handleMetrics emits host and per-container metrics in Prometheus text
// exposition format (version 0.0.4). It is registered at both
// GET /_portwing/metrics and GET /metrics.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder

	fmt.Fprintf(&b, "# HELP portwing_build_info Portwing agent build metadata.\n")
	fmt.Fprintf(&b, "# TYPE portwing_build_info gauge\n")
	fmt.Fprintf(&b, "portwing_build_info{version=\"%s\"} 1\n", escapeLabelValue(protocol.AgentVersion))
	fmt.Fprintf(&b, "# HELP portwing_uptime_seconds Seconds since the agent started.\n")
	fmt.Fprintf(&b, "# TYPE portwing_uptime_seconds gauge\n")
	fmt.Fprintf(&b, "portwing_uptime_seconds %g\n", time.Since(s.startTime).Seconds())

	metrics.WriteHostPrometheus(&b, s.collector)
	metrics.WriteContainerPrometheus(r.Context(), &b, s.dockerClient, escapeLabelValue)

	if s.metrics != nil {
		if s.auditor != nil {
			s.setAuditMetrics(s.auditor.Stats())
		}
		s.metrics.WritePrometheus(&b, escapeLabelValue)
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, b.String())
}

// escapeLabelValue escapes a Prometheus label value per the exposition format
// spec: backslash -> \\, double-quote -> \", newline -> \n.
func escapeLabelValue(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}
