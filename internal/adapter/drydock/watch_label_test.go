// SPDX-License-Identifier: MIT

package drydock

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codeswhat/portwing/internal/docker"
)

func TestWatchLabelInventoryAndSyncUseAdvertisedWatcher(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"true", "false", ""} {
		t.Run(value, func(t *testing.T) {
			labels := map[string]string{"dd.watch": value}
			listener, err := net.Listen("unix", shortSocketPath(t))
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/version":
					_ = json.NewEncoder(w).Encode(docker.VersionResponse{APIVersion: "1.44"})
				case "/v1.44/containers/json":
					_ = json.NewEncoder(w).Encode([]docker.ContainerJSON{{ID: "web", Labels: labels}})
				case "/v1.44/containers/web/json":
					_ = json.NewEncoder(w).Encode(docker.ContainerInspect{ID: "web", Name: "/web", Config: docker.ContainerConfig{Image: "nginx:latest", Labels: labels}})
				default:
					http.NotFound(w, r)
				}
			}))
			_ = server.Listener.Close()
			server.Listener = listener
			server.Start()
			t.Cleanup(server.Close)
			client, err := docker.NewClient(listener.Addr().String(), 2)
			if err != nil {
				t.Fatal(err)
			}
			a := NewAdapter(client, "test-agent", AgentInfo{})
			inventory, err := a.containers.BuildInventory(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(inventory) != 1 {
				t.Fatalf("inventory = %+v, want one container", inventory)
			}
			sender := &captureSender{}
			a.sendContainerSync(sender, inventory)
			encoded, err := json.Marshal(sender.data)
			if err != nil {
				t.Fatal(err)
			}
			var payload struct {
				Containers []drydockContainer `json:"containers"`
			}
			if err := json.Unmarshal(encoded, &payload); err != nil {
				t.Fatal(err)
			}
			if len(payload.Containers) != 1 {
				t.Fatalf("payload = %s, want one container", encoded)
			}
			advertised := GetWatcherComponents()
			if len(advertised) != 1 {
				t.Fatalf("watchers = %+v, want Docker watcher", advertised)
			}
			c := payload.Containers[0]
			if c.Watcher != advertised[0].Name || c.Watcher != "docker" {
				t.Errorf("dd.watch=%q: wire watcher=%q, advertised=%q", value, c.Watcher, advertised[0].Name)
			}
			if c.Labels[LabelWatch] != value {
				t.Errorf("dd.watch=%q: original label lost: %+v", value, c.Labels)
			}
		})
	}
}
