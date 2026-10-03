// Package mcp implements a read-only MCP (Model Context Protocol) server using
// the Streamable HTTP transport (protocol revision 2025-11-25). It exposes
// Docker container state to AI assistants over a single POST endpoint.
//
// Transport: stateless single-request mode. Requests return one JSON-RPC
// response object; notifications return 202 Accepted with no body.
// Session IDs are not assigned; clients operate without Mcp-Session-Id.
// This is compliant: the spec makes session management optional ("MAY").
//
// The GET method returns 405 Method Not Allowed to signal that the server
// does not offer an SSE stream at this endpoint.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/codeswhat/portwing/internal/docker"
	"github.com/codeswhat/portwing/internal/metrics"
	"github.com/codeswhat/portwing/internal/protocol"
)

// protocolVersion is the MCP spec revision this server implements.
const protocolVersion = "2025-11-25"

// JSON-RPC 2.0 error codes.
const (
	errParseError     = -32700
	errInvalidRequest = -32600
	errMethodNotFound = -32601
	errInvalidParams  = -32602
	errInternalError  = -32603
)

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
	if rawParams, ok := fields["params"]; ok {
		var params map[string]json.RawMessage
		if err := json.Unmarshal(rawParams, &params); err != nil || params == nil {
			if req.ID == nil {
				w.WriteHeader(http.StatusBadRequest)
			} else {
				writeError(w, req.ID, errInvalidParams, "params must be an object")
			}
			return
		}
		req.Params = rawParams
	}

	if req.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	ctx := r.Context()

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
// comes first because null unmarshals into a string without an error.
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

// toolsList returns the MCP tools/list result body.
func (h *Handler) toolsList() map[string]any {
	return map[string]any{
		"tools": []any{
			map[string]any{
				"name":        "list_containers",
				"description": "List all Docker containers (running and stopped) with id, names, image, state, status, and labels.",
				"inputSchema": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
				},
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
			},
			map[string]any{
				"name":        "host_metrics",
				"description": "Return a snapshot of host-level resource metrics: CPU, memory, disk, network, and uptime.",
				"inputSchema": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
				},
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
			},
		},
	}
}

// handleToolsCall dispatches tools/call to the appropriate tool implementation.
func (h *Handler) handleToolsCall(ctx context.Context, w http.ResponseWriter, req rpcRequest) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeError(w, req.ID, errInvalidParams, "invalid tools/call params")
		return
	}

	switch params.Name {
	case "list_containers":
		h.toolListContainers(ctx, w, req.ID)
	case "inspect_container":
		h.toolInspectContainer(ctx, w, req.ID, params.Arguments)
	case "container_logs":
		h.toolContainerLogs(ctx, w, req.ID, params.Arguments)
	case "host_metrics":
		h.toolHostMetrics(w, req.ID)
	case "container_stats":
		h.toolContainerStats(ctx, w, req.ID, params.Arguments)
	default:
		writeError(w, req.ID, errInvalidParams, fmt.Sprintf("unknown tool: %s", params.Name))
	}
}

// toolListContainers lists all containers.
func (h *Handler) toolListContainers(ctx context.Context, w http.ResponseWriter, id json.RawMessage) {
	if h.docker == nil {
		writeToolError(w, id, "docker client not available")
		return
	}
	containers, err := h.docker.ListContainers(ctx, true)
	if err != nil {
		writeToolError(w, id, fmt.Sprintf("list containers: %v", err))
		return
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

	writeToolResult(w, id, out)
}

// toolInspectContainer inspects a single container. Env values are never
// returned — only the count is exposed to prevent credential leakage.
func (h *Handler) toolInspectContainer(ctx context.Context, w http.ResponseWriter, id json.RawMessage, args json.RawMessage) {
	if h.docker == nil {
		writeToolError(w, id, "docker client not available")
		return
	}
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.ID == "" {
		writeToolError(w, id, "id is required")
		return
	}

	info, err := h.docker.InspectContainer(ctx, p.ID)
	if err != nil {
		writeToolError(w, id, fmt.Sprintf("inspect container: %v", err))
		return
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

	writeToolResult(w, id, out)
}

// toolContainerLogs returns demuxed log lines (stdout/stderr) for a container.
// Tail is capped at maxLogLines and the output at the other container_logs
// bounds; truncated reports whether anything was dropped or cut.
func (h *Handler) toolContainerLogs(ctx context.Context, w http.ResponseWriter, id json.RawMessage, args json.RawMessage) {
	if h.docker == nil {
		writeToolError(w, id, "docker client not available")
		return
	}
	var p struct {
		ID   string `json:"id"`
		Tail int    `json:"tail"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.ID == "" {
		writeToolError(w, id, "id is required")
		return
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
		writeToolError(w, id, fmt.Sprintf("container logs: %v", err))
		return
	}
	defer rc.Close()

	lines, truncated, err := decodeContainerLogTail(rc)
	if err != nil {
		writeToolError(w, id, fmt.Sprintf("decode logs: %v", err))
		return
	}

	out := map[string]any{
		"id":        p.ID,
		"lines":     lines,
		"truncated": truncated,
	}
	writeToolResult(w, id, out)
}

// toolHostMetrics returns the collector's host metrics snapshot.
func (h *Handler) toolHostMetrics(w http.ResponseWriter, id json.RawMessage) {
	if h.collector == nil {
		writeToolError(w, id, "metrics collector not available")
		return
	}
	m, err := h.collector.Collect()
	if err != nil {
		writeToolError(w, id, fmt.Sprintf("collect metrics: %v", err))
		return
	}
	writeToolResult(w, id, m)
}

// toolContainerStats returns a single-shot stats snapshot for a container.
func (h *Handler) toolContainerStats(ctx context.Context, w http.ResponseWriter, id json.RawMessage, args json.RawMessage) {
	if h.docker == nil {
		writeToolError(w, id, "docker client not available")
		return
	}
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.ID == "" {
		writeToolError(w, id, "id is required")
		return
	}

	stats, err := h.docker.ContainerStats(ctx, p.ID)
	if err != nil {
		writeToolError(w, id, fmt.Sprintf("container stats: %v", err))
		return
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
	writeToolResult(w, id, out)
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

// writeError encodes a JSON-RPC error response.
func writeError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	resp := rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: message},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// writeToolResult writes a successful tools/call result with a text content block.
func writeToolResult(w http.ResponseWriter, id json.RawMessage, data any) {
	b, err := json.Marshal(data)
	if err != nil {
		writeToolError(w, id, fmt.Sprintf("marshal result: %v", err))
		return
	}
	result := map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": string(b),
			},
		},
		"isError": false,
	}
	writeResult(w, id, result)
}

// writeToolError writes a tools/call result with isError: true.
func writeToolError(w http.ResponseWriter, id json.RawMessage, message string) {
	result := map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": message,
			},
		},
		"isError": true,
	}
	writeResult(w, id, result)
}
