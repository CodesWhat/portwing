package drydock

import (
	"encoding/json"
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
