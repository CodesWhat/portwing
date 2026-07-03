package drydock

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/codeswhat/portwing/internal/adapter"
	"github.com/codeswhat/portwing/internal/metrics"
)

type SSEClient struct {
	id     string
	events chan []byte
}

// ContainerProvider supplies the current container inventory for SSE payloads.
type ContainerProvider interface {
	GetContainers() []adapter.Container
}

// AgentInfo carries the static agent runtime details reported in the dd:ack
// event alongside the values derived at runtime.
type AgentInfo struct {
	LogLevel     string
	PollInterval string
}

type SSEBroadcaster struct {
	mu           sync.RWMutex
	clients      map[string]*SSEClient
	manager      ContainerProvider
	agentVersion string
	agentInfo    AgentInfo
	memoryGB     float64
	startTime    time.Time
}

func NewSSEBroadcaster(manager ContainerProvider, agentVersion string, info AgentInfo) *SSEBroadcaster {
	return &SSEBroadcaster{
		clients:      make(map[string]*SSEClient),
		manager:      manager,
		agentVersion: agentVersion,
		agentInfo:    info,
		memoryGB:     metrics.MemoryTotalGB(),
		startTime:    time.Now(),
	}
}

// ServeHTTP implements http.Handler for SSE connections. It sets the
// appropriate headers, registers the client, sends an initial dd:ack
// event, and then streams events until the client disconnects.
func (b *SSEBroadcaster) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	client := &SSEClient{
		id:     uuid.New().String(),
		events: make(chan []byte, 64),
	}

	b.mu.Lock()
	b.clients[client.id] = client
	b.mu.Unlock()

	slog.Info("SSE client connected", "clientId", client.id)

	// Send initial dd:ack event.
	ackPayload := b.buildAckPayload()
	ackEvent := fmt.Sprintf("data: %s\n\n", ackPayload)
	if _, err := w.Write([]byte(ackEvent)); err != nil {
		b.removeClient(client.id)
		return
	}
	flusher.Flush()

	// Follow with the current watcher snapshot so the client immediately
	// has the authoritative inventory without waiting for a poll cycle.
	if snap, err := b.buildWatcherSnapshotPayload(); err == nil {
		if _, err := fmt.Fprintf(w, "data: %s\n\n", snap); err != nil {
			b.removeClient(client.id)
			return
		}
		flusher.Flush()
	}

	// Stream events until client disconnects.
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			slog.Info("SSE client disconnected", "clientId", client.id)
			b.removeClient(client.id)
			return
		case data, ok := <-client.events:
			if !ok {
				return
			}
			event := fmt.Sprintf("data: %s\n\n", data)
			if _, err := w.Write([]byte(event)); err != nil {
				b.removeClient(client.id)
				return
			}
			flusher.Flush()
		}
	}
}

// ackPayload represents the dd:ack SSE event structure.
type ackPayload struct {
	Type string      `json:"type"`
	Data ackDataBody `json:"data"`
}

type ackDataBody struct {
	Version       string        `json:"version"`
	OS            string        `json:"os"`
	Arch          string        `json:"arch"`
	CPUs          int           `json:"cpus"`
	MemoryGB      float64       `json:"memoryGb"`
	UptimeSeconds int64         `json:"uptimeSeconds"`
	LastSeen      string        `json:"lastSeen"`
	LogLevel      string        `json:"logLevel,omitempty"`
	PollInterval  string        `json:"pollInterval,omitempty"`
	Containers    ackContainers `json:"containers"`
	Images        int           `json:"images"`
}

type ackContainers struct {
	Total   int `json:"total"`
	Running int `json:"running"`
	Stopped int `json:"stopped"`
}

// buildAckPayload builds the dd:ack JSON payload per spec 10.3.
func (b *SSEBroadcaster) buildAckPayload() []byte {
	containers := b.manager.GetContainers()

	var running, stopped int
	imageSet := make(map[string]struct{})
	for _, c := range containers {
		switch c.Status {
		case "running":
			running++
		default:
			stopped++
		}
		imageSet[c.Image.ID] = struct{}{}
	}

	uptimeSeconds := int64(time.Since(b.startTime).Seconds())

	payload := ackPayload{
		Type: "dd:ack",
		Data: ackDataBody{
			Version:       b.agentVersion,
			OS:            runtime.GOOS,
			Arch:          runtime.GOARCH,
			CPUs:          runtime.NumCPU(),
			MemoryGB:      b.memoryGB,
			UptimeSeconds: uptimeSeconds,
			LastSeen:      time.Now().UTC().Format(time.RFC3339),
			LogLevel:      b.agentInfo.LogLevel,
			PollInterval:  b.agentInfo.PollInterval,
			Containers: ackContainers{
				Total:   len(containers),
				Running: running,
				Stopped: stopped,
			},
			Images: len(imageSet),
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		slog.Error("failed to marshal ack payload", "error", err)
		return []byte(`{"type":"dd:ack","data":{}}`)
	}
	return data
}

// buildWatcherSnapshotPayload builds the dd:watcher-snapshot event carrying
// the watcher descriptor and the full authoritative container inventory.
// Drydock prunes containers absent from this snapshot (AgentClient.ts
// handleWatcherSnapshotEvent), so it must always contain the complete
// current inventory.
func (b *SSEBroadcaster) buildWatcherSnapshotPayload() ([]byte, error) {
	containers := b.manager.GetContainers()
	if containers == nil {
		containers = []adapter.Container{}
	}

	event := map[string]any{
		"type": "dd:watcher-snapshot",
		"data": map[string]any{
			"watcher":    GetWatcherComponents()[0],
			"containers": containers,
		},
	}
	return json.Marshal(event)
}

// BroadcastWatcherSnapshot sends a dd:watcher-snapshot event to all clients.
// Called after each container poll cycle.
func (b *SSEBroadcaster) BroadcastWatcherSnapshot() {
	data, err := b.buildWatcherSnapshotPayload()
	if err != nil {
		slog.Error("failed to marshal watcher-snapshot event", "error", err)
		return
	}
	b.broadcast(data)
}

// BroadcastContainerAdded sends a dd:container-added event to all clients.
func (b *SSEBroadcaster) BroadcastContainerAdded(c adapter.Container) {
	event := map[string]any{
		"type": "dd:container-added",
		"data": c,
	}
	data, err := json.Marshal(event)
	if err != nil {
		slog.Error("failed to marshal container-added event", "error", err)
		return
	}
	b.broadcast(data)
}

// BroadcastContainerUpdated sends a dd:container-updated event to all clients.
func (b *SSEBroadcaster) BroadcastContainerUpdated(c adapter.Container) {
	event := map[string]any{
		"type": "dd:container-updated",
		"data": c,
	}
	data, err := json.Marshal(event)
	if err != nil {
		slog.Error("failed to marshal container-updated event", "error", err)
		return
	}
	b.broadcast(data)
}

// BroadcastContainerRemoved sends a dd:container-removed event to all clients.
func (b *SSEBroadcaster) BroadcastContainerRemoved(id, name string) {
	event := map[string]any{
		"type": "dd:container-removed",
		"data": map[string]string{
			"id":   id,
			"name": name,
		},
	}
	data, err := json.Marshal(event)
	if err != nil {
		slog.Error("failed to marshal container-removed event", "error", err)
		return
	}
	b.broadcast(data)
}

// broadcast sends raw data to all connected SSE clients.
func (b *SSEBroadcaster) broadcast(data []byte) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, client := range b.clients {
		select {
		case client.events <- data:
		default:
			slog.Warn("SSE client buffer full, dropping event", "clientId", client.id)
		}
	}
}

// removeClient removes a disconnected client and closes its channels.
func (b *SSEBroadcaster) removeClient(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	client, ok := b.clients[id]
	if !ok {
		return
	}

	close(client.events)
	delete(b.clients, id)
	slog.Info("SSE client removed", "clientId", id)
}
