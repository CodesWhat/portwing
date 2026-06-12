// Package mcp implements a read-only MCP (Model Context Protocol) server using
// the Streamable HTTP transport (protocol revision 2025-11-25). It exposes
// Docker container state to AI assistants over a single POST endpoint.
//
// Transport: stateless single-request mode — every POST returns
// Content-Type: application/json with one JSON-RPC response object.
// Session IDs are not assigned; clients operate without Mcp-Session-Id.
// This is compliant: the spec makes session management optional ("MAY").
//
// The GET method returns 405 Method Not Allowed to signal that the server
// does not offer an SSE stream at this endpoint.
package mcp

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/codeswhat/lookout/internal/docker"
	"github.com/codeswhat/lookout/internal/metrics"
	"github.com/codeswhat/lookout/internal/protocol"
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
	Result  interface{}     `json:"result,omitempty"`
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

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, nil, errParseError, "parse error")
		return
	}

	if req.JSONRPC != "2.0" {
		writeError(w, req.ID, errInvalidRequest, "jsonrpc must be \"2.0\"")
		return
	}

	ctx := r.Context()

	switch req.Method {
	case "initialize":
		h.handleInitialize(w, req)
	case "notifications/initialized":
		// One-way notification — acknowledge with 202 and no body.
		w.WriteHeader(http.StatusAccepted)
	case "ping":
		writeResult(w, req.ID, map[string]interface{}{})
	case "tools/list":
		writeResult(w, req.ID, h.toolsList())
	case "tools/call":
		h.handleToolsCall(ctx, w, req)
	default:
		writeError(w, req.ID, errMethodNotFound, fmt.Sprintf("method not found: %s", req.Method))
	}
}

// handleInitialize responds to the MCP initialization handshake.
func (h *Handler) handleInitialize(w http.ResponseWriter, req rpcRequest) {
	result := map[string]interface{}{
		"protocolVersion": protocolVersion,
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{},
		},
		"serverInfo": map[string]interface{}{
			"name":    "lookout",
			"version": protocol.AgentVersion,
		},
	}
	writeResult(w, req.ID, result)
}

// toolsList returns the MCP tools/list result body.
func (h *Handler) toolsList() map[string]interface{} {
	return map[string]interface{}{
		"tools": []interface{}{
			map[string]interface{}{
				"name":        "list_containers",
				"description": "List all Docker containers (running and stopped) with id, names, image, state, status, and labels.",
				"inputSchema": map[string]interface{}{
					"type":                 "object",
					"additionalProperties": false,
				},
			},
			map[string]interface{}{
				"name":        "inspect_container",
				"description": "Inspect a container: state, image, env var count (no values), mounts, network names, and restart policy.",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id": map[string]interface{}{
							"type":        "string",
							"description": "Container ID or name.",
						},
					},
					"required": []string{"id"},
				},
			},
			map[string]interface{}{
				"name":        "container_logs",
				"description": "Return the last N lines (max 500) of stdout/stderr from a container.",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id": map[string]interface{}{
							"type":        "string",
							"description": "Container ID or name.",
						},
						"tail": map[string]interface{}{
							"type":        "integer",
							"description": "Number of log lines to return (1–500, default 100).",
							"minimum":     1,
							"maximum":     500,
						},
					},
					"required": []string{"id"},
				},
			},
			map[string]interface{}{
				"name":        "host_metrics",
				"description": "Return a snapshot of host-level resource metrics: CPU, memory, disk, network, and uptime.",
				"inputSchema": map[string]interface{}{
					"type":                 "object",
					"additionalProperties": false,
				},
			},
			map[string]interface{}{
				"name":        "container_stats",
				"description": "Return a one-shot CPU/memory/network stats snapshot for a single container.",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id": map[string]interface{}{
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

	out := map[string]interface{}{
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
// Tail is capped at 500 lines.
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
	if tail > 500 {
		tail = 500
	}

	tailStr := fmt.Sprintf("%d", tail)
	rc, err := h.docker.GetContainerLogs(ctx, p.ID, tailStr, "", "", false, false)
	if err != nil {
		writeToolError(w, id, fmt.Sprintf("container logs: %v", err))
		return
	}
	defer rc.Close()

	lines, err := demuxLogs(rc)
	if err != nil {
		writeToolError(w, id, fmt.Sprintf("demux logs: %v", err))
		return
	}

	out := map[string]interface{}{
		"id":    p.ID,
		"lines": lines,
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

	out := map[string]interface{}{
		"id":            p.ID,
		"cpuTotalUsage": stats.CPUStats.CPUUsage.TotalUsage,
		"memUsage":      stats.MemoryStats.Usage,
		"memLimit":      stats.MemoryStats.Limit,
		"networks":      networks,
	}
	writeToolResult(w, id, out)
}

// demuxLogs reads the Docker multiplexed log stream format and returns
// a slice of log line strings with a "stdout:" / "stderr:" prefix.
// Docker log multiplexing: 8-byte header per frame: [stream_type, 0, 0, 0, size(4)]
// stream_type: 1 = stdout, 2 = stderr.
func demuxLogs(r io.Reader) ([]string, error) {
	var lines []string

	hdr := make([]byte, 8)
	for {
		_, err := io.ReadFull(r, hdr)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return nil, err
		}

		streamType := hdr[0]
		size := binary.BigEndian.Uint32(hdr[4:8])
		if size == 0 {
			continue
		}

		payload := make([]byte, size)
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, err
		}

		text := strings.TrimRight(string(payload), "\n")
		prefix := "stdout"
		if streamType == 2 {
			prefix = "stderr"
		}

		for _, line := range strings.Split(text, "\n") {
			lines = append(lines, prefix+": "+line)
		}
	}

	return lines, nil
}

// writeResult encodes a successful JSON-RPC response.
func writeResult(w http.ResponseWriter, id json.RawMessage, result interface{}) {
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
func writeToolResult(w http.ResponseWriter, id json.RawMessage, data interface{}) {
	b, err := json.Marshal(data)
	if err != nil {
		writeToolError(w, id, fmt.Sprintf("marshal result: %v", err))
		return
	}
	result := map[string]interface{}{
		"content": []interface{}{
			map[string]interface{}{
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
	result := map[string]interface{}{
		"content": []interface{}{
			map[string]interface{}{
				"type": "text",
				"text": message,
			},
		},
		"isError": true,
	}
	writeResult(w, id, result)
}
