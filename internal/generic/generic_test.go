package generic

import (
	"bufio"
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

	adapterpkg "github.com/codeswhat/lookout/internal/adapter"
	"github.com/codeswhat/lookout/internal/docker"
	"github.com/codeswhat/lookout/internal/protocol"
)

// shortSocketPath returns a temp socket path short enough for the unix socket
// path limit (104 bytes on darwin).
func shortSocketPath(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "lk")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	return filepath.Join(dir, "d.sock")
}

type testDockerCalls struct {
	listCalls    atomic.Int64
	inspectCalls atomic.Int64
	logsCalls    atomic.Int64
}

// newTestDockerClient creates a minimal stub Docker server and returns a
// docker.Client pointed at it, along with a call counter and shutdown func.
func newTestDockerClient(t *testing.T) (*docker.Client, *testDockerCalls, func()) {
	t.Helper()

	socketPath := shortSocketPath(t)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on unix socket: %v", err)
	}

	calls := &testDockerCalls{}

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
				Labels:  map[string]string{},
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
					Image:  "nginx:latest",
					Labels: map[string]string{},
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

func TestContainersRouteServesCachedInventory(t *testing.T) {
	t.Parallel()

	client, calls, shutdown := newTestDockerClient(t)
	defer shutdown()

	a := New(client, "test-agent")
	if _, err := a.containers.BuildInventory(context.Background()); err != nil {
		t.Fatalf("build inventory: %v", err)
	}

	baseList := calls.listCalls.Load()
	baseInspect := calls.inspectCalls.Load()
	if baseList == 0 || baseInspect == 0 {
		t.Fatalf("expected initial docker calls, got list=%d inspect=%d", baseList, baseInspect)
	}

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/containers", nil)
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
			t.Fatalf("unexpected container count: got %d want 1", len(containers))
		}
	}

	// No additional docker calls should have been made.
	if got := calls.listCalls.Load(); got != baseList {
		t.Fatalf("unexpected list calls after cache: got %d want %d", got, baseList)
	}
}

func TestTailValidationRejectsGarbage(t *testing.T) {
	t.Parallel()

	client, calls, shutdown := newTestDockerClient(t)
	defer shutdown()

	a := New(client, "test-agent")

	cases := []struct {
		name string
		tail string
	}{
		{"non-numeric", "abc"},
		{"zero", "0"},
		{"negative", "-1"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			before := calls.logsCalls.Load()

			req := httptest.NewRequest(http.MethodGet, "/api/v1/containers/c1/logs?tail="+tc.tail, nil)
			req.SetPathValue("id", "c1")
			rec := httptest.NewRecorder()

			a.handleContainerLogs(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d", rec.Code)
			}
			if calls.logsCalls.Load() != before {
				t.Fatal("docker logs should not have been called for invalid tail")
			}
		})
	}
}

func TestVersionRoute(t *testing.T) {
	t.Parallel()

	client, _, shutdown := newTestDockerClient(t)
	defer shutdown()

	a := New(client, "test-agent")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
	rec := httptest.NewRecorder()

	a.handleVersion(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}

	var resp versionResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode version response: %v", err)
	}
	if resp.AgentVersion != protocol.AgentVersion {
		t.Fatalf("agentVersion: got %q want %q", resp.AgentVersion, protocol.AgentVersion)
	}
	if resp.Adapter != "generic" {
		t.Fatalf("adapter: got %q want %q", resp.Adapter, "generic")
	}
	if resp.ProtocolName != protocol.ProtocolName {
		t.Fatalf("protocolName: got %q want %q", resp.ProtocolName, protocol.ProtocolName)
	}
}

// newTestDockerClientWithEvents creates a stub that additionally streams a
// single Docker event then closes the connection.
func newTestDockerClientWithEvents(t *testing.T) (*docker.Client, func()) {
	t.Helper()

	socketPath := shortSocketPath(t)
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
	mux.HandleFunc("/v1.44/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Write one container start event, then close.
		event := docker.DockerEvent{
			Type:   "container",
			Action: "start",
			Actor: docker.Actor{
				ID: "abc123",
				Attributes: map[string]string{
					"name":  "my-container",
					"image": "nginx:latest",
				},
			},
			Time: time.Now().Unix(),
		}
		_ = json.NewEncoder(w).Encode(event)
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

func TestEventSSEWritesWellFormedEvent(t *testing.T) {
	t.Parallel()

	client, shutdown := newTestDockerClientWithEvents(t)
	defer shutdown()

	b := NewEventBroadcaster(client)

	// Use a context that the test can cancel after receiving the event.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	// Run ServeHTTP in a goroutine; cancel once we've read at least one line.
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.ServeHTTP(rec, req)
	}()

	// Poll until the response body contains a "data:" line or the context expires.
	deadline := time.Now().Add(4 * time.Second)
	var dataLine string
	for time.Now().Before(deadline) {
		scanner := bufio.NewScanner(strings.NewReader(rec.Body.String()))
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data:") {
				dataLine = line
				cancel() // stop ServeHTTP
				break
			}
		}
		if dataLine != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	<-done

	if dataLine == "" {
		t.Fatal("expected at least one SSE data line")
	}

	// Strip "data: " prefix and parse JSON.
	payload := strings.TrimPrefix(dataLine, "data: ")
	var ge genericEvent
	if err := json.Unmarshal([]byte(payload), &ge); err != nil {
		t.Fatalf("unmarshal generic event: %v", err)
	}

	if ge.Action != "start" {
		t.Fatalf("action: got %q want %q", ge.Action, "start")
	}
	if ge.ContainerID != "abc123" {
		t.Fatalf("containerId: got %q want %q", ge.ContainerID, "abc123")
	}
	if ge.Name != "my-container" {
		t.Fatalf("name: got %q want %q", ge.Name, "my-container")
	}
	if ge.TS == "" {
		t.Fatal("expected ts to be set")
	}
}
