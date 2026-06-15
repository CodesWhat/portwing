package generic

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/codeswhat/portwing/internal/adapter"
	"github.com/codeswhat/portwing/internal/docker"
	"github.com/codeswhat/portwing/internal/protocol"
)

// Adapter is the generic standalone adapter. It exposes a clean REST surface
// under /api/v1/* without requiring any external controller.
type Adapter struct {
	containers   *adapter.ContainerManager
	dockerClient *docker.Client
	events       *EventBroadcaster
}

// New creates a generic adapter wired to the given Docker client.
func New(dockerClient *docker.Client, agentName string) *Adapter {
	cm := adapter.NewContainerManager(dockerClient, agentName, nil)
	return &Adapter{
		containers:   cm,
		dockerClient: dockerClient,
		events:       NewEventBroadcaster(dockerClient),
	}
}

func (a *Adapter) Name() string { return "generic" }

func (a *Adapter) Capabilities() []string {
	return []string{
		"containers",
		"logs",
		"events",
		"version",
	}
}

func (a *Adapter) HelloExtension() *adapter.HelloExtension { return nil }

func (a *Adapter) PollInterval() int { return 0 }

func (a *Adapter) OnConnect(_ context.Context, _ adapter.MessageSender) error { return nil }

func (a *Adapter) RefreshContainers(ctx context.Context) (added, updated, removed []adapter.Container, err error) {
	return a.containers.Refresh(ctx)
}

func (a *Adapter) OnContainerRefresh(_ context.Context, _ adapter.MessageSender, _, _, _ []adapter.Container) error {
	return nil
}

func (a *Adapter) HandleMessage(_ context.Context, _ adapter.MessageSender, _ string, _ json.RawMessage) bool {
	return false
}

// RegisterRoutes registers generic REST routes on /api/v1/*.
func (a *Adapter) RegisterRoutes(mux *http.ServeMux, auth func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/v1/containers", auth(a.handleContainers))
	mux.Handle("GET /api/v1/containers/{id}/logs", auth(a.handleContainerLogs))
	mux.Handle("GET /api/v1/events", auth(a.events.ServeHTTP))
	mux.Handle("GET /api/v1/version", auth(a.handleVersion))
}

// versionResponse is the payload returned by GET /api/v1/version.
type versionResponse struct {
	AgentVersion    string `json:"agentVersion"`
	ProtocolName    string `json:"protocolName"`
	ProtocolVersion string `json:"protocolVersion"`
	Adapter         string `json:"adapter"`
}

func (a *Adapter) handleVersion(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(versionResponse{
		AgentVersion:    protocol.AgentVersion,
		ProtocolName:    protocol.ProtocolName,
		ProtocolVersion: protocol.ProtocolVersion,
		Adapter:         a.Name(),
	})
}
