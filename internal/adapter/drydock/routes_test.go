package drydock

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	adapterpkg "github.com/codeswhat/portwing/internal/adapter"
	"github.com/codeswhat/portwing/internal/docker"
)

// shortSocketPath returns a temp socket path short enough for the unix
// socket path limit (104 bytes on darwin); t.TempDir() embeds the full
// test name and overflows it.
func shortSocketPath(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "lk")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	return filepath.Join(dir, "d.sock")
}

func TestHandleContainersUsesCachedInventory(t *testing.T) {
	t.Parallel()

	client, calls, shutdown := newRouteTestDockerClient(t)
	defer shutdown()

	a := NewAdapter(client, "test-agent")
	if _, err := a.containers.BuildInventory(context.Background()); err != nil {
		t.Fatalf("build inventory: %v", err)
	}

	baseListCalls := calls.listCalls.Load()
	baseInspectCalls := calls.inspectCalls.Load()
	if baseListCalls == 0 || baseInspectCalls == 0 {
		t.Fatalf("expected initial docker calls to prime cache, got list=%d inspect=%d", baseListCalls, baseInspectCalls)
	}

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/containers", nil)
		rec := httptest.NewRecorder()

		a.handleContainers(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusOK)
		}

		var containers []adapterpkg.Container
		if err := json.NewDecoder(rec.Body).Decode(&containers); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if len(containers) != 1 {
			t.Fatalf("unexpected containers length: got %d want %d", len(containers), 1)
		}
	}

	if got := calls.listCalls.Load(); got != baseListCalls {
		t.Fatalf("expected no additional list calls after cache warmup; got %d want %d", got, baseListCalls)
	}
	if got := calls.inspectCalls.Load(); got != baseInspectCalls {
		t.Fatalf("expected no additional inspect calls after cache warmup; got %d want %d", got, baseInspectCalls)
	}
}

func TestHandleContainerLogsRejectsInvalidTail(t *testing.T) {
	t.Parallel()

	client, calls, shutdown := newRouteTestDockerClient(t)
	defer shutdown()

	a := NewAdapter(client, "test-agent")

	tests := []struct {
		name string
		tail string
	}{
		{name: "non numeric", tail: "abc"},
		{name: "zero", tail: "0"},
		{name: "negative", tail: "-5"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			before := calls.logsCalls.Load()

			req := httptest.NewRequest(http.MethodGet, "/api/containers/container-1/logs?tail="+tt.tail, nil)
			req.SetPathValue("id", "container-1")
			rec := httptest.NewRecorder()

			a.handleContainerLogs(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusBadRequest)
			}
			if got := calls.logsCalls.Load(); got != before {
				t.Fatalf("expected docker logs endpoint not to be called; got %d want %d", got, before)
			}
		})
	}
}

func TestHandleContainerLogsAcceptsPositiveTail(t *testing.T) {
	t.Parallel()

	client, calls, shutdown := newRouteTestDockerClient(t)
	defer shutdown()

	a := NewAdapter(client, "test-agent")

	req := httptest.NewRequest(http.MethodGet, "/api/containers/container-1/logs?tail=5", nil)
	req.SetPathValue("id", "container-1")
	rec := httptest.NewRecorder()

	a.handleContainerLogs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusOK)
	}
	if calls.logsCalls.Load() != 1 {
		t.Fatalf("expected one docker logs call, got %d", calls.logsCalls.Load())
	}
	if rec.Body.String() != "log line\n" {
		t.Fatalf("unexpected logs body: got %q want %q", rec.Body.String(), "log line\n")
	}
}

func TestHandleWatcherGetReturnsKnownWatcher(t *testing.T) {
	t.Parallel()

	client, _, shutdown := newRouteTestDockerClient(t)
	defer shutdown()

	a := NewAdapter(client, "test-agent")

	req := httptest.NewRequest(http.MethodGet, "/api/watchers/docker/docker", nil)
	req.SetPathValue("type", "docker")
	req.SetPathValue("name", "docker")
	rec := httptest.NewRecorder()

	a.handleWatcherGet(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("unexpected content-type: got %q", ct)
	}

	var desc map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&desc); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if desc["type"] != "docker" {
		t.Fatalf("unexpected type: got %v want docker", desc["type"])
	}
	if desc["name"] != "docker" {
		t.Fatalf("unexpected name: got %v want docker", desc["name"])
	}
}

func TestHandleWatcherGetReturns404ForUnknown(t *testing.T) {
	t.Parallel()

	client, _, shutdown := newRouteTestDockerClient(t)
	defer shutdown()

	a := NewAdapter(client, "test-agent")

	req := httptest.NewRequest(http.MethodGet, "/api/watchers/unknown/missing", nil)
	req.SetPathValue("type", "unknown")
	req.SetPathValue("name", "missing")
	rec := httptest.NewRecorder()

	a.handleWatcherGet(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleLogEntriesReturnsEmptyArray(t *testing.T) {
	t.Parallel()

	client, _, shutdown := newRouteTestDockerClient(t)
	defer shutdown()

	a := NewAdapter(client, "test-agent")

	req := httptest.NewRequest(http.MethodGet, "/api/log/entries", nil)
	rec := httptest.NewRecorder()

	a.handleLogEntries(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("unexpected content-type: got %q", ct)
	}

	var entries []interface{}
	if err := json.NewDecoder(rec.Body).Decode(&entries); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty array, got %d entries", len(entries))
	}
}

type routeTestDockerCalls struct {
	listCalls    atomic.Int64
	inspectCalls atomic.Int64
	logsCalls    atomic.Int64
}

func newRouteTestDockerClient(t *testing.T) (*docker.Client, *routeTestDockerCalls, func()) {
	t.Helper()

	socketPath := shortSocketPath(t)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on unix socket: %v", err)
	}

	calls := &routeTestDockerCalls{}

	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(docker.VersionResponse{
			Version:    "26.0.0",
			APIVersion: "1.44",
		})
	})

	mux.HandleFunc("/v1.44/containers/json", func(w http.ResponseWriter, r *http.Request) {
		calls.listCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]docker.ContainerJSON{
			{
				ID:      "container-1",
				Image:   "nginx:latest",
				ImageID: "sha256:test",
				Labels: map[string]string{
					LabelDisplayName: "container-1",
				},
			},
		})
	})

	mux.HandleFunc("/v1.44/containers/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/json"):
			calls.inspectCalls.Add(1)

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
				Created: "2026-01-01T00:00:00Z",
				State: docker.ContainerState{
					Status:    "running",
					Running:   true,
					StartedAt: "2026-01-01T00:00:00Z",
				},
				Config: docker.ContainerConfig{
					Image: "nginx:latest",
					Labels: map[string]string{
						LabelDisplayName: "container-1",
					},
				},
				NetworkSettings: &docker.NetworkSettings{
					Networks: map[string]docker.NetworkEndpoint{},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/logs"):
			calls.logsCalls.Add(1)
			w.Header().Set("Content-Type", "application/octet-stream")

			payload := []byte("log line\n")
			header := make([]byte, 8)
			header[0] = 1
			binary.BigEndian.PutUint32(header[4:8], uint32(len(payload)))

			_, _ = w.Write(header)
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
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

	return client, calls, shutdown
}
