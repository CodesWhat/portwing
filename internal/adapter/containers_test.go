package adapter

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codeswhat/lookout/internal/docker"
)

func TestContainerManagerConcurrentRefreshAndRead(t *testing.T) {
	t.Parallel()

	client, shutdown := newStubDockerClient(t)
	defer shutdown()

	manager := NewContainerManager(client, "test-agent", nil)
	if _, err := manager.BuildInventory(context.Background()); err != nil {
		t.Fatalf("build inventory: %v", err)
	}

	const (
		refreshWorkers = 4
		readWorkers    = 8
		iterations     = 300
	)

	ctx := context.Background()
	errCh := make(chan error, refreshWorkers)
	var wg sync.WaitGroup

	for i := 0; i < refreshWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				if _, _, _, err := manager.Refresh(ctx); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}

	for i := 0; i < readWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations*2; j++ {
				_ = manager.GetContainers()
				_, _ = manager.GetContainer("container-1")
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("refresh failed: %v", err)
	}
}

func newStubDockerClient(t *testing.T) (*docker.Client, func()) {
	t.Helper()

	socketPath := filepath.Join(t.TempDir(), "docker.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on unix socket: %v", err)
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
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]docker.ContainerJSON{
			{
				ID:      "container-1",
				Image:   "nginx:latest",
				ImageID: "sha256:test",
				Labels: map[string]string{
					"lookout.display_name": "container-1",
				},
			},
		})
	})

	mux.HandleFunc("/v1.44/containers/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/json") {
			http.NotFound(w, r)
			return
		}

		id := strings.TrimPrefix(r.URL.Path, "/v1.44/containers/")
		id = strings.TrimSuffix(id, "/json")
		if id == "" {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(docker.ContainerInspect{
			ID:      id,
			Name:    "/" + id,
			Created: time.Now().UTC().Format(time.RFC3339),
			State: docker.ContainerState{
				Status:    "running",
				Running:   true,
				StartedAt: time.Now().UTC().Format(time.RFC3339),
			},
			Config: docker.ContainerConfig{
				Image: "nginx:latest",
				Labels: map[string]string{
					"lookout.display_name": "container-1",
				},
			},
			NetworkSettings: &docker.NetworkSettings{
				Networks: map[string]docker.NetworkEndpoint{},
			},
		})
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

	return client, shutdown
}
