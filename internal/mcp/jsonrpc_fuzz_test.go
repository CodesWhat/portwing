package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// FuzzMCPHandler feeds arbitrary JSON bodies to the MCP HTTP handler and verifies:
//   - The handler never panics.
//   - The response is always valid JSON (when it has a body).
//   - The response JSONRPC field is always "2.0" on success paths.
func FuzzMCPHandler(f *testing.F) {
	// Seed: valid MCP JSON-RPC requests.
	f.Add(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	f.Add(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	f.Add(`{"jsonrpc":"2.0","id":3,"method":"ping"}`)
	f.Add(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	f.Add(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"list_containers","arguments":{}}}`)
	f.Add(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"inspect_container","arguments":{"id":"abc"}}}`)
	// Seed: hostile inputs.
	f.Add(``)
	f.Add(`{}`)
	f.Add(`{"jsonrpc":"1.0","id":1,"method":"initialize"}`)
	f.Add(`{"jsonrpc":"2.0","id":1,"method":""}`)
	f.Add(`{"jsonrpc":"2.0","id":1,"method":"unknown/method"}`)
	f.Add(`not json at all`)
	f.Add(`{"jsonrpc":"2.0","id":` + strings.Repeat("1", 100) + `,"method":"ping"}`)
	f.Add(`{"jsonrpc":"2.0","id":null,"method":"tools/call","params":null}`)
	f.Add(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + strings.Repeat("x", 10000) + `"}}`)

	// Use a nil docker client and nil collector; the handler must not reach
	// them for the JSON-RPC error paths exercised by fuzz inputs. For the
	// tools/call path that does need docker, a nil docker client will return
	// an error from the tool — which is a valid non-panic outcome.
	h := &Handler{docker: nil, collector: nil}

	f.Fuzz(func(t *testing.T, body string) {
		req := httptest.NewRequest(http.MethodPost, "/_lookout/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		// Must never panic.
		h.ServeHTTP(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		// Status must be a valid HTTP status.
		if resp.StatusCode < 100 || resp.StatusCode > 599 {
			t.Errorf("unexpected status %d", resp.StatusCode)
		}

		// If there's a body, it must be parseable JSON.
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Errorf("reading response body: %v", err)
			return
		}

		if len(b) == 0 {
			return
		}

		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(b, &envelope); err != nil {
			t.Errorf("response body is not valid JSON: %v\nbody: %s", err, b)
			return
		}

		// If there is a jsonrpc field, it must be "2.0".
		if raw, ok := envelope["jsonrpc"]; ok {
			var ver string
			if err := json.Unmarshal(raw, &ver); err != nil || ver != "2.0" {
				t.Errorf("jsonrpc field is not \"2.0\": %s", raw)
			}
		}
	})
}
