package generic

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/codeswhat/portwing/internal/docker"
)

// genericEvent is the stable envelope written on the SSE stream.
type genericEvent struct {
	TS          string            `json:"ts"`
	Type        string            `json:"type"`
	Action      string            `json:"action"`
	ContainerID string            `json:"containerId"`
	Name        string            `json:"name"`
	Image       string            `json:"image"`
	Labels      map[string]string `json:"labels,omitempty"`
}

type sseClient struct {
	id     string
	events chan []byte
}

// EventBroadcaster subscribes to Docker container events and fans them out
// to connected SSE clients.
type EventBroadcaster struct {
	dockerClient *docker.Client

	mu      sync.RWMutex
	clients map[string]*sseClient
}

// NewEventBroadcaster creates an EventBroadcaster.
func NewEventBroadcaster(dockerClient *docker.Client) *EventBroadcaster {
	return &EventBroadcaster{
		dockerClient: dockerClient,
		clients:      make(map[string]*sseClient),
	}
}

// ServeHTTP implements http.Handler for SSE connections.
func (b *EventBroadcaster) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	client := &sseClient{
		id:     uuid.New().String(),
		events: make(chan []byte, 64),
	}

	b.mu.Lock()
	b.clients[client.id] = client
	b.mu.Unlock()

	slog.Info("generic SSE client connected", "clientId", client.id)

	// Subscribe to Docker events for this client's lifetime.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	stream := docker.NewEventStream(b.dockerClient)
	eventCh, err := stream.Subscribe(ctx)
	if err != nil {
		slog.Error("failed to subscribe to docker events", "error", err)
		b.removeClient(client.id)
		return
	}

	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("generic SSE client disconnected", "clientId", client.id)
			b.removeClient(client.id)
			return

		case <-heartbeat.C:
			if _, err := fmt.Fprintf(w, ": heartbeat\n\n"); err != nil {
				b.removeClient(client.id)
				return
			}
			flusher.Flush()

		case de, ok := <-eventCh:
			if !ok {
				b.removeClient(client.id)
				return
			}

			ge := genericEvent{
				TS:          time.Unix(de.Time, 0).UTC().Format(time.RFC3339),
				Type:        de.Type,
				Action:      de.Action,
				ContainerID: de.Actor.ID,
				Name:        de.Actor.Attributes["name"],
				Image:       de.Actor.Attributes["image"],
				Labels:      filterLabels(de.Actor.Attributes),
			}

			data, err := json.Marshal(ge)
			if err != nil {
				slog.Error("failed to marshal generic event", "error", err)
				continue
			}

			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				b.removeClient(client.id)
				return
			}
			flusher.Flush()
		}
	}
}

// removeClient cleans up a disconnected client.
func (b *EventBroadcaster) removeClient(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.clients, id)
	slog.Info("generic SSE client removed", "clientId", id)
}

// filterLabels strips the synthetic Docker attributes (name, image, etc.)
// that are not real container labels, leaving only the label key/value pairs.
var syntheticKeys = map[string]bool{
	"name":                              true,
	"image":                             true,
	"exitCode":                          true,
	"signal":                            true,
	"execID":                            true,
	"maintainer":                        true,
	"org.opencontainers.image.created":  true,
	"org.opencontainers.image.revision": true,
	"org.opencontainers.image.source":   true,
	"org.opencontainers.image.title":    true,
	"org.opencontainers.image.url":      true,
	"org.opencontainers.image.version":  true,
}

func filterLabels(attrs map[string]string) map[string]string {
	if len(attrs) == 0 {
		return nil
	}
	out := make(map[string]string)
	for k, v := range attrs {
		if !syntheticKeys[k] {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
