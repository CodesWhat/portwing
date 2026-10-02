package drydock

// routes_mutant_test.go adds tests that target Gremlins mutants surviving in
// routes.go.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestHandleContainerLogsTimestampsParameter pins the second disjunct of the
// `timestamps == "1" || timestamps == "true"` pair, killing the
// CONDITIONALS_NEGATION mutant at routes.go:61:90. Negated, every value except
// the literal "true" turns timestamps on, including a request that never
// mentioned them. The existing test only sends timestamps=1, which the first
// disjunct answers on its own.
func TestHandleContainerLogsTimestampsParameter(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name  string
		query string
		want  bool
	}{
		{name: "absent", query: "", want: false},
		{name: "true", query: "?timestamps=true", want: true},
		{name: "1", query: "?timestamps=1", want: true},
		{name: "false", query: "?timestamps=false", want: false},
		{name: "0", query: "?timestamps=0", want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client, calls, shutdown := newRouteTestDockerClient(t)
			defer shutdown()

			a := NewAdapter(client, "test-agent", AgentInfo{})

			req := httptest.NewRequest(http.MethodGet, "/api/containers/container-1/logs"+tt.query, nil)
			req.SetPathValue("id", "container-1")
			rec := httptest.NewRecorder()

			a.handleContainerLogs(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			rawQuery, _ := calls.lastLogsRawQuery.Load().(string)
			daemonQuery, err := url.ParseQuery(rawQuery)
			if err != nil {
				t.Fatalf("parse daemon query %q: %v", rawQuery, err)
			}
			if got := daemonQuery.Get("timestamps") == "1"; got != tt.want {
				t.Fatalf("daemon query %q: timestamps on = %v, want %v", rawQuery, got, tt.want)
			}
		})
	}
}
