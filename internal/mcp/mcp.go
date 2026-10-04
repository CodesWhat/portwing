// Package mcp implements a read-only MCP (Model Context Protocol) server using
// the Streamable HTTP transport. It exposes Docker container state to AI
// assistants over a single POST endpoint and serves two protocol revisions:
//
//   - 2025-11-25, negotiated with the initialize handshake. Session IDs are
//     not assigned; that revision makes session management optional ("MAY").
//   - 2026-07-28, which is stateless: every request names its revision and
//     client capabilities in params._meta. negotiate decides which revision
//     serves a request, and is the only place that decision is made.
//
// Transport: stateless single-request mode. Requests return one JSON-RPC
// response object; notifications return 202 Accepted with no body.
//
// The GET method returns 405 Method Not Allowed to signal that the server
// does not offer an SSE stream at this endpoint.
package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/codeswhat/portwing/internal/docker"
	"github.com/codeswhat/portwing/internal/metrics"
	"github.com/codeswhat/portwing/internal/protocol"
)

// protocolVersion is the handshake-based MCP revision this server answers
// initialize with.
const protocolVersion = "2025-11-25"

// modernProtocolVersion is the stateless MCP revision this server serves to
// requests that name it in params._meta.
const modernProtocolVersion = "2026-07-28"

// JSON-RPC 2.0 error codes.
const (
	errParseError     = -32700
	errInvalidRequest = -32600
	errMethodNotFound = -32601
	errInvalidParams  = -32602
	errInternalError  = -32603
)

// MCP error codes defined by the 2026-07-28 revision. Neither is ever sent
// to a 2025-11-25 request.
const (
	errHeaderMismatch             = -32020
	errUnsupportedProtocolVersion = -32022
)

// Reserved _meta keys from the 2026-07-28 revision.
const (
	metaProtocolVersion    = "io.modelcontextprotocol/protocolVersion"
	metaClientCapabilities = "io.modelcontextprotocol/clientCapabilities"
	metaServerInfo         = "io.modelcontextprotocol/serverInfo"
)

// Standard request headers from the 2026-07-28 Streamable HTTP transport, in
// the canonical form net/http stores them under.
const (
	headerProtocolVersion = "Mcp-Protocol-Version"
	headerMethod          = "Mcp-Method"
	headerName            = "Mcp-Name"
)

// The Base64 sentinel a client wraps a header value in when it can't be sent
// as plain ASCII.
const (
	base64Prefix = "=?base64?"
	base64Suffix = "?="
)

// listCacheTTLMs is the ttlMs hint on tools/list and server/discover results.
// Both are compile-time constants, so they can only change when the binary is
// replaced, which restarts the process. An hour bounds how long a client keeps
// a stale list after an upgrade, and a client that calls a removed tool gets
// an unknown-tool error, which the spec names as a reason to re-fetch early.
const listCacheTTLMs = 3_600_000

// container_logs output bounds. Docker's tail parameter caps how many lines
// the daemon sends but not how long a line is, so the tool keeps the newest
// lines that fit and reports truncated: true when it dropped or cut any.
const (
	maxLogLines = 500       // also the largest tail a caller may ask for
	maxLogBytes = 256 << 10 // every returned line together, prefixes included

	// maxLogLineBytes is the longest line that fits the budget on its own
	// once its 8-byte "stdout: " or "stderr: " prefix is added. A longer line
	// keeps its first maxLogLineBytes bytes. It is maxLogBytes minus 8,
	// spelled as a literal so mutation testing has no package-level
	// arithmetic it can't cover; TestLogBoundConstants pins the relationship.
	maxLogLineBytes = 262_136
)

// rpcRequest is the incoming JSON-RPC 2.0 envelope.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcResponse is the outgoing JSON-RPC 2.0 envelope for a result.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// rejection is a 2026-07-28 request refused before dispatch. The revision
// requires an HTTP status other than 200 for these errors.
type rejection struct {
	status int
	rpcError
}

// unsupportedVersionData is the data member of UnsupportedProtocolVersionError.
type unsupportedVersionData struct {
	Supported []string `json:"supported"`
	Requested string   `json:"requested"`
}

// Handler is the MCP HTTP handler. It holds references to the Docker client
// and the metrics collector used by the tool implementations.
type Handler struct {
	docker    *docker.Client
	collector *metrics.Collector
}

// NewHandler creates a new MCP Handler.
func NewHandler(dockerClient *docker.Client, collector *metrics.Collector) *Handler {
	return &Handler{
		docker:    dockerClient,
		collector: collector,
	}
}

// ServeHTTP dispatches POST requests as JSON-RPC calls.
// GET returns 405; all other methods return 405 as well.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		// Signal: no SSE stream offered at this endpoint.
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, nil, errInternalError, "reading request body")
		return
	}

	if !json.Valid(body) {
		writeError(w, nil, errParseError, "parse error")
		return
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		writeError(w, nil, errInvalidRequest, "request must be an object")
		return
	}

	rawMethod, hasMethod := fields["method"]
	_, hasResult := fields["result"]
	_, hasError := fields["error"]
	if !hasMethod && (hasResult || hasError) {
		if validRPCResponse(fields) {
			w.WriteHeader(http.StatusAccepted)
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}
		return
	}

	var req rpcRequest
	if rawID, ok := fields["id"]; ok {
		req.ID = rawID
		if !validRequestID(rawID) {
			writeError(w, nil, errInvalidRequest, "id must be a string or number")
			return
		}
	}

	if rawVersion, ok := fields["jsonrpc"]; ok {
		_ = json.Unmarshal(rawVersion, &req.JSONRPC)
	}
	if req.JSONRPC != "2.0" {
		writeError(w, req.ID, errInvalidRequest, "jsonrpc must be \"2.0\"")
		return
	}

	if hasResult || hasError {
		writeError(w, req.ID, errInvalidRequest, "request must not contain result or error")
		return
	}
	if !hasMethod {
		writeError(w, req.ID, errInvalidRequest, "method must be a string")
		return
	}
	if !decodeMethod(rawMethod, &req.Method) {
		writeError(w, req.ID, errInvalidRequest, "method must be a string")
		return
	}
	// params is assigned from a block-local map so that a request without
	// params doesn't pay for a heap-allocated map header.
	var params map[string]json.RawMessage
	if rawParams, ok := fields["params"]; ok {
		var decoded map[string]json.RawMessage
		if err := json.Unmarshal(rawParams, &decoded); err != nil || decoded == nil {
			if req.ID == nil {
				w.WriteHeader(http.StatusBadRequest)
			} else {
				writeError(w, req.ID, errInvalidParams, "params must be an object")
			}
			return
		}
		params = decoded
		req.Params = rawParams
	}

	if req.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	ctx := r.Context()

	modern, rejected := negotiate(r.Header, req.Method, params)
	if rejected != nil {
		writeErrorStatus(w, rejected.status, req.ID, &rejected.rpcError)
		return
	}
	if modern {
		h.serveModern(ctx, w, req, params)
		return
	}

	switch req.Method {
	case "initialize":
		h.handleInitialize(w, req)
	case "notifications/initialized":
		writeError(w, req.ID, errInvalidRequest, "notifications must not include an id")
	case "ping":
		writeResult(w, req.ID, map[string]any{})
	case "tools/list":
		writeResult(w, req.ID, h.toolsList())
	case "tools/call":
		h.handleToolsCall(ctx, w, req)
	default:
		writeError(w, req.ID, errMethodNotFound, fmt.Sprintf("method not found: %s", req.Method))
	}
}

// negotiate picks the protocol revision for a request that carries an id.
// It is the only place that choice is made.
//
// A request whose params._meta carries io.modelcontextprotocol/protocolVersion
// is a 2026-07-28 request, and so is one whose MCP-Protocol-Version header
// names 2026-07-28. Everything else, including a 2025-11-25 client that sends
// MCP-Protocol-Version: 2025-11-25 after initialize, takes the 2025-11-25 path
// exactly as before: no other header is consulted and no new error can occur.
//
// A 2026-07-28 request is validated in the order the revision's reference SDK
// uses: envelope fields (-32602), header values that contradict the body
// (-32020), an unsupported version (-32022), then missing headers (-32020).
// Any failure comes back as a rejection carrying HTTP 400.
func negotiate(header http.Header, method string, params map[string]json.RawMessage) (bool, *rejection) {
	meta, claimed := envelopeClaim(params)
	versionHeader, hasVersionHeader := headerValue(header, headerProtocolVersion)
	if !claimed {
		if versionHeader != modernProtocolVersion {
			return false, nil
		}
		return true, invalidEnvelope("missing _meta key " + metaProtocolVersion)
	}

	var version string
	if !decodeMethod(meta[metaProtocolVersion], &version) {
		return true, invalidEnvelope(metaProtocolVersion + " must be a string")
	}
	if firstNonSpace(meta[metaClientCapabilities]) != '{' {
		return true, invalidEnvelope(metaClientCapabilities + " must be an object")
	}
	if hasVersionHeader && versionHeader != version {
		return true, headerMismatch("MCP-Protocol-Version header does not match " + metaProtocolVersion)
	}
	methodHeader, hasMethodHeader := headerValue(header, headerMethod)
	if hasMethodHeader && methodHeader != method {
		return true, headerMismatch("Mcp-Method header does not match the request method")
	}
	if version != modernProtocolVersion {
		return true, &rejection{status: http.StatusBadRequest, rpcError: rpcError{
			Code:    errUnsupportedProtocolVersion,
			Message: "unsupported protocol version",
			Data:    unsupportedVersionData{Supported: []string{modernProtocolVersion}, Requested: version},
		}}
	}
	if !hasVersionHeader {
		return true, headerMismatch("missing MCP-Protocol-Version header")
	}
	if !hasMethodHeader {
		return true, headerMismatch("missing Mcp-Method header")
	}
	return true, checkNameHeader(header, method, params)
}

// envelopeClaim returns params._meta and whether it carries the reserved
// protocol version key. Only the key's presence counts as a claim; its value
// is validated afterwards, so a malformed claim is rejected rather than
// served as 2025-11-25. _meta is only decoded when it is present.
func envelopeClaim(params map[string]json.RawMessage) (map[string]json.RawMessage, bool) {
	raw, ok := params["_meta"]
	if !ok {
		return nil, false
	}
	var meta map[string]json.RawMessage
	if json.Unmarshal(raw, &meta) != nil {
		return nil, false
	}
	_, claimed := meta[metaProtocolVersion]
	return meta, claimed
}

// checkNameHeader validates the Mcp-Name header for the methods whose body
// field it mirrors. A body without that field is left to dispatch, which
// rejects it in-band.
func checkNameHeader(header http.Header, method string, params map[string]json.RawMessage) *rejection {
	field := nameSource(method)
	if field == "" {
		return nil
	}
	var bodyValue string
	if !decodeMethod(params[field], &bodyValue) {
		return nil
	}
	nameHeader, ok := headerValue(header, headerName)
	if !ok {
		return headerMismatch("missing Mcp-Name header")
	}
	decoded, ok := decodeHeaderValue(nameHeader)
	if !ok {
		return headerMismatch("Mcp-Name header is not a valid header value")
	}
	if decoded != bodyValue {
		return headerMismatch("Mcp-Name header does not match the request body")
	}
	return nil
}

// nameSource returns the params field the Mcp-Name header mirrors for method,
// or "" when the header doesn't apply. The table is the revision's, not just
// the methods this server implements, so a resources/read or prompts/get
// request is validated before it gets method-not-found.
func nameSource(method string) string {
	switch method {
	case "tools/call", "prompts/get":
		return "name"
	case "resources/read":
		return "uri"
	default:
		return ""
	}
}

// decodeHeaderValue undoes the transport's value encoding. A value wrapped in
// =?base64?...?= is canonical padded Base64 of UTF-8 text; any other value
// must already be visible ASCII, space or tab.
func decodeHeaderValue(value string) (string, bool) {
	if len(value) >= len(base64Prefix)+len(base64Suffix) &&
		strings.HasPrefix(value, base64Prefix) && strings.HasSuffix(value, base64Suffix) {
		decoded, err := base64.StdEncoding.Strict().DecodeString(value[len(base64Prefix) : len(value)-len(base64Suffix)])
		return string(decoded), err == nil && utf8.Valid(decoded)
	}
	return value, isPlainHeaderValue(value)
}

func isPlainHeaderValue(value string) bool {
	for i := 0; i < len(value); i++ {
		if c := value[i]; c != '\t' && (c < ' ' || c > '~') {
			return false
		}
	}
	return true
}

// headerValue returns the first value sent for a canonical header key, and
// whether the header was sent at all.
func headerValue(header http.Header, key string) (string, bool) {
	values := header[key]
	if len(values) == 0 {
		return "", false
	}
	return values[0], true
}

func invalidEnvelope(detail string) *rejection {
	return &rejection{status: http.StatusBadRequest, rpcError: rpcError{Code: errInvalidParams, Message: "invalid params: " + detail}}
}

func headerMismatch(detail string) *rejection {
	return &rejection{status: http.StatusBadRequest, rpcError: rpcError{Code: errHeaderMismatch, Message: "header mismatch: " + detail}}
}

// serveModern dispatches a 2026-07-28 request that passed negotiate. An
// unknown method, including ping and initialize which this revision removed,
// gets -32601 with HTTP 404 as the transport requires.
func (h *Handler) serveModern(ctx context.Context, w http.ResponseWriter, req rpcRequest, params map[string]json.RawMessage) {
	switch req.Method {
	case "server/discover":
		writeResult(w, req.ID, modernResult(map[string]any{
			"supportedVersions": []string{modernProtocolVersion},
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"ttlMs":      listCacheTTLMs,
			"cacheScope": "public",
		}))
	case "tools/list":
		result := h.toolsList()
		result["ttlMs"] = listCacheTTLMs
		result["cacheScope"] = "public"
		writeResult(w, req.ID, modernResult(result))
	case "tools/call":
		h.handleModernToolsCall(ctx, w, req.ID, params)
	default:
		writeErrorStatus(w, http.StatusNotFound, req.ID, &rpcError{
			Code:    errMethodNotFound,
			Message: fmt.Sprintf("method not found: %s", req.Method),
		})
	}
}

// modernResult adds the fields every 2026-07-28 result carries: resultType,
// which the revision requires, and the server identity it asks for in _meta.
func modernResult(result map[string]any) map[string]any {
	result["resultType"] = "complete"
	result["_meta"] = map[string]any{
		metaServerInfo: map[string]any{
			"name":    "portwing",
			"version": protocol.AgentVersion,
		},
	}
	return result
}

func validRPCResponse(fields map[string]json.RawMessage) bool {
	var version string
	if err := json.Unmarshal(fields["jsonrpc"], &version); err != nil || version != "2.0" {
		return false
	}
	if rawID, ok := fields["id"]; !ok || !validRequestID(rawID) {
		return false
	}

	rawResult, hasResult := fields["result"]
	rawError, hasError := fields["error"]
	if hasResult == hasError {
		return false
	}
	if hasResult {
		var result map[string]json.RawMessage
		if err := json.Unmarshal(rawResult, &result); err != nil || result == nil {
			return false
		}
	}
	if hasError && !validRPCError(rawError) {
		return false
	}
	return true
}

func validRPCError(raw json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return false
	}

	decoder := json.NewDecoder(bytes.NewReader(fields["code"]))
	decoder.UseNumber()
	var code json.Number
	if err := decoder.Decode(&code); err != nil {
		return false
	}
	if _, err := code.Int64(); err != nil {
		return false
	}

	var message any
	if err := json.Unmarshal(fields["message"], &message); err != nil {
		return false
	}
	_, ok := message.(string)
	return ok
}

// firstNonSpace returns the first byte of raw that is not JSON whitespace, or
// 0 when there is none. Callers hold raw values already validated by the
// parent Unmarshal, so the first byte alone identifies the JSON type.
func firstNonSpace(raw json.RawMessage) byte {
	for _, c := range raw {
		switch c {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return c
		}
	}
	return 0
}

func isJSONString(raw json.RawMessage) bool {
	return firstNonSpace(raw) == '"'
}

// decodeMethod decodes raw into dst when it is a JSON string. The quote check
// comes first because null unmarshals into a string without an error. Despite
// the name it suits any string field; negotiate uses it for _meta values too.
func decodeMethod(raw json.RawMessage, dst *string) bool {
	return isJSONString(raw) && json.Unmarshal(raw, dst) == nil
}

// validRequestID accepts JSON strings and numbers (including fractional and
// exponent forms) and rejects null, booleans, objects, arrays and empty input.
func validRequestID(raw json.RawMessage) bool {
	c := firstNonSpace(raw)
	return c == '"' || c == '-' || (c >= '0' && c <= '9')
}

// handleInitialize responds to the MCP initialization handshake.
func (h *Handler) handleInitialize(w http.ResponseWriter, req rpcRequest) {
	result := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    "portwing",
			"version": protocol.AgentVersion,
		},
	}
	writeResult(w, req.ID, result)
}

// toolsList returns the MCP tools/list result body. Both revisions get the
// same tools in the same order; the 2026-07-28 path adds its cache hints.
func (h *Handler) toolsList() map[string]any {
	return map[string]any{"tools": encodedTools()}
}

// encodedTools returns the tools array as JSON. The list is fixed at compile
// time, so it is encoded once per process rather than on every tools/list;
// the response bytes are the same either way.
var encodedTools = sync.OnceValue(marshalTools)

func marshalTools() json.RawMessage {
	// A tree of maps, slices, strings, ints and bools always marshals.
	encoded, _ := json.Marshal(toolDefinitions())
	return encoded
}

// toolDefinitions returns the tool definitions served by tools/list.
//
// Every tool is read-only. openWorldHint is false where a tool reports Docker
// or host metadata, and true for container_logs: the daemon is local, but the
// text it returns is written by the workload and often records outside
// traffic, so a client should treat it as untrusted input.
func toolDefinitions() []any {
	return []any{
		map[string]any{
			"name":        "list_containers",
			"description": "List all Docker containers (running and stopped) with id, names, image, state, status, and labels.",
			"inputSchema": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
			},
			"annotations": readOnlyAnnotations("List containers", false),
		},
		map[string]any{
			"name":        "inspect_container",
			"description": "Inspect a container: state, image, env var count (no values), mounts, network names, and restart policy.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Container ID or name.",
					},
				},
				"required": []string{"id"},
			},
			"annotations": readOnlyAnnotations("Inspect container", false),
		},
		map[string]any{
			"name": "container_logs",
			"description": "Return the last N lines (max 500) of stdout/stderr from a container. " +
				"Output is capped at 256 KiB of log text, keeping the newest lines; truncated is true when lines were dropped or one was cut to fit.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Container ID or name.",
					},
					"tail": map[string]any{
						"type":        "integer",
						"description": "Number of log lines to return (1–500, default 100).",
						"minimum":     1,
						"maximum":     maxLogLines,
					},
				},
				"required": []string{"id"},
			},
			"annotations": readOnlyAnnotations("Container logs", true),
		},
		map[string]any{
			"name":        "host_metrics",
			"description": "Return a snapshot of host-level resource metrics: CPU, memory, disk, network, and uptime.",
			"inputSchema": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
			},
			"annotations": readOnlyAnnotations("Host metrics", false),
		},
		map[string]any{
			"name":        "container_stats",
			"description": "Return a one-shot CPU/memory/network stats snapshot for a single container.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Container ID or name.",
					},
				},
				"required": []string{"id"},
			},
			"annotations": readOnlyAnnotations("Container stats", false),
		},
	}
}

// readOnlyAnnotations returns the standard ToolAnnotations for a tool that
// never modifies its environment. destructiveHint and idempotentHint are left
// out because the spec makes them meaningful only when readOnlyHint is false.
func readOnlyAnnotations(title string, openWorld bool) map[string]any {
	return map[string]any{
		"title":         title,
		"readOnlyHint":  true,
		"openWorldHint": openWorld,
	}
}

// handleToolsCall answers a 2025-11-25 tools/call. Protocol errors stay
// in-band with HTTP 200.
func (h *Handler) handleToolsCall(ctx context.Context, w http.ResponseWriter, req rpcRequest) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeError(w, req.ID, errInvalidParams, "invalid tools/call params")
		return
	}
	result, rpcErr := h.callTool(ctx, params.Name, params.Arguments)
	if rpcErr != nil {
		writeError(w, req.ID, rpcErr.Code, rpcErr.Message)
		return
	}
	writeResult(w, req.ID, result)
}

// handleModernToolsCall answers a 2026-07-28 tools/call. It reads the exact
// params.name key, the field the Mcp-Name header was checked against, so the
// tool that runs is the one the header named. Decoding into a struct instead
// would match keys case-insensitively and let a "NAME" key pick the tool.
func (h *Handler) handleModernToolsCall(ctx context.Context, w http.ResponseWriter, id json.RawMessage, params map[string]json.RawMessage) {
	var name string
	if !decodeMethod(params["name"], &name) {
		writeError(w, id, errInvalidParams, "invalid tools/call params")
		return
	}
	result, rpcErr := h.callTool(ctx, name, params["arguments"])
	if rpcErr != nil {
		writeError(w, id, rpcErr.Code, rpcErr.Message)
		return
	}
	writeResult(w, id, modernResult(result))
}

// callTool runs the named tool. An unknown tool is a protocol error; a tool
// that fails returns a result with isError set.
func (h *Handler) callTool(ctx context.Context, name string, args json.RawMessage) (map[string]any, *rpcError) {
	switch name {
	case "list_containers":
		return h.toolListContainers(ctx), nil
	case "inspect_container":
		return h.toolInspectContainer(ctx, args), nil
	case "container_logs":
		return h.toolContainerLogs(ctx, args), nil
	case "host_metrics":
		return h.toolHostMetrics(), nil
	case "container_stats":
		return h.toolContainerStats(ctx, args), nil
	default:
		return nil, &rpcError{Code: errInvalidParams, Message: fmt.Sprintf("unknown tool: %s", name)}
	}
}

// toolListContainers lists all containers.
func (h *Handler) toolListContainers(ctx context.Context) map[string]any {
	if h.docker == nil {
		return toolError("docker client not available")
	}
	containers, err := h.docker.ListContainers(ctx, true)
	if err != nil {
		return toolError(fmt.Sprintf("list containers: %v", err))
	}

	type item struct {
		ID     string            `json:"id"`
		Names  []string          `json:"names"`
		Image  string            `json:"image"`
		State  string            `json:"state"`
		Status string            `json:"status"`
		Labels map[string]string `json:"labels,omitempty"`
	}

	out := make([]item, 0, len(containers))
	for _, c := range containers {
		out = append(out, item{
			ID:     c.ID,
			Names:  c.Names,
			Image:  c.Image,
			State:  c.State,
			Status: c.Status,
			Labels: c.Labels,
		})
	}

	return toolResult(out)
}

// toolInspectContainer inspects a single container. Env values are never
// returned — only the count is exposed to prevent credential leakage.
func (h *Handler) toolInspectContainer(ctx context.Context, args json.RawMessage) map[string]any {
	if h.docker == nil {
		return toolError("docker client not available")
	}
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.ID == "" {
		return toolError("id is required")
	}

	info, err := h.docker.InspectContainer(ctx, p.ID)
	if err != nil {
		return toolError(fmt.Sprintf("inspect container: %v", err))
	}

	type mountOut struct {
		Source      string `json:"source"`
		Destination string `json:"destination"`
		ReadOnly    bool   `json:"readOnly"`
	}

	mounts := make([]mountOut, 0, len(info.Mounts))
	for _, m := range info.Mounts {
		mounts = append(mounts, mountOut{
			Source:      m.Source,
			Destination: m.Destination,
			ReadOnly:    !m.RW,
		})
	}

	var networks []string
	if info.NetworkSettings != nil {
		for name := range info.NetworkSettings.Networks {
			networks = append(networks, name)
		}
	}

	restartPolicy := ""
	if info.HostConfig != nil {
		restartPolicy = info.HostConfig.RestartPolicy.Name
	}

	out := map[string]any{
		"id":            info.ID,
		"name":          info.Name,
		"state":         info.State,
		"image":         info.Config.Image,
		"envCount":      len(info.Config.Env),
		"mounts":        mounts,
		"networks":      networks,
		"restartPolicy": restartPolicy,
	}

	return toolResult(out)
}

// toolContainerLogs returns demuxed log lines (stdout/stderr) for a container.
// Tail is capped at maxLogLines and the output at the other container_logs
// bounds; truncated reports whether anything was dropped or cut.
func (h *Handler) toolContainerLogs(ctx context.Context, args json.RawMessage) map[string]any {
	if h.docker == nil {
		return toolError("docker client not available")
	}
	var p struct {
		ID   string `json:"id"`
		Tail int    `json:"tail"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.ID == "" {
		return toolError("id is required")
	}

	tail := p.Tail
	if tail <= 0 {
		tail = 100
	}
	if tail > maxLogLines {
		tail = maxLogLines
	}

	tailStr := fmt.Sprintf("%d", tail)
	rc, err := h.docker.GetContainerLogs(ctx, p.ID, tailStr, "", "", false, false)
	if err != nil {
		return toolError(fmt.Sprintf("container logs: %v", err))
	}
	defer rc.Close()

	lines, truncated, err := decodeContainerLogTail(rc)
	if err != nil {
		return toolError(fmt.Sprintf("decode logs: %v", err))
	}

	return toolResult(map[string]any{
		"id":        p.ID,
		"lines":     lines,
		"truncated": truncated,
	})
}

// toolHostMetrics returns the collector's host metrics snapshot.
func (h *Handler) toolHostMetrics() map[string]any {
	if h.collector == nil {
		return toolError("metrics collector not available")
	}
	m, err := h.collector.Collect()
	if err != nil {
		return toolError(fmt.Sprintf("collect metrics: %v", err))
	}
	return toolResult(m)
}

// toolContainerStats returns a single-shot stats snapshot for a container.
func (h *Handler) toolContainerStats(ctx context.Context, args json.RawMessage) map[string]any {
	if h.docker == nil {
		return toolError("docker client not available")
	}
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.ID == "" {
		return toolError("id is required")
	}

	stats, err := h.docker.ContainerStats(ctx, p.ID)
	if err != nil {
		return toolError(fmt.Sprintf("container stats: %v", err))
	}

	type netIface struct {
		RxBytes uint64 `json:"rxBytes"`
		TxBytes uint64 `json:"txBytes"`
	}

	networks := make(map[string]netIface, len(stats.Networks))
	for name, iface := range stats.Networks {
		networks[name] = netIface{RxBytes: iface.RxBytes, TxBytes: iface.TxBytes}
	}

	out := map[string]any{
		"id":            p.ID,
		"cpuTotalUsage": stats.CPUStats.CPUUsage.TotalUsage,
		"memUsage":      stats.MemoryStats.Usage,
		"memLimit":      stats.MemoryStats.Limit,
		"networks":      networks,
	}
	return toolResult(out)
}

// decodeContainerLogLines decodes a container log stream into lines prefixed
// with their stream, within the container_logs bounds.
func decodeContainerLogLines(r io.Reader) ([]string, error) {
	lines, _, err := decodeContainerLogTail(r)
	return lines, err
}

// decodeContainerLogTail decodes a container log stream into lines prefixed
// with their stream and keeps the newest ones inside the container_logs
// bounds. truncated reports whether any line was dropped or cut.
func decodeContainerLogTail(r io.Reader) (lines []string, truncated bool, err error) {
	t := new(logTail)
	err = docker.DecodeContainerLogStream(r, t.write)
	if t.pending.Len() > 0 {
		t.flush()
	}
	return t.lines(), t.truncated, err
}

// logTail accumulates decoded log lines in a ring that holds the newest
// maxLogLines of them, dropping the oldest while their total size exceeds
// maxLogBytes. Memory stays bounded however much the daemon sends.
type logTail struct {
	ring      [maxLogLines]string
	start     int // ring index of the oldest kept line
	count     int // kept lines
	size      int // bytes in the kept lines
	truncated bool

	// pending is the line being assembled. It holds at most one byte more
	// than maxLogLineBytes, which is enough for flush to see it is too long.
	pending       strings.Builder
	pendingStream docker.ContainerLogStream
}

// write receives one decoded payload. A partial line is completed by the
// next payload from the same stream; a payload from the other stream ends it.
func (t *logTail) write(stream docker.ContainerLogStream, payload []byte) error {
	if stream != t.pendingStream && t.pending.Len() > 0 {
		t.flush()
	}
	t.pendingStream = stream
	for {
		newline := bytes.IndexByte(payload, '\n')
		if newline < 0 {
			t.buffer(payload)
			return nil
		}
		t.buffer(payload[:newline])
		t.flush()
		payload = payload[newline+1:]
	}
}

func (t *logTail) buffer(p []byte) {
	t.pending.Write(p[:min(len(p), maxLogLineBytes+1-t.pending.Len())])
}

// flush ends the pending line, cutting it to maxLogLineBytes on a UTF-8
// boundary when it is longer.
func (t *logTail) flush() {
	line := t.pending.String()
	t.pending.Reset()
	if len(line) > maxLogLineBytes {
		cut := maxLogLineBytes
		for cut > 0 && !utf8.RuneStart(line[cut]) {
			cut--
		}
		line = line[:cut]
		t.truncated = true
	}
	prefix := "stdout: "
	if t.pendingStream == docker.ContainerLogStderr {
		prefix = "stderr: "
	}
	t.add(prefix + line)
}

func (t *logTail) add(line string) {
	if t.count == maxLogLines {
		t.dropOldest()
	}
	t.ring[(t.start+t.count)%maxLogLines] = line
	t.count++
	t.size += len(line)
	for t.size > maxLogBytes {
		t.dropOldest()
	}
}

func (t *logTail) dropOldest() {
	t.size -= len(t.ring[t.start])
	t.ring[t.start] = ""
	t.start = (t.start + 1) % maxLogLines
	t.count--
	t.truncated = true
}

// lines returns the kept lines oldest first, or nil when there are none.
func (t *logTail) lines() []string {
	out := slices.Grow([]string(nil), t.count)
	for i := range t.count {
		out = append(out, t.ring[(t.start+i)%maxLogLines])
	}
	return out
}

// writeResult encodes a successful JSON-RPC response.
func writeResult(w http.ResponseWriter, id json.RawMessage, result any) {
	resp := rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// writeError encodes a JSON-RPC error response with HTTP 200.
func writeError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	writeErrorStatus(w, http.StatusOK, id, &rpcError{Code: code, Message: message})
}

// writeErrorStatus encodes a JSON-RPC error response with the given HTTP
// status.
func writeErrorStatus(w http.ResponseWriter, status int, id json.RawMessage, rpcErr *rpcError) {
	resp := rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   rpcErr,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

// toolResult returns a successful tools/call result with a text content block.
func toolResult(data any) map[string]any {
	b, err := json.Marshal(data)
	if err != nil {
		return toolError(fmt.Sprintf("marshal result: %v", err))
	}
	return map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": string(b),
			},
		},
		"isError": false,
	}
}

// toolError returns a tools/call result with isError: true.
func toolError(message string) map[string]any {
	return map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": message,
			},
		},
		"isError": true,
	}
}
