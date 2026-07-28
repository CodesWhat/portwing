package drydock

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/codeswhat/portwing/internal/adapter"
)

func TestContainerSyncUsesCurrentDrydockImageShape(t *testing.T) {
	t.Parallel()

	a := &Adapter{}
	sender := &captureSender{}
	a.sendContainerSync(sender, []adapter.Container{
		{
			ID:          "container-1",
			Name:        "web",
			DisplayName: "web",
			Status:      "running",
			Watcher:     "docker",
			Image: adapter.ContainerImage{
				ID:       "sha256:1234",
				Registry: "docker.io",
				Name:     "library/nginx",
				Tag:      "1.27.0",
				Digest:   "sha256:abcd",
			},
			UpdateKind: adapter.UpdateKindUnknown,
		},
	})

	encoded, err := json.Marshal(sender.data)
	if err != nil {
		t.Fatalf("marshal container sync: %v", err)
	}
	var payload struct {
		Containers []struct {
			Image struct {
				Registry struct {
					Name string `json:"name"`
					URL  string `json:"url"`
				} `json:"registry"`
				Tag struct {
					Value  string `json:"value"`
					Semver bool   `json:"semver"`
				} `json:"tag"`
				Digest struct {
					Watch bool   `json:"watch"`
					Value string `json:"value"`
				} `json:"digest"`
				Architecture string `json:"architecture"`
				OS           string `json:"os"`
			} `json:"image"`
		} `json:"containers"`
	}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("decode container sync: %v", err)
	}
	if len(payload.Containers) != 1 {
		t.Fatalf("containers = %d, want 1", len(payload.Containers))
	}
	image := payload.Containers[0].Image
	if image.Registry.Name == "" || image.Registry.URL != "docker.io" {
		t.Fatalf("registry = %+v, want non-empty name and docker.io URL", image.Registry)
	}
	if image.Tag.Value != "1.27.0" {
		t.Fatalf("tag = %+v, want value 1.27.0", image.Tag)
	}
	if image.Digest.Watch || image.Digest.Value != "sha256:abcd" {
		t.Fatalf("digest = %+v, want watch=false and value sha256:abcd", image.Digest)
	}
	if image.Architecture == "" || image.OS == "" {
		t.Fatalf("architecture/os = %q/%q, want non-empty", image.Architecture, image.OS)
	}
}

func TestDrydockContainerIncludesRuntimeDetailsAndError(t *testing.T) {
	t.Parallel()

	got := toDrydockContainer(adapter.Container{
		ID:     "container-1",
		Status: "running",
		Error:  &adapter.ContainerError{Message: "inspect failed"},
		Details: &adapter.RuntimeDetails{
			Health: "healthy",
			Ports: []adapter.PortMapping{
				{Container: 80, Protocol: "tcp"},
				{Container: 443, Host: 8443, Protocol: "tcp"},
				{Container: 53, Host: 5353, Protocol: "udp", IP: "127.0.0.1"},
			},
			Volumes: []adapter.VolumeInfo{
				{Source: "/srv/data", Destination: "/data"},
				{Source: "/srv/config", Destination: "/config", ReadOnly: true},
			},
			Started: "2026-07-28T12:00:00Z",
		},
	})

	if got.Error == nil || got.Error.Message != "inspect failed" {
		t.Fatalf("error = %+v, want inspect failed", got.Error)
	}
	if got.Health != "healthy" {
		t.Fatalf("health = %q, want healthy", got.Health)
	}
	if got.Details == nil {
		t.Fatal("details = nil, want runtime details")
	}
	if got.Details.Env == nil || len(got.Details.Env) != 0 {
		t.Fatalf("env = %#v, want non-nil empty slice", got.Details.Env)
	}
	wantPorts := []string{"80/tcp", "8443->443/tcp", "127.0.0.1:5353->53/udp"}
	if !slices.Equal(got.Details.Ports, wantPorts) {
		t.Fatalf("ports = %#v, want %#v", got.Details.Ports, wantPorts)
	}
	wantVolumes := []string{"/srv/data:/data", "/srv/config:/config:ro"}
	if !slices.Equal(got.Details.Volumes, wantVolumes) {
		t.Fatalf("volumes = %#v, want %#v", got.Details.Volumes, wantVolumes)
	}
	if got.Details.StartedAt != "2026-07-28T12:00:00Z" {
		t.Fatalf("startedAt = %q, want preserved timestamp", got.Details.StartedAt)
	}
}
