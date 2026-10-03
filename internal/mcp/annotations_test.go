package mcp

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// toolAnnotationResponses returns tools/list responses keyed by protocol
// revision.
func toolAnnotationResponses(t *testing.T, h *Handler) map[string]*httptest.ResponseRecorder {
	t.Helper()
	return map[string]*httptest.ResponseRecorder{
		"2025-11-25": postMCPRaw(h, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`),
		"2026-07-28": postWithHeaders(h, modernBody(t, 1, "tools/list", nil, modernMeta()), modernHeaders("tools/list")),
	}
}

func TestToolAnnotations(t *testing.T) {
	h := NewHandler(nil, nil)
	wantOpenWorld := map[string]bool{
		"list_containers":   false,
		"inspect_container": false,
		"container_logs":    true,
		"host_metrics":      false,
		"container_stats":   false,
	}
	for revision, rr := range toolAnnotationResponses(t, h) {
		result, _ := decodeResponse(t, rr).Result.(map[string]any)
		tools, _ := result["tools"].([]any)
		if len(tools) != len(wantOpenWorld) {
			t.Fatalf("%s: %d tools, want %d", revision, len(tools), len(wantOpenWorld))
		}
		for _, raw := range tools {
			tool, _ := raw.(map[string]any)
			name, _ := tool["name"].(string)
			annotations, ok := tool["annotations"].(map[string]any)
			if !ok {
				t.Errorf("%s: %s has no annotations", revision, name)
				continue
			}
			if annotations["readOnlyHint"] != true {
				t.Errorf("%s: %s readOnlyHint = %v, want true", revision, name, annotations["readOnlyHint"])
			}
			if annotations["openWorldHint"] != wantOpenWorld[name] {
				t.Errorf("%s: %s openWorldHint = %v, want %v", revision, name, annotations["openWorldHint"], wantOpenWorld[name])
			}
			if title, _ := annotations["title"].(string); title == "" {
				t.Errorf("%s: %s has no annotations.title", revision, name)
			}
			if len(annotations) != 3 {
				t.Errorf("%s: %s annotations = %v, want only title, readOnlyHint, openWorldHint", revision, name, annotations)
			}
		}
	}
}

// TestEncodedToolsMatchDirectEncoding checks that the once-per-process
// encoding of the tool list writes the same bytes as encoding the
// definitions on every request.
func TestEncodedToolsMatchDirectEncoding(t *testing.T) {
	direct := httptest.NewRecorder()
	writeResult(direct, json.RawMessage("1"), map[string]any{"tools": toolDefinitions()})
	cached := httptest.NewRecorder()
	writeResult(cached, json.RawMessage("1"), (&Handler{}).toolsList())
	if !bytes.Equal(direct.Body.Bytes(), cached.Body.Bytes()) {
		t.Errorf("cached tools encoding differs:\n%s\n%s", direct.Body.String(), cached.Body.String())
	}
}
