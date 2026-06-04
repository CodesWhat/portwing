package drydock

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/codeswhat/lookout/internal/adapter"
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

	b := NewSSEBroadcaster(provider, "v-test")
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
