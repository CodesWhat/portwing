package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/codeswhat/portwing/internal/protocol"
)

// Tests for protocol revision 2026-07-28 and for the negotiation that keeps
// 2025-11-25 traffic on its original path.

// modernResponse is a decoded response with the error data kept raw so tests
// can compare it exactly.
type modernResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  map[string]any  `json:"result"`
	Error   *struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	} `json:"error"`
}

// modernMeta is a valid 2026-07-28 request envelope.
func modernMeta() map[string]any {
	return map[string]any{
		metaProtocolVersion:                  modernProtocolVersion,
		metaClientCapabilities:               map[string]any{},
		"io.modelcontextprotocol/clientInfo": map[string]any{"name": "test", "version": "0.0.1"},
	}
}

// modernBody returns a request body whose params carry meta as _meta. A nil
// meta leaves _meta out.
func modernBody(t *testing.T, id any, method string, params map[string]any, meta map[string]any) string {
	t.Helper()
	if params == nil {
		params = map[string]any{}
	}
	if meta != nil {
		params["_meta"] = meta
	}
	b, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return string(b)
}

// modernHeaders returns the standard request headers for method.
func modernHeaders(method string) map[string]string {
	return map[string]string{"MCP-Protocol-Version": modernProtocolVersion, "Mcp-Method": method}
}

func postWithHeaders(h *Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/_portwing/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func decodeModern(t *testing.T, rr *httptest.ResponseRecorder) modernResponse {
	t.Helper()
	var resp modernResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v\nbody: %s", err, rr.Body.String())
	}
	return resp
}

// wantModernError asserts the HTTP status and JSON-RPC error code.
func wantModernError(t *testing.T, rr *httptest.ResponseRecorder, status, code int) modernResponse {
	t.Helper()
	resp := decodeModern(t, rr)
	if rr.Code != status {
		t.Errorf("status = %d, want %d (body %s)", rr.Code, status, rr.Body.String())
	}
	if resp.Error == nil {
		t.Fatalf("error = nil, want code %d (body %s)", code, rr.Body.String())
	}
	if resp.Error.Code != code {
		t.Errorf("error code = %d, want %d (message %q)", resp.Error.Code, code, resp.Error.Message)
	}
	if resp.Result != nil {
		t.Errorf("error response also carries a result: %v", resp.Result)
	}
	return resp
}

// wantModernResult asserts a 200 result with the 2026-07-28 envelope fields.
func wantModernResult(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	resp := decodeModern(t, rr)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rr.Code, rr.Body.String())
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if got := resp.Result["resultType"]; got != "complete" {
		t.Errorf("resultType = %v, want complete", got)
	}
	meta, _ := resp.Result["_meta"].(map[string]any)
	info, _ := meta[metaServerInfo].(map[string]any)
	if info["name"] != "portwing" || info["version"] != protocol.AgentVersion {
		t.Errorf("_meta serverInfo = %v, want portwing %s", info, protocol.AgentVersion)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	return resp.Result
}

func TestModernDiscover(t *testing.T) {
	h := NewHandler(nil, nil)
	rr := postWithHeaders(h, modernBody(t, "discover-1", "server/discover", nil, modernMeta()), modernHeaders("server/discover"))
	result := wantModernResult(t, rr)

	want := map[string]any{
		"supportedVersions": []any{modernProtocolVersion},
		"capabilities":      map[string]any{"tools": map[string]any{}},
		"ttlMs":             float64(3_600_000),
		"cacheScope":        "public",
	}
	for key, value := range want {
		if !reflect.DeepEqual(result[key], value) {
			t.Errorf("%s = %#v, want %#v", key, result[key], value)
		}
	}
	if len(result) != len(want)+2 {
		t.Errorf("discover result has unexpected fields: %v", result)
	}
	if string(decodeModern(t, rr).ID) != `"discover-1"` {
		t.Errorf("response id = %s, want \"discover-1\"", decodeModern(t, rr).ID)
	}
}

func TestModernToolsListMatchesLegacyToolsWithCacheHints(t *testing.T) {
	h := NewHandler(nil, nil)
	modern := wantModernResult(t, postWithHeaders(h, modernBody(t, 1, "tools/list", nil, modernMeta()), modernHeaders("tools/list")))
	if modern["ttlMs"] != float64(listCacheTTLMs) {
		t.Errorf("ttlMs = %v, want %d", modern["ttlMs"], listCacheTTLMs)
	}
	if modern["cacheScope"] != "public" {
		t.Errorf("cacheScope = %v, want public", modern["cacheScope"])
	}

	legacy := decodeModern(t, postMCPRaw(h, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if !reflect.DeepEqual(modern["tools"], legacy.Result["tools"]) {
		t.Errorf("2026-07-28 tools differ from 2025-11-25 tools:\n%v\n%v", modern["tools"], legacy.Result["tools"])
	}
	for _, key := range []string{"resultType", "_meta", "ttlMs", "cacheScope"} {
		if _, ok := legacy.Result[key]; ok {
			t.Errorf("2025-11-25 tools/list result carries %s", key)
		}
	}
}

func TestModernToolsCall(t *testing.T) {
	h, shutdown := newTestHandler(t)
	defer shutdown()

	headers := modernHeaders("tools/call")
	headers["Mcp-Name"] = "list_containers"
	body := modernBody(t, 7, "tools/call", map[string]any{"name": "list_containers", "arguments": map[string]any{}}, modernMeta())
	result := wantModernResult(t, postWithHeaders(h, body, headers))
	if result["isError"] != false {
		t.Fatalf("isError = %v, want false: %v", result["isError"], result)
	}
	content, _ := result["content"].([]any)
	block, _ := content[0].(map[string]any)
	if text, _ := block["text"].(string); !strings.Contains(text, `"id":"abc123"`) {
		t.Errorf("tool text = %q, want the stub container", text)
	}
	if _, ok := result["ttlMs"]; ok {
		t.Error("tools/call result carries ttlMs; only list results are cacheable")
	}
}

func TestModernToolsCallToolErrorKeepsEnvelope(t *testing.T) {
	h := NewHandler(nil, nil)
	headers := modernHeaders("tools/call")
	headers["Mcp-Name"] = "host_metrics"
	body := modernBody(t, 8, "tools/call", map[string]any{"name": "host_metrics"}, modernMeta())
	result := wantModernResult(t, postWithHeaders(h, body, headers))
	if result["isError"] != true {
		t.Errorf("isError = %v, want true with a nil collector", result["isError"])
	}
}

func TestModernToolsCallProtocolErrorsStayInBand(t *testing.T) {
	h := NewHandler(nil, nil)
	tests := []struct {
		name    string
		params  map[string]any
		mcpName string
	}{
		{name: "unknown tool", params: map[string]any{"name": "nope"}, mcpName: "nope"},
		{name: "missing name", params: map[string]any{}},
		{name: "non-string name", params: map[string]any{"name": 5}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := modernHeaders("tools/call")
			if tt.mcpName != "" {
				headers["Mcp-Name"] = tt.mcpName
			}
			rr := postWithHeaders(h, modernBody(t, 9, "tools/call", tt.params, modernMeta()), headers)
			wantModernError(t, rr, http.StatusOK, errInvalidParams)
		})
	}
}

func TestModernRemovedAndUnknownMethodsAre404(t *testing.T) {
	h := NewHandler(nil, nil)
	for _, method := range []string{"ping", "initialize", "logging/setLevel", "notifications/initialized", "resources/list"} {
		t.Run(method, func(t *testing.T) {
			rr := postWithHeaders(h, modernBody(t, 10, method, nil, modernMeta()), modernHeaders(method))
			resp := wantModernError(t, rr, http.StatusNotFound, errMethodNotFound)
			if resp.Error.Message != "method not found: "+method {
				t.Errorf("message = %q", resp.Error.Message)
			}
			if string(resp.ID) != "10" {
				t.Errorf("id = %s, want 10", resp.ID)
			}
		})
	}
}

func TestModernEnvelopeValidation(t *testing.T) {
	h := NewHandler(nil, nil)
	headers := modernHeaders("tools/list")
	withMeta := func(change func(map[string]any)) map[string]any {
		meta := modernMeta()
		change(meta)
		return meta
	}
	tests := []struct {
		name string
		body string
	}{
		{name: "header but no params", body: `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`},
		{name: "header but no _meta", body: `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`},
		{name: "header and _meta without version", body: modernBody(t, 1, "tools/list", nil, withMeta(func(m map[string]any) { delete(m, metaProtocolVersion) }))},
		{name: "header and non-object _meta", body: `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":5}}`},
		{name: "numeric version", body: modernBody(t, 1, "tools/list", nil, withMeta(func(m map[string]any) { m[metaProtocolVersion] = 20260728 }))},
		{name: "null version", body: modernBody(t, 1, "tools/list", nil, withMeta(func(m map[string]any) { m[metaProtocolVersion] = nil }))},
		{name: "missing capabilities", body: modernBody(t, 1, "tools/list", nil, withMeta(func(m map[string]any) { delete(m, metaClientCapabilities) }))},
		{name: "array capabilities", body: modernBody(t, 1, "tools/list", nil, withMeta(func(m map[string]any) { m[metaClientCapabilities] = []any{} }))},
		{name: "null capabilities", body: modernBody(t, 1, "tools/list", nil, withMeta(func(m map[string]any) { m[metaClientCapabilities] = nil }))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantModernError(t, postWithHeaders(h, tt.body, headers), http.StatusBadRequest, errInvalidParams)
		})
	}

	t.Run("claim without headers is still validated", func(t *testing.T) {
		body := modernBody(t, 1, "tools/list", nil, withMeta(func(m map[string]any) { m[metaProtocolVersion] = true }))
		wantModernError(t, postMCPRaw(h, body), http.StatusBadRequest, errInvalidParams)
	})
}

func TestModernHeaderValidation(t *testing.T) {
	h := NewHandler(nil, nil)
	call := func(name string) string {
		return modernBody(t, 1, "tools/call", map[string]any{"name": name}, modernMeta())
	}
	tests := []struct {
		name    string
		body    string
		headers map[string]string
	}{
		{
			name:    "missing MCP-Protocol-Version",
			body:    modernBody(t, 1, "tools/list", nil, modernMeta()),
			headers: map[string]string{"Mcp-Method": "tools/list"},
		},
		{
			name:    "MCP-Protocol-Version disagrees with _meta",
			body:    modernBody(t, 1, "tools/list", nil, modernMeta()),
			headers: map[string]string{"MCP-Protocol-Version": "2025-11-25", "Mcp-Method": "tools/list"},
		},
		{
			name:    "missing Mcp-Method",
			body:    modernBody(t, 1, "tools/list", nil, modernMeta()),
			headers: map[string]string{"MCP-Protocol-Version": modernProtocolVersion},
		},
		{
			name:    "Mcp-Method disagrees with body",
			body:    modernBody(t, 1, "tools/list", nil, modernMeta()),
			headers: modernHeaders("tools/call"),
		},
		{
			name:    "Mcp-Method differs only in case",
			body:    modernBody(t, 1, "tools/list", nil, modernMeta()),
			headers: modernHeaders("Tools/List"),
		},
		{name: "missing Mcp-Name", body: call("list_containers"), headers: modernHeaders("tools/call")},
		{name: "Mcp-Name disagrees with body", body: call("list_containers"), headers: withName(modernHeaders("tools/call"), "host_metrics")},
		{name: "Mcp-Name bad Base64", body: call("list_containers"), headers: withName(modernHeaders("tools/call"), "=?base64?not base64?=")},
		{name: "Mcp-Name raw non-ASCII", body: call("héllo"), headers: withName(modernHeaders("tools/call"), "héllo")},
		{
			name:    "resources/read without Mcp-Name",
			body:    modernBody(t, 1, "resources/read", map[string]any{"uri": "file:///x"}, modernMeta()),
			headers: modernHeaders("resources/read"),
		},
		{
			name:    "prompts/get without Mcp-Name",
			body:    modernBody(t, 1, "prompts/get", map[string]any{"name": "p"}, modernMeta()),
			headers: modernHeaders("prompts/get"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantModernError(t, postWithHeaders(h, tt.body, tt.headers), http.StatusBadRequest, errHeaderMismatch)
		})
	}
}

func withName(headers map[string]string, name string) map[string]string {
	headers["Mcp-Name"] = name
	return headers
}

func TestModernNameHeaderAccepted(t *testing.T) {
	h := NewHandler(nil, nil)
	tests := []struct {
		name     string
		method   string
		params   map[string]any
		mcpName  string
		status   int
		wantCode int
	}{
		{name: "plain tool name", method: "tools/call", params: map[string]any{"name": "nope"}, mcpName: "nope", status: http.StatusOK, wantCode: errInvalidParams},
		{name: "Base64 non-ASCII name", method: "tools/call", params: map[string]any{"name": "héllo"}, mcpName: "=?base64?aMOpbGxv?=", status: http.StatusOK, wantCode: errInvalidParams},
		{name: "Base64 empty name", method: "tools/call", params: map[string]any{"name": ""}, mcpName: "=?base64??=", status: http.StatusOK, wantCode: errInvalidParams},
		{name: "resource uri", method: "resources/read", params: map[string]any{"uri": "file:///x"}, mcpName: "file:///x", status: http.StatusNotFound, wantCode: errMethodNotFound},
		{name: "unrelated method ignores Mcp-Name", method: "server/discover", params: map[string]any{"name": "x"}, mcpName: "", status: http.StatusOK},
		{name: "empty-string key is not a name source", method: "tools/list", params: map[string]any{"": "x"}, mcpName: "", status: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := modernHeaders(tt.method)
			if tt.mcpName != "" {
				headers["Mcp-Name"] = tt.mcpName
			}
			rr := postWithHeaders(h, modernBody(t, 1, tt.method, tt.params, modernMeta()), headers)
			if tt.wantCode == 0 {
				wantModernResult(t, rr)
				return
			}
			wantModernError(t, rr, tt.status, tt.wantCode)
		})
	}
}

func TestModernUnsupportedVersion(t *testing.T) {
	h := NewHandler(nil, nil)
	for _, version := range []string{"2025-11-25", "2099-01-01", ""} {
		t.Run(version, func(t *testing.T) {
			meta := modernMeta()
			meta[metaProtocolVersion] = version
			headers := map[string]string{"MCP-Protocol-Version": version, "Mcp-Method": "tools/list"}
			resp := wantModernError(t, postWithHeaders(h, modernBody(t, "v", "tools/list", nil, meta), headers), http.StatusBadRequest, errUnsupportedProtocolVersion)
			var data struct {
				Supported []string `json:"supported"`
				Requested *string  `json:"requested"`
			}
			if err := json.Unmarshal(resp.Error.Data, &data); err != nil {
				t.Fatalf("decode data %s: %v", resp.Error.Data, err)
			}
			if !reflect.DeepEqual(data.Supported, []string{modernProtocolVersion}) {
				t.Errorf("supported = %v, want [%s]", data.Supported, modernProtocolVersion)
			}
			if data.Requested == nil || *data.Requested != version {
				t.Errorf("requested = %v, want %q", data.Requested, version)
			}
		})
	}
}

// TestModernValidationOrder pins which error wins when a request fails more
// than one check, following the order the reference SDK uses.
func TestModernValidationOrder(t *testing.T) {
	h := NewHandler(nil, nil)
	unsupported := modernMeta()
	unsupported[metaProtocolVersion] = "2099-01-01"
	noCaps := modernMeta()
	delete(noCaps, metaClientCapabilities)
	tests := []struct {
		name    string
		body    string
		headers map[string]string
		status  int
		code    int
	}{
		{
			name:    "envelope beats header mismatch",
			body:    modernBody(t, 1, "tools/list", nil, noCaps),
			headers: map[string]string{"MCP-Protocol-Version": "2025-11-25", "Mcp-Method": "ping"},
			status:  http.StatusBadRequest, code: errInvalidParams,
		},
		{
			name:    "version header mismatch beats unsupported version",
			body:    modernBody(t, 1, "tools/list", nil, unsupported),
			headers: modernHeaders("tools/list"),
			status:  http.StatusBadRequest, code: errHeaderMismatch,
		},
		{
			name:    "method header mismatch beats unsupported version",
			body:    modernBody(t, 1, "tools/list", nil, unsupported),
			headers: map[string]string{"MCP-Protocol-Version": "2099-01-01", "Mcp-Method": "ping"},
			status:  http.StatusBadRequest, code: errHeaderMismatch,
		},
		{
			name:    "unsupported version beats missing headers",
			body:    modernBody(t, 1, "tools/list", nil, unsupported),
			headers: nil,
			status:  http.StatusBadRequest, code: errUnsupportedProtocolVersion,
		},
		{
			name:    "missing headers beat method not found",
			body:    modernBody(t, 1, "ping", nil, modernMeta()),
			headers: map[string]string{"MCP-Protocol-Version": modernProtocolVersion},
			status:  http.StatusBadRequest, code: errHeaderMismatch,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantModernError(t, postWithHeaders(h, tt.body, tt.headers), tt.status, tt.code)
		})
	}
}

// TestLegacyRequestsIgnoreModernHeaders checks that every request without an
// envelope claim gets byte-for-byte the response it got before 2026-07-28
// support, whatever headers ride along with it.
func TestLegacyRequestsIgnoreModernHeaders(t *testing.T) {
	h := NewHandler(nil, nil)
	bodies := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2026-07-28"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"host_metrics","_meta":{"progressToken":1}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"server/discover","params":{"_meta":{}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"ping","params":{"_meta":null}}`,
		`{"jsonrpc":"2.0","id":7,"method":"ping","params":{"_meta":"x"}}`,
		`{"jsonrpc":"2.0","id":8,"method":"logging/setLevel","params":{"level":"debug"}}`,
	}
	headerSets := []map[string]string{
		{"MCP-Protocol-Version": "2025-11-25"},
		{"MCP-Protocol-Version": "2025-06-18", "Mcp-Method": "something/else", "Mcp-Name": "=?base64?!!?="},
		{"MCP-Protocol-Version": "garbage"},
		{"MCP-Protocol-Version": "2099-01-01"},
		{"Mcp-Session-Id": "abc"},
	}
	for _, body := range bodies {
		want := postMCPRaw(h, body)
		for _, headers := range headerSets {
			got := postWithHeaders(h, body, headers)
			if got.Code != want.Code || !bytes.Equal(got.Body.Bytes(), want.Body.Bytes()) {
				t.Errorf("headers %v changed the response to %s:\n got %d %s\nwant %d %s", headers, body, got.Code, got.Body.String(), want.Code, want.Body.String())
			}
		}
	}
}

func TestLegacyResponsesCarryNoModernFields(t *testing.T) {
	h := NewHandler(nil, nil)
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"host_metrics"}}`,
	} {
		resp := decodeModern(t, postMCPRaw(h, body))
		for _, key := range []string{"resultType", "_meta"} {
			if _, ok := resp.Result[key]; ok {
				t.Errorf("%s: 2025-11-25 result carries %s", body, key)
			}
		}
	}
}

// TestModernHeaderNamesAreCaseInsensitive sends lower-case header names over
// a real connection; net/http canonicalizes them before the handler runs.
func TestModernHeaderNamesAreCaseInsensitive(t *testing.T) {
	srv := httptest.NewServer(NewHandler(nil, nil))
	defer srv.Close()

	body := modernBody(t, 1, "server/discover", nil, modernMeta())
	req, err := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header["content-type"] = []string{"application/json"}
	req.Header["mcp-protocol-version"] = []string{modernProtocolVersion}
	req.Header["mcp-method"] = []string{"server/discover"}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"resultType":"complete"`) {
		t.Errorf("status %d body %s, want a discover result", resp.StatusCode, b)
	}
}

func TestModernNotificationsAreAccepted(t *testing.T) {
	h := NewHandler(nil, nil)
	body := `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":7}}}`
	rr := postWithHeaders(h, body, map[string]string{"MCP-Protocol-Version": modernProtocolVersion})
	if rr.Code != http.StatusAccepted || rr.Body.Len() != 0 {
		t.Errorf("status %d body %q, want 202 with no body", rr.Code, rr.Body.String())
	}
}

func TestDecodeHeaderValue(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{in: "list_containers", want: "list_containers", ok: true},
		{in: "", want: "", ok: true},
		{in: "a b\tc~", want: "a b\tc~", ok: true},
		{in: "=?base64?SGVsbG8sIOS4lueVjA==?=", want: "Hello, 世界", ok: true},
		{in: "=?base64?PT9iYXNlNjQ/bGl0ZXJhbD89?=", want: "=?base64?literal?=", ok: true},
		{in: "=?base64??=", want: "", ok: true},
		{in: "=?base64?SGk?=", ok: false},                // unpadded
		{in: "=?base64?SGl=?=", ok: false},               // non-zero trailing bits
		{in: "=?base64?S G k=?=", ok: false},             // not Base64
		{in: "=?base64?/w==?=", ok: false},               // decodes to invalid UTF-8
		{in: "=?base64?=", want: "=?base64?=", ok: true}, // prefix and suffix overlap: plain
		{in: "=?BASE64?SGk=?=", want: "=?BASE64?SGk=?=", ok: true},
		{in: "x\x1f", ok: false},
		{in: "x\x7f", ok: false},
		{in: "x\x80", ok: false},
		{in: " ", want: " ", ok: true},
	}
	for _, tt := range tests {
		got, ok := decodeHeaderValue(tt.in)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("decodeHeaderValue(%q) = (%q, %v), want (%q, %v)", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestHeaderValueUsesFirstValue(t *testing.T) {
	header := http.Header{headerMethod: {"tools/list", "tools/call"}, headerName: {}}
	if got, ok := headerValue(header, headerMethod); !ok || got != "tools/list" {
		t.Errorf("headerValue = (%q, %v), want first value", got, ok)
	}
	if _, ok := headerValue(header, headerName); ok {
		t.Error("an empty value list counts as a sent header")
	}
	if got, ok := headerValue(http.Header{headerName: {""}}, headerName); !ok || got != "" {
		t.Errorf("an empty header value = (%q, %v), want sent and empty", got, ok)
	}
}

// TestModernToolsCallUsesExactNameKey pins that the 2026-07-28 path runs the
// tool named by the exact params.name key, the one Mcp-Name is checked
// against, while 2025-11-25 keeps encoding/json's case-insensitive match.
func TestModernToolsCallUsesExactNameKey(t *testing.T) {
	h := NewHandler(nil, nil)
	meta := `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}`

	ambiguous := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"x","NAME":"host_metrics",` + meta + `}}`
	resp := wantModernError(t, postWithHeaders(h, ambiguous, withName(modernHeaders("tools/call"), "x")), http.StatusOK, errInvalidParams)
	if resp.Error.Message != "unknown tool: x" {
		t.Errorf("message = %q, want the exact-key tool name", resp.Error.Message)
	}

	caseOnly := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"Name":"host_metrics",` + meta + `}}`
	resp = wantModernError(t, postWithHeaders(h, caseOnly, modernHeaders("tools/call")), http.StatusOK, errInvalidParams)
	if resp.Error.Message != "invalid tools/call params" {
		t.Errorf("message = %q", resp.Error.Message)
	}

	legacy := decodeModern(t, postMCPRaw(h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"Name":"host_metrics"}}`))
	if legacy.Error != nil || legacy.Result["isError"] != true {
		t.Errorf("2025-11-25 case-insensitive name no longer reaches host_metrics: %+v", legacy)
	}
}
