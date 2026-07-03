package drydock

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/adapter"
)

type fakeContainerProvider struct {
	containers []adapter.Container
}

func (f fakeContainerProvider) GetContainers() []adapter.Container {
	return f.containers
}

func TestBuildAckPayloadWithContainerProvider(t *testing.T) {
	provider := fakeContainerProvider{
		containers: []adapter.Container{
			{
				ID:     "c1",
				Status: "running",
				Image:  adapter.ContainerImage{ID: "img-a"},
			},
			{
				ID:     "c2",
				Status: "exited",
				Image:  adapter.ContainerImage{ID: "img-b"},
			},
			{
				ID:     "c3",
				Status: "paused",
				Image:  adapter.ContainerImage{ID: "img-b"},
			},
		},
	}

	b := NewSSEBroadcaster(provider, "v-test", AgentInfo{})
	b.startTime = time.Now().Add(-3 * time.Second)

	var payload ackPayload
	if err := json.Unmarshal(b.buildAckPayload(), &payload); err != nil {
		t.Fatalf("unmarshal ack payload: %v", err)
	}

	if payload.Type != "dd:ack" {
		t.Fatalf("unexpected payload type: got %q want %q", payload.Type, "dd:ack")
	}
	if payload.Data.Version != "v-test" {
		t.Fatalf("unexpected version: got %q want %q", payload.Data.Version, "v-test")
	}
	if payload.Data.Containers.Total != 3 {
		t.Fatalf("unexpected total containers: got %d want %d", payload.Data.Containers.Total, 3)
	}
	if payload.Data.Containers.Running != 1 {
		t.Fatalf("unexpected running containers: got %d want %d", payload.Data.Containers.Running, 1)
	}
	if payload.Data.Containers.Stopped != 2 {
		t.Fatalf("unexpected stopped containers: got %d want %d", payload.Data.Containers.Stopped, 2)
	}
	if payload.Data.Images != 2 {
		t.Fatalf("unexpected image count: got %d want %d", payload.Data.Images, 2)
	}
	if payload.Data.UptimeSeconds < 3 {
		t.Fatalf("unexpected uptime: got %d want at least %d", payload.Data.UptimeSeconds, 3)
	}
	if payload.Data.LastSeen == "" {
		t.Fatalf("expected lastSeen to be populated")
	}
}

// TestBuildAckPayloadAgentInfo verifies logLevel and pollInterval are present
// in the marshaled dd:ack payload when AgentInfo is populated.
func TestBuildAckPayloadAgentInfo(t *testing.T) {
	b := NewSSEBroadcaster(fakeContainerProvider{}, "v-test", AgentInfo{LogLevel: "debug", PollInterval: "5m0s"})

	var payload struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(b.buildAckPayload(), &payload); err != nil {
		t.Fatalf("unmarshal ack payload: %v", err)
	}

	if got, _ := payload.Data["logLevel"].(string); got != "debug" {
		t.Fatalf("unexpected logLevel: got %v want %q", payload.Data["logLevel"], "debug")
	}
	if got, _ := payload.Data["pollInterval"].(string); got != "5m0s" {
		t.Fatalf("unexpected pollInterval: got %v want %q", payload.Data["pollInterval"], "5m0s")
	}
}

// TestBuildAckPayloadAgentInfoOmittedWhenZero verifies logLevel and
// pollInterval are absent (omitempty) when AgentInfo is the zero value.
func TestBuildAckPayloadAgentInfoOmittedWhenZero(t *testing.T) {
	b := NewSSEBroadcaster(fakeContainerProvider{}, "v-test", AgentInfo{})

	var payload struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(b.buildAckPayload(), &payload); err != nil {
		t.Fatalf("unmarshal ack payload: %v", err)
	}

	if _, ok := payload.Data["logLevel"]; ok {
		t.Fatalf("expected logLevel to be omitted, got %v", payload.Data["logLevel"])
	}
	if _, ok := payload.Data["pollInterval"]; ok {
		t.Fatalf("expected pollInterval to be omitted, got %v", payload.Data["pollInterval"])
	}
}

func TestBuildWatcherSnapshotPayload(t *testing.T) {
	provider := fakeContainerProvider{
		containers: []adapter.Container{
			{ID: "c1", Name: "app-1", Status: "running"},
			{ID: "c2", Name: "app-2", Status: "exited"},
		},
	}

	b := NewSSEBroadcaster(provider, "v-test", AgentInfo{})

	raw, err := b.buildWatcherSnapshotPayload()
	if err != nil {
		t.Fatalf("build watcher snapshot payload: %v", err)
	}

	var payload struct {
		Type string `json:"type"`
		Data struct {
			Watcher struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"watcher"`
			Containers []adapter.Container `json:"containers"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal watcher snapshot payload: %v", err)
	}

	if payload.Type != "dd:watcher-snapshot" {
		t.Fatalf("unexpected payload type: got %q want %q", payload.Type, "dd:watcher-snapshot")
	}
	if payload.Data.Watcher.Type != "docker" || payload.Data.Watcher.Name != "docker" {
		t.Fatalf("unexpected watcher descriptor: got %+v", payload.Data.Watcher)
	}
	if len(payload.Data.Containers) != 2 {
		t.Fatalf("unexpected container count: got %d want %d", len(payload.Data.Containers), 2)
	}
	if payload.Data.Containers[0].ID != "c1" {
		t.Fatalf("unexpected first container: got %q want %q", payload.Data.Containers[0].ID, "c1")
	}
}

func TestBuildWatcherSnapshotPayloadEmptyInventory(t *testing.T) {
	b := NewSSEBroadcaster(fakeContainerProvider{}, "v-test", AgentInfo{})

	raw, err := b.buildWatcherSnapshotPayload()
	if err != nil {
		t.Fatalf("build watcher snapshot payload: %v", err)
	}

	var payload struct {
		Data struct {
			Containers json.RawMessage `json:"containers"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal watcher snapshot payload: %v", err)
	}

	// Drydock expects a JSON array, never null.
	if string(payload.Data.Containers) != "[]" {
		t.Fatalf("expected empty containers array, got %s", payload.Data.Containers)
	}
}
