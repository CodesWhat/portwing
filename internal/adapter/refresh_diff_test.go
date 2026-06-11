package adapter

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codeswhat/lookout/internal/docker"
)

type fixtureContainer struct {
	id    string
	image string
	state string
}

type dockerInventorySnapshot struct {
	listed    []docker.ContainerJSON
	inspected map[string]docker.ContainerInspect
}

type dynamicDockerFixture struct {
	mu        sync.RWMutex
	inventory dockerInventorySnapshot
}

func (f *dynamicDockerFixture) Set(snapshot dockerInventorySnapshot) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inventory = snapshot
}

func (f *dynamicDockerFixture) Snapshot() dockerInventorySnapshot {
	f.mu.RLock()
	defer f.mu.RUnlock()

	listed := make([]docker.ContainerJSON, len(f.inventory.listed))
	copy(listed, f.inventory.listed)

	inspected := make(map[string]docker.ContainerInspect, len(f.inventory.inspected))
	for id, inspect := range f.inventory.inspected {
		inspected[id] = inspect
	}

	return dockerInventorySnapshot{
		listed:    listed,
		inspected: inspected,
	}
}

func TestContainerManagerRefreshDiffsAddedUpdatedRemoved(t *testing.T) {
	t.Parallel()

	client, fixture, shutdown := newDynamicDockerClient(t)
	defer shutdown()

	fixture.Set(buildInventorySnapshot([]fixtureContainer{
		{id: "c1", image: "nginx:1.0", state: "running"},
		{id: "c2", image: "redis:7", state: "running"},
	}))

	manager := NewContainerManager(client, "test-agent", nil)
	if _, err := manager.BuildInventory(context.Background()); err != nil {
		t.Fatalf("build inventory: %v", err)
	}

	fixture.Set(buildInventorySnapshot([]fixtureContainer{
		{id: "c1", image: "nginx:1.0", state: "exited"},
		{id: "c3", image: "postgres:16", state: "running"},
	}))

	added, updated, removed, err := manager.Refresh(context.Background())
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}

	assertContainerIDs(t, added, []string{"c3"})
	assertContainerIDs(t, updated, []string{"c1"})
	assertContainerIDs(t, removed, []string{"c2"})

	if updated[0].Status != "stopped" {
		t.Fatalf("expected updated container status to be stopped, got %q", updated[0].Status)
	}

	current := manager.GetContainers()
	assertContainerIDs(t, current, []string{"c1", "c3"})

	c1, ok := manager.GetContainer("c1")
	if !ok {
		t.Fatalf("expected c1 in container map")
	}
	if c1.Status != "stopped" {
		t.Fatalf("expected c1 status to be stopped in current map, got %q", c1.Status)
	}
}

func TestContainerManagerRefreshNoChanges(t *testing.T) {
	t.Parallel()

	client, fixture, shutdown := newDynamicDockerClient(t)
	defer shutdown()

	snapshot := buildInventorySnapshot([]fixtureContainer{
		{id: "c1", image: "nginx:1.0", state: "running"},
		{id: "c2", image: "redis:7", state: "exited"},
	})
	fixture.Set(snapshot)

	manager := NewContainerManager(client, "test-agent", nil)
	if _, err := manager.BuildInventory(context.Background()); err != nil {
		t.Fatalf("build inventory: %v", err)
	}

	fixture.Set(snapshot)

	added, updated, removed, err := manager.Refresh(context.Background())
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}

	if len(added) != 0 || len(updated) != 0 || len(removed) != 0 {
		t.Fatalf("expected empty diff, got added=%d updated=%d removed=%d", len(added), len(updated), len(removed))
	}
}

func assertContainerIDs(t *testing.T, containers []Container, want []string) {
	t.Helper()

	got := make([]string, 0, len(containers))
	for _, c := range containers {
		got = append(got, c.ID)
	}

	sort.Strings(got)
	wantSorted := append([]string(nil), want...)
	sort.Strings(wantSorted)

	if !reflect.DeepEqual(got, wantSorted) {
		t.Fatalf("container IDs mismatch: got %v want %v", got, wantSorted)
	}
}

func newDynamicDockerClient(t *testing.T) (*docker.Client, *dynamicDockerFixture, func()) {
	t.Helper()

	socketPath := shortSocketPath(t)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on unix socket: %v", err)
	}

	fixture := &dynamicDockerFixture{
		inventory: dockerInventorySnapshot{
			listed:    []docker.ContainerJSON{},
			inspected: map[string]docker.ContainerInspect{},
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(docker.VersionResponse{
			Version:    "26.0.0",
			APIVersion: "1.44",
		})
	})

	mux.HandleFunc("/v1.44/containers/json", func(w http.ResponseWriter, r *http.Request) {
		snapshot := fixture.Snapshot()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snapshot.listed)
	})

	mux.HandleFunc("/v1.44/containers/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/json") {
			http.NotFound(w, r)
			return
		}

		id := strings.TrimPrefix(r.URL.Path, "/v1.44/containers/")
		id = strings.TrimSuffix(id, "/json")

		snapshot := fixture.Snapshot()
		inspect, ok := snapshot.inspected[id]
		if !ok {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(inspect)
	})

	server := &http.Server{Handler: mux}
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		_ = server.Serve(listener)
	}()

	client, err := docker.NewClient(socketPath, 2)
	if err != nil {
		t.Fatalf("new docker client: %v", err)
	}

	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = listener.Close()
		<-serverDone
	}

	return client, fixture, shutdown
}

func buildInventorySnapshot(containers []fixtureContainer) dockerInventorySnapshot {
	listed := make([]docker.ContainerJSON, 0, len(containers))
	inspected := make(map[string]docker.ContainerInspect, len(containers))

	for _, c := range containers {
		image := c.image
		if image == "" {
			image = "nginx:latest"
		}

		listed = append(listed, docker.ContainerJSON{
			ID:      c.id,
			Image:   image,
			ImageID: "sha256:" + c.id,
			Labels: map[string]string{
				"test.id": c.id,
			},
		})

		state := docker.ContainerState{
			Status:    c.state,
			StartedAt: "2026-01-01T00:00:00Z",
		}
		switch c.state {
		case "running":
			state.Running = true
		case "paused":
			state.Paused = true
		case "restarting":
			state.Restarting = true
		case "dead":
			state.Dead = true
		}

		inspected[c.id] = docker.ContainerInspect{
			ID:      c.id,
			Name:    "/" + c.id,
			Created: "2026-01-01T00:00:00Z",
			State:   state,
			Config: docker.ContainerConfig{
				Image: image,
				Labels: map[string]string{
					"test.id": c.id,
				},
			},
			NetworkSettings: &docker.NetworkSettings{
				Networks: map[string]docker.NetworkEndpoint{},
			},
		}
	}

	return dockerInventorySnapshot{
		listed:    listed,
		inspected: inspected,
	}
}
