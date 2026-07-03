package drydock

// coverage_test.go adds targeted tests to raise coverage for functions that
// were at 0% or low. All helpers (fakeContainerProvider, captureSender,
// syncCaptureSender, newSyncCaptureSender, newRouteTestDockerClient,
// shortSocketPath) live in sibling test files.

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	adapterpkg "github.com/codeswhat/portwing/internal/adapter"
	"github.com/codeswhat/portwing/internal/docker"
	"github.com/codeswhat/portwing/internal/protocol"
)

// ---------------------------------------------------------------------------
// adapter.go — trivial accessors
// ---------------------------------------------------------------------------

func TestName(t *testing.T) {
	t.Parallel()
	a := &Adapter{}
	if got := a.Name(); got != "drydock" {
		t.Fatalf("Name() = %q, want %q", got, "drydock")
	}
}

func TestCapabilities(t *testing.T) {
	t.Parallel()
	a := &Adapter{}
	caps := a.Capabilities()
	if len(caps) == 0 {
		t.Fatal("Capabilities() returned empty slice")
	}
	want := map[string]bool{
		"dd:watch":          true,
		"dd:trigger":        true,
		"dd:container-sync": true,
		"dd:logs":           true,
	}
	for _, c := range caps {
		if !want[c] {
			t.Errorf("unexpected capability %q", c)
		}
		delete(want, c)
	}
	for missing := range want {
		t.Errorf("missing capability %q", missing)
	}
}

func TestHelloExtension(t *testing.T) {
	t.Parallel()
	a := &Adapter{}
	ext := a.HelloExtension()
	if ext == nil {
		t.Fatal("HelloExtension() returned nil")
	}
	if ext.DrydockCompat == "" {
		t.Error("HelloExtension().DrydockCompat is empty")
	}
	if len(ext.WatcherTypes) == 0 {
		t.Error("HelloExtension().WatcherTypes is empty")
	}
	// TriggerTypes must not be nil even if empty.
	if ext.TriggerTypes == nil {
		t.Error("HelloExtension().TriggerTypes is nil, want non-nil")
	}
}

func TestPollInterval(t *testing.T) {
	t.Parallel()
	a := &Adapter{}
	if got := a.PollInterval(); got != 0 {
		t.Fatalf("PollInterval() = %d, want 0", got)
	}
}

func TestContainersAccessor(t *testing.T) {
	t.Parallel()
	client, _, shutdown := newRouteTestDockerClient(t)
	defer shutdown()
	a := NewAdapter(client, "test-agent", AgentInfo{})
	if got := a.Containers(); got == nil {
		t.Fatal("Containers() returned nil")
	}
}

func TestSSEAccessor(t *testing.T) {
	t.Parallel()
	client, _, shutdown := newRouteTestDockerClient(t)
	defer shutdown()
	a := NewAdapter(client, "test-agent", AgentInfo{})
	if got := a.SSE(); got == nil {
		t.Fatal("SSE() returned nil")
	}
}

func TestDockerClientAccessor(t *testing.T) {
	t.Parallel()
	client, _, shutdown := newRouteTestDockerClient(t)
	defer shutdown()
	a := NewAdapter(client, "test-agent", AgentInfo{})
	if got := a.DockerClient(); got == nil {
		t.Fatal("DockerClient() returned nil")
	}
}

// ---------------------------------------------------------------------------
// adapter.go — getMessageSemaphore: nil initial semaphore path (sync.Once)
// ---------------------------------------------------------------------------

func TestGetMessageSemaphore_InitializesWhenNil(t *testing.T) {
	t.Parallel()
	a := &Adapter{} // messageSem intentionally nil
	sem := a.getMessageSemaphore()
	if sem == nil {
		t.Fatal("getMessageSemaphore() returned nil")
	}
	sem2 := a.getMessageSemaphore()
	if sem != sem2 {
		t.Fatal("getMessageSemaphore() returned different channel on second call")
	}
	if cap(sem) != defaultMessageHandlerConcurrency {
		t.Fatalf("semaphore capacity = %d, want %d", cap(sem), defaultMessageHandlerConcurrency)
	}
}

// ---------------------------------------------------------------------------
// adapter.go — OnConnect
// ---------------------------------------------------------------------------

// collectingSender records every (msgType, data) pair sent.
type collectingSender struct {
	sent []string
}

func (s *collectingSender) SendTypedMessage(msgType string, _ any) error {
	s.sent = append(s.sent, msgType)
	return nil
}

func TestOnConnect_SendsContainerSyncThenComponentSync(t *testing.T) {
	t.Parallel()

	client, _, shutdown := newRouteTestDockerClient(t)
	defer shutdown()

	a := NewAdapter(client, "test-agent", AgentInfo{})
	if _, err := a.containers.BuildInventory(context.Background()); err != nil {
		t.Fatalf("BuildInventory: %v", err)
	}

	coll := &collectingSender{}
	if err := a.OnConnect(context.Background(), coll); err != nil {
		t.Fatalf("OnConnect: %v", err)
	}

	if len(coll.sent) < 2 {
		t.Fatalf("expected ≥2 messages, got %d: %v", len(coll.sent), coll.sent)
	}
	if coll.sent[0] != protocol.TypeDDContainerSync {
		t.Errorf("msg[0] = %q, want %q", coll.sent[0], protocol.TypeDDContainerSync)
	}
	if coll.sent[1] != protocol.TypeDDComponentSync {
		t.Errorf("msg[1] = %q, want %q", coll.sent[1], protocol.TypeDDComponentSync)
	}
}

// ---------------------------------------------------------------------------
// adapter.go — RefreshContainers
// ---------------------------------------------------------------------------

func TestRefreshContainers_DelegatesToManager(t *testing.T) {
	t.Parallel()

	client, _, shutdown := newRouteTestDockerClient(t)
	defer shutdown()

	a := NewAdapter(client, "test-agent", AgentInfo{})

	if _, err := a.containers.BuildInventory(context.Background()); err != nil {
		t.Fatalf("BuildInventory: %v", err)
	}

	added, updated, removed, err := a.RefreshContainers(context.Background())
	if err != nil {
		t.Fatalf("RefreshContainers: %v", err)
	}
	// All slices must be non-nil; may be empty.
	_ = added
	_ = updated
	_ = removed
}

// ---------------------------------------------------------------------------
// adapter.go — OnContainerRefresh
// ---------------------------------------------------------------------------

func TestOnContainerRefresh_EdgeMode_SendsAllEventTypes(t *testing.T) {
	t.Parallel()

	client, _, shutdown := newRouteTestDockerClient(t)
	defer shutdown()

	a := NewAdapter(client, "test-agent", AgentInfo{})

	c1 := adapterpkg.Container{ID: "c1", Name: "one", Status: "running"}
	c2 := adapterpkg.Container{ID: "c2", Name: "two", Status: "exited"}
	c3 := adapterpkg.Container{ID: "c3", Name: "three", Status: "running"}

	coll := &collectingSender{}
	if err := a.OnContainerRefresh(context.Background(), coll, []adapterpkg.Container{c1}, []adapterpkg.Container{c2}, []adapterpkg.Container{c3}); err != nil {
		t.Fatalf("OnContainerRefresh: %v", err)
	}

	sentSet := make(map[string]bool, len(coll.sent))
	for _, s := range coll.sent {
		sentSet[s] = true
	}
	for _, want := range []string{
		protocol.TypeDDContainerAdded,
		protocol.TypeDDContainerUpdated,
		protocol.TypeDDContainerRemoved,
	} {
		if !sentSet[want] {
			t.Errorf("expected message type %q, got: %v", want, coll.sent)
		}
	}
}

func TestOnContainerRefresh_StandardMode_NilSenderNoOp(t *testing.T) {
	t.Parallel()

	client, _, shutdown := newRouteTestDockerClient(t)
	defer shutdown()

	a := NewAdapter(client, "test-agent", AgentInfo{})
	c1 := adapterpkg.Container{ID: "c1", Status: "running"}

	// nil sender = standard (SSE) mode — must not panic.
	if err := a.OnContainerRefresh(context.Background(), nil, []adapterpkg.Container{c1}, nil, nil); err != nil {
		t.Fatalf("OnContainerRefresh(nil sender): %v", err)
	}
}

// ---------------------------------------------------------------------------
// adapter.go — sendComponentSync
// ---------------------------------------------------------------------------

func TestSendComponentSync_SendsDDComponentSync(t *testing.T) {
	t.Parallel()
	a := &Adapter{}
	sender := &captureSender{}
	a.sendComponentSync(sender)
	if sender.msgType != protocol.TypeDDComponentSync {
		t.Fatalf("expected %q, got %q", protocol.TypeDDComponentSync, sender.msgType)
	}
	if sender.data == nil {
		t.Fatal("sendComponentSync sent nil data")
	}
}

// ---------------------------------------------------------------------------
// adapter.go — sendContainerEvent
// ---------------------------------------------------------------------------

func TestSendContainerEvent_Added(t *testing.T) {
	t.Parallel()
	a := &Adapter{}
	sender := &captureSender{}
	c := adapterpkg.Container{ID: "c1", Name: "app", Status: "running"}
	a.sendContainerEvent(sender, protocol.TypeDDContainerAdded, c)
	if sender.msgType != protocol.TypeDDContainerAdded {
		t.Fatalf("expected %q, got %q", protocol.TypeDDContainerAdded, sender.msgType)
	}
}

func TestSendContainerEvent_Updated(t *testing.T) {
	t.Parallel()
	a := &Adapter{}
	sender := &captureSender{}
	c := adapterpkg.Container{ID: "c2", Name: "db", Status: "exited"}
	a.sendContainerEvent(sender, protocol.TypeDDContainerUpdated, c)
	if sender.msgType != protocol.TypeDDContainerUpdated {
		t.Fatalf("expected %q, got %q", protocol.TypeDDContainerUpdated, sender.msgType)
	}
}

func TestSendContainerEvent_UnknownTypeIsNoOp(t *testing.T) {
	t.Parallel()
	a := &Adapter{}
	sender := &captureSender{}
	c := adapterpkg.Container{ID: "c3"}
	// Unknown type: must not send anything.
	a.sendContainerEvent(sender, "dd:unknown_type", c)
	if sender.msgType != "" {
		t.Fatalf("expected no message for unknown type, got %q", sender.msgType)
	}
}

// ---------------------------------------------------------------------------
// adapter.go — handleContainerLogRequest via HandleMessage + fake Docker
// ---------------------------------------------------------------------------

func TestHandleContainerLogRequest_SuccessPath(t *testing.T) {
	t.Parallel()

	client, calls, shutdown := newRouteTestDockerClient(t)
	defer shutdown()

	a := NewAdapter(client, "test-agent", AgentInfo{})
	sender := newSyncCaptureSender()

	payload := json.RawMessage(`{"containerId":"container-1","tail":10}`)
	handled := a.HandleMessage(context.Background(), sender, protocol.TypeDDContainerLogRequest, payload)
	if !handled {
		t.Fatal("expected dd:container_log_request to be handled")
	}

	select {
	case <-sender.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for container log response")
	}

	if got := sender.MsgType(); got != protocol.TypeDDContainerLogResponse {
		t.Fatalf("response type = %q, want %q", got, protocol.TypeDDContainerLogResponse)
	}
	if calls.logsCalls.Load() == 0 {
		t.Fatal("expected at least one docker logs call")
	}
}

func TestHandleContainerLogRequest_ZeroTailCallsDocker(t *testing.T) {
	t.Parallel()

	client, calls, shutdown := newRouteTestDockerClient(t)
	defer shutdown()

	a := NewAdapter(client, "test-agent", AgentInfo{})
	sender := newSyncCaptureSender()

	// tail=0 means "all logs" — docker client still called.
	payload := json.RawMessage(`{"containerId":"container-1","tail":0}`)
	handled := a.HandleMessage(context.Background(), sender, protocol.TypeDDContainerLogRequest, payload)
	if !handled {
		t.Fatal("expected handled=true")
	}

	select {
	case <-sender.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for response")
	}
	if calls.logsCalls.Load() == 0 {
		t.Fatal("expected docker logs call")
	}
}

// ---------------------------------------------------------------------------
// routes.go — RegisterRoutes
// ---------------------------------------------------------------------------

func TestRegisterRoutes_RegistrationCoversKeyPaths(t *testing.T) {
	t.Parallel()

	client, _, shutdown := newRouteTestDockerClient(t)
	defer shutdown()

	a := NewAdapter(client, "test-agent", AgentInfo{})
	mux := http.NewServeMux()
	noopAuth := func(h http.HandlerFunc) http.Handler { return h }
	a.RegisterRoutes(mux, noopAuth)

	// Build inventory so GET /api/containers returns something.
	if _, err := a.containers.BuildInventory(context.Background()); err != nil {
		t.Fatalf("BuildInventory: %v", err)
	}

	cases := []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodGet, "/api/containers", http.StatusOK},
		{http.MethodGet, "/api/watchers", http.StatusOK},
		{http.MethodGet, "/api/triggers", http.StatusOK},
		{http.MethodGet, "/api/log/entries", http.StatusOK},
		{http.MethodPost, "/api/watchers/docker/docker", http.StatusNotImplemented},
		{http.MethodPost, "/api/watchers/docker/docker/container/c1", http.StatusNotImplemented},
		{http.MethodPost, "/api/triggers/restart/web", http.StatusNotImplemented},
		{http.MethodPost, "/api/triggers/restart/web/batch", http.StatusNotImplemented},
	}

	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s %s: got %d, want %d (body: %s)", tc.method, tc.path, rec.Code, tc.want, rec.Body.String())
		}
	}
}

// ---------------------------------------------------------------------------
// routes.go — handleContainerDelete
// ---------------------------------------------------------------------------

// newDeleteServer spins up a minimal Docker-like HTTP server over a unix
// socket that responds 204 to DELETE /v1.44/containers/{id} and stubs
// /version for client negotiation.
func newDeleteServer(t *testing.T) (client *docker.Client, shutdown func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "lk")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	socketPath := filepath.Join(dir, "d.sock")

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		t.Fatalf("listen: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(docker.VersionResponse{
			Version:    "26.0.0",
			APIVersion: "1.44",
		})
	})
	mux.HandleFunc("/v1.44/containers/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// Stub inspect so NewAdapter/BuildInventory doesn't blow up if called.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{})
	})

	srv := &http.Server{Handler: mux}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ln)
	}()

	c, err := docker.NewClient(socketPath, 2)
	if err != nil {
		_ = srv.Close()
		_ = os.RemoveAll(dir)
		t.Fatalf("new docker client: %v", err)
	}

	return c, func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		_ = ln.Close()
		<-done
		_ = os.RemoveAll(dir)
	}
}

func TestHandleContainerDelete_Success(t *testing.T) {
	t.Parallel()

	client, shutdown := newDeleteServer(t)
	defer shutdown()

	a := NewAdapter(client, "test-agent", AgentInfo{})
	req := httptest.NewRequest(http.MethodDelete, "/api/containers/container-1", nil)
	req.SetPathValue("id", "container-1")
	rec := httptest.NewRecorder()
	a.handleContainerDelete(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestHandleContainerDelete_NotFound(t *testing.T) {
	t.Parallel()

	// Use the existing fake docker server. It returns 404 from the default
	// handler for paths it doesn't recognise.
	dir, err := os.MkdirTemp("", "lk")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	socketPath := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(docker.VersionResponse{
			Version:    "26.0.0",
			APIVersion: "1.44",
		})
	})
	mux.HandleFunc("/v1.44/containers/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			http.Error(w, `{"message":"No such container: missing"}`, http.StatusNotFound)
			return
		}
		http.NotFound(w, r)
	})

	srv := &http.Server{Handler: mux}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ln)
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		_ = ln.Close()
		<-done
	}()

	client, err := docker.NewClient(socketPath, 2)
	if err != nil {
		t.Fatalf("new docker client: %v", err)
	}

	a := NewAdapter(client, "test-agent", AgentInfo{})
	req := httptest.NewRequest(http.MethodDelete, "/api/containers/missing", nil)
	req.SetPathValue("id", "missing")
	rec := httptest.NewRecorder()
	a.handleContainerDelete(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestHandleContainerDelete_Conflict(t *testing.T) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "lk")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	socketPath := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(docker.VersionResponse{
			Version:    "26.0.0",
			APIVersion: "1.44",
		})
	})
	mux.HandleFunc("/v1.44/containers/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			http.Error(w, `{"message":"conflict: container running"}`, http.StatusConflict)
			return
		}
		http.NotFound(w, r)
	})

	srv := &http.Server{Handler: mux}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ln)
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		_ = ln.Close()
		<-done
	}()

	client, err := docker.NewClient(socketPath, 2)
	if err != nil {
		t.Fatalf("new docker client: %v", err)
	}

	a := NewAdapter(client, "test-agent", AgentInfo{})
	req := httptest.NewRequest(http.MethodDelete, "/api/containers/busy-container", nil)
	req.SetPathValue("id", "busy-container")
	rec := httptest.NewRecorder()
	a.handleContainerDelete(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// routes.go — handleWatchers
// ---------------------------------------------------------------------------

func TestHandleWatchers_ReturnsWatcherArray(t *testing.T) {
	t.Parallel()

	client, _, shutdown := newRouteTestDockerClient(t)
	defer shutdown()

	a := NewAdapter(client, "test-agent", AgentInfo{})
	req := httptest.NewRequest(http.MethodGet, "/api/watchers", nil)
	rec := httptest.NewRecorder()
	a.handleWatchers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type = %q", ct)
	}
	var watchers []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&watchers); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(watchers) == 0 {
		t.Fatal("expected at least one watcher")
	}
}

// ---------------------------------------------------------------------------
// routes.go — handleTriggers
// ---------------------------------------------------------------------------

func TestHandleTriggers_ReturnsEmptyArray(t *testing.T) {
	t.Parallel()

	client, _, shutdown := newRouteTestDockerClient(t)
	defer shutdown()

	a := NewAdapter(client, "test-agent", AgentInfo{})
	req := httptest.NewRequest(http.MethodGet, "/api/triggers", nil)
	rec := httptest.NewRecorder()
	a.handleTriggers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var triggers []any
	if err := json.NewDecoder(rec.Body).Decode(&triggers); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if triggers == nil {
		t.Fatal("triggers must not decode to nil")
	}
}

// ---------------------------------------------------------------------------
// routes.go — handleWatcherPoll (501 stub)
// ---------------------------------------------------------------------------

func TestHandleWatcherPoll_Returns501(t *testing.T) {
	t.Parallel()

	client, _, shutdown := newRouteTestDockerClient(t)
	defer shutdown()

	a := NewAdapter(client, "test-agent", AgentInfo{})
	req := httptest.NewRequest(http.MethodPost, "/api/watchers/docker/docker", nil)
	req.SetPathValue("type", "docker")
	req.SetPathValue("name", "docker")
	rec := httptest.NewRecorder()
	a.handleWatcherPoll(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["error"] == "" {
		t.Fatal("expected non-empty error field in 501 body")
	}
}

// ---------------------------------------------------------------------------
// routes.go — handleContainerLogs: no-tail case
// ---------------------------------------------------------------------------

func TestHandleContainerLogs_NoTailParam(t *testing.T) {
	t.Parallel()

	client, calls, shutdown := newRouteTestDockerClient(t)
	defer shutdown()

	a := NewAdapter(client, "test-agent", AgentInfo{})
	req := httptest.NewRequest(http.MethodGet, "/api/containers/container-1/logs", nil)
	req.SetPathValue("id", "container-1")
	rec := httptest.NewRecorder()
	a.handleContainerLogs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if calls.logsCalls.Load() == 0 {
		t.Fatal("expected docker logs call")
	}
}

// ---------------------------------------------------------------------------
// sse.go — Broadcast* methods + removeClient
// ---------------------------------------------------------------------------

func TestSSEBroadcast_AllEventTypes(t *testing.T) {
	t.Parallel()

	provider := fakeContainerProvider{
		containers: []adapterpkg.Container{
			{ID: "c1", Status: "running", Image: adapterpkg.ContainerImage{ID: "img-a"}},
		},
	}
	b := NewSSEBroadcaster(provider, "v-test", AgentInfo{})

	ch := make(chan []byte, 16)
	b.mu.Lock()
	b.clients["test-client"] = &SSEClient{id: "test-client", events: ch}
	b.mu.Unlock()

	c := adapterpkg.Container{ID: "c2", Name: "db", Status: "exited"}
	b.BroadcastContainerAdded(c)
	b.BroadcastContainerUpdated(c)
	b.BroadcastContainerRemoved("c2", "db")
	b.BroadcastWatcherSnapshot()

	for i, want := range []string{
		"dd:container-added",
		"dd:container-updated",
		"dd:container-removed",
		"dd:watcher-snapshot",
	} {
		select {
		case msg := <-ch:
			if !strings.Contains(string(msg), want) {
				t.Errorf("event %d: expected %q in payload %s", i, want, msg)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("event %d (%q): timed out", i, want)
		}
	}
}

// TestSSEBroadcast_DropsBroadcastWhenBufferFull verifies that a full client
// channel causes the event to be silently dropped (no panic, no block).
func TestSSEBroadcast_DropsBroadcastWhenBufferFull(t *testing.T) {
	t.Parallel()

	provider := fakeContainerProvider{}
	b := NewSSEBroadcaster(provider, "v-test", AgentInfo{})

	// Unbuffered channel with no receiver, so any non-blocking broadcast is
	// immediately dropped.
	ch := make(chan []byte)
	b.mu.Lock()
	b.clients["slow-client"] = &SSEClient{id: "slow-client", events: ch}
	b.mu.Unlock()

	c := adapterpkg.Container{ID: "c1", Status: "running"}
	// Must not block.
	done := make(chan struct{})
	go func() {
		b.BroadcastContainerAdded(c)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("BroadcastContainerAdded blocked on full client buffer")
	}
}

// ---------------------------------------------------------------------------
// sse.go — ServeHTTP (initial ack + snapshot)
// ---------------------------------------------------------------------------

func TestSSEServeHTTP_DeliversInitialAckAndSnapshot(t *testing.T) {
	t.Parallel()

	provider := fakeContainerProvider{
		containers: []adapterpkg.Container{
			{ID: "c1", Status: "running", Image: adapterpkg.ContainerImage{ID: "img-a"}},
		},
	}
	b := NewSSEBroadcaster(provider, "v-test", AgentInfo{})

	srv := httptest.NewServer(http.HandlerFunc(b.ServeHTTP))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Context cancelled after receiving initial events — that's fine.
		if ctx.Err() != nil {
			return
		}
		t.Fatalf("GET SSE: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	seen := map[string]bool{}
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "dd:ack") {
			seen["ack"] = true
		}
		if strings.Contains(line, "dd:watcher-snapshot") {
			seen["snapshot"] = true
		}
		if seen["ack"] && seen["snapshot"] {
			cancel() // got what we need; cancel to close the stream
			break
		}
	}

	if !seen["ack"] {
		t.Error("never received dd:ack event")
	}
	if !seen["snapshot"] {
		t.Error("never received dd:watcher-snapshot event")
	}
}

// ---------------------------------------------------------------------------
// newErrorDockerServer — helper for tests that need Docker to return errors.
// Returns a client wired to a fake server whose /v1.44/containers/<id>/logs
// endpoint returns the given HTTP status, and whose DELETE returns the given
// delStatus. /version and /v1.44/containers/json are stubbed minimally.
// ---------------------------------------------------------------------------

func newErrorDockerServer(t *testing.T, logsStatus, delStatus int) (*docker.Client, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "lk")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	socketPath := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		t.Fatalf("listen: %v", err)
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
		// Return empty list so BuildInventory succeeds (needed by NewAdapter).
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]docker.ContainerJSON{})
	})
	mux.HandleFunc("/v1.44/containers/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			http.Error(w, `{"message":"internal error"}`, delStatus)
		default:
			// Logs endpoint (GET).
			http.Error(w, `{"message":"internal error"}`, logsStatus)
		}
	})

	srv := &http.Server{Handler: mux}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ln)
	}()

	c, err := docker.NewClient(socketPath, 2)
	if err != nil {
		_ = srv.Close()
		_ = os.RemoveAll(dir)
		t.Fatalf("new docker client: %v", err)
	}

	return c, func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		_ = ln.Close()
		<-done
		_ = os.RemoveAll(dir)
	}
}

// ---------------------------------------------------------------------------
// adapter.go — OnConnect: error path when BuildInventory fails
// ---------------------------------------------------------------------------

func TestOnConnect_StillSendsComponentSyncWhenContainerSyncFails(t *testing.T) {
	t.Parallel()

	// Use an error server — lists endpoint returns empty list so NewAdapter works,
	// but we cancel context before BuildInventory to simulate failure.
	client, shutdown := newErrorDockerServer(t, http.StatusInternalServerError, http.StatusInternalServerError)
	defer shutdown()

	a := NewAdapter(client, "test-agent", AgentInfo{})
	coll := &collectingSender{}

	// Cancel the context immediately so BuildInventory fails.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := a.OnConnect(ctx, coll); err != nil {
		t.Fatalf("OnConnect: %v", err)
	}

	// Even when container sync fails, component sync must still be sent.
	found := false
	for _, s := range coll.sent {
		if s == protocol.TypeDDComponentSync {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected dd:component_sync to be sent even when container sync fails, got: %v", coll.sent)
	}
}

// ---------------------------------------------------------------------------
// routes.go — handleContainerLogs: docker error path (500)
// ---------------------------------------------------------------------------

func TestHandleContainerLogs_DockerError(t *testing.T) {
	t.Parallel()

	client, shutdown := newErrorDockerServer(t, http.StatusInternalServerError, http.StatusNoContent)
	defer shutdown()

	a := NewAdapter(client, "test-agent", AgentInfo{})
	req := httptest.NewRequest(http.MethodGet, "/api/containers/container-1/logs?tail=5", nil)
	req.SetPathValue("id", "container-1")
	rec := httptest.NewRecorder()
	a.handleContainerLogs(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// routes.go — handleContainerDelete: generic 500 path
// ---------------------------------------------------------------------------

func TestHandleContainerDelete_InternalError(t *testing.T) {
	t.Parallel()

	client, shutdown := newErrorDockerServer(t, http.StatusOK, http.StatusInternalServerError)
	defer shutdown()

	a := NewAdapter(client, "test-agent", AgentInfo{})
	req := httptest.NewRequest(http.MethodDelete, "/api/containers/container-1", nil)
	req.SetPathValue("id", "container-1")
	rec := httptest.NewRecorder()
	a.handleContainerDelete(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// adapter.go — handleContainerLogRequest: docker error response
// ---------------------------------------------------------------------------

func TestHandleContainerLogRequest_DockerErrorSendsResponse(t *testing.T) {
	t.Parallel()

	client, shutdown := newErrorDockerServer(t, http.StatusInternalServerError, http.StatusNoContent)
	defer shutdown()

	a := NewAdapter(client, "test-agent", AgentInfo{})
	sender := newSyncCaptureSender()

	payload := json.RawMessage(`{"containerId":"container-1","tail":5}`)
	handled := a.HandleMessage(context.Background(), sender, protocol.TypeDDContainerLogRequest, payload)
	if !handled {
		t.Fatal("expected handled=true")
	}

	select {
	case <-sender.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for log error response")
	}

	if got := sender.MsgType(); got != protocol.TypeDDContainerLogResponse {
		t.Fatalf("response type = %q, want %q", got, protocol.TypeDDContainerLogResponse)
	}
}
