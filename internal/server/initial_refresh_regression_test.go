package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/adapter"
	"github.com/codeswhat/portwing/internal/adapter/drydock"
	"github.com/codeswhat/portwing/internal/config"
	"github.com/codeswhat/portwing/internal/docker"
)

type initialRefreshAdapter struct {
	mu        sync.Mutex
	refreshed bool
	notified  chan initialRefreshNotification
}

type initialRefreshNotification struct {
	added  []adapter.Container
	sender adapter.MessageSender
}

type canceledInitialRefreshAdapter struct {
	started  chan struct{}
	release  chan struct{}
	notified chan struct{}
}

type failedInitialRefreshAdapter struct {
	called   chan struct{}
	notified chan struct{}
}

func (a *failedInitialRefreshAdapter) Name() string           { return "failed-initial-refresh" }
func (a *failedInitialRefreshAdapter) Capabilities() []string { return nil }
func (a *failedInitialRefreshAdapter) HelloExtension() *adapter.HelloExtension {
	return nil
}
func (a *failedInitialRefreshAdapter) PollInterval() int { return 300 }
func (a *failedInitialRefreshAdapter) RegisterRoutes(*http.ServeMux, func(http.HandlerFunc) http.Handler) {
}
func (a *failedInitialRefreshAdapter) OnConnect(context.Context, adapter.MessageSender) error {
	return nil
}
func (a *failedInitialRefreshAdapter) RefreshContainers(context.Context) ([]adapter.Container, []adapter.Container, []adapter.Container, error) {
	close(a.called)
	return nil, nil, nil, context.DeadlineExceeded
}
func (a *failedInitialRefreshAdapter) OnContainerRefresh(context.Context, adapter.MessageSender, []adapter.Container, []adapter.Container, []adapter.Container) error {
	a.notified <- struct{}{}
	return nil
}

func (a *canceledInitialRefreshAdapter) Name() string           { return "canceled-initial-refresh" }
func (a *canceledInitialRefreshAdapter) Capabilities() []string { return nil }
func (a *canceledInitialRefreshAdapter) HelloExtension() *adapter.HelloExtension {
	return nil
}
func (a *canceledInitialRefreshAdapter) PollInterval() int { return 300 }
func (a *canceledInitialRefreshAdapter) RegisterRoutes(*http.ServeMux, func(http.HandlerFunc) http.Handler) {
}
func (a *canceledInitialRefreshAdapter) OnConnect(context.Context, adapter.MessageSender) error {
	return nil
}
func (a *canceledInitialRefreshAdapter) RefreshContainers(context.Context) ([]adapter.Container, []adapter.Container, []adapter.Container, error) {
	close(a.started)
	<-a.release
	return []adapter.Container{{ID: "stale-after-cancel"}}, nil, nil, nil
}
func (a *canceledInitialRefreshAdapter) OnContainerRefresh(context.Context, adapter.MessageSender, []adapter.Container, []adapter.Container, []adapter.Container) error {
	a.notified <- struct{}{}
	return nil
}

func (a *initialRefreshAdapter) Name() string                            { return "initial-refresh" }
func (a *initialRefreshAdapter) Capabilities() []string                  { return nil }
func (a *initialRefreshAdapter) HelloExtension() *adapter.HelloExtension { return nil }
func (a *initialRefreshAdapter) PollInterval() int                       { return 300 }
func (a *initialRefreshAdapter) RegisterRoutes(*http.ServeMux, func(http.HandlerFunc) http.Handler) {
}
func (a *initialRefreshAdapter) OnConnect(context.Context, adapter.MessageSender) error { return nil }
func (a *initialRefreshAdapter) RefreshContainers(context.Context) ([]adapter.Container, []adapter.Container, []adapter.Container, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.refreshed {
		return nil, nil, nil, nil
	}
	a.refreshed = true
	return []adapter.Container{{ID: "initial-container", Name: "initial"}}, nil, nil, nil
}
func (a *initialRefreshAdapter) OnContainerRefresh(_ context.Context, sender adapter.MessageSender, added, _, _ []adapter.Container) error {
	a.notified <- initialRefreshNotification{added: added, sender: sender}
	return nil
}

func TestPollContainersDeliversSuccessfulInitialDiffBeforeTicker(t *testing.T) {
	t.Parallel()

	a := &initialRefreshAdapter{notified: make(chan initialRefreshNotification, 1)}
	s := &Server{cfg: &config.Config{DDPollInterval: 300}, adapter: a}
	ctx, cancel := context.WithCancel(context.Background())
	pollDone := make(chan struct{})
	go func() {
		defer close(pollDone)
		s.pollContainers(ctx)
	}()

	select {
	case notification := <-a.notified:
		if notification.sender != nil {
			t.Fatalf("initial standard-mode sender = %T, want nil", notification.sender)
		}
		if len(notification.added) != 1 || notification.added[0].ID != "initial-container" {
			t.Fatalf("initial added diff = %+v", notification.added)
		}
	case <-time.After(time.Second):
		t.Fatal("initial refresh diff was not delivered before the 300-second ticker")
	}
	cancel()
	select {
	case <-pollDone:
	case <-time.After(time.Second):
		t.Fatal("pollContainers did not stop after cancellation")
	}
}

func TestPollContainersSkipsRefreshWhenAlreadyCanceled(t *testing.T) {
	t.Parallel()

	a := &initialRefreshAdapter{notified: make(chan initialRefreshNotification, 1)}
	s := &Server{cfg: &config.Config{DDPollInterval: 300}, adapter: a}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s.pollContainers(ctx)

	a.mu.Lock()
	refreshed := a.refreshed
	a.mu.Unlock()
	if refreshed {
		t.Fatal("pollContainers refreshed inventory after cancellation")
	}
	select {
	case notification := <-a.notified:
		t.Fatalf("pollContainers notified after cancellation: %+v", notification)
	default:
	}
}

func TestPollContainersDoesNotNotifyInitialDiffAfterCancellation(t *testing.T) {
	t.Parallel()

	a := &canceledInitialRefreshAdapter{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		notified: make(chan struct{}, 1),
	}
	s := &Server{cfg: &config.Config{DDPollInterval: 300}, adapter: a}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.pollContainers(ctx)
	}()

	select {
	case <-a.started:
	case <-time.After(time.Second):
		t.Fatal("initial refresh did not start")
	}
	cancel()
	close(a.release)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pollContainers did not stop after initial refresh cancellation")
	}
	select {
	case <-a.notified:
		t.Fatal("initial refresh notified the adapter after cancellation")
	default:
	}
}

func TestPollContainersDoesNotNotifyFailedInitialRefresh(t *testing.T) {
	t.Parallel()

	a := &failedInitialRefreshAdapter{
		called:   make(chan struct{}),
		notified: make(chan struct{}, 1),
	}
	s := &Server{cfg: &config.Config{DDPollInterval: 300}, adapter: a}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.pollContainers(ctx)
	}()

	select {
	case <-a.called:
	case <-time.After(time.Second):
		t.Fatal("initial refresh was not attempted")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pollContainers did not stop after failed initial refresh")
	}
	select {
	case <-a.notified:
		t.Fatal("failed initial refresh notified the adapter")
	default:
	}
}

func TestInitialDrydockRefreshCorrectsAlreadyConnectedEmptySnapshot(t *testing.T) {
	t.Parallel()

	sockPath, cleanupSocket := shortSocketPath(t)
	defer cleanupSocket()
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen on Docker socket: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(docker.VersionResponse{Version: "26.0.0", APIVersion: "1.44"})
	})
	mux.HandleFunc("/v1.44/containers/json", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]docker.ContainerJSON{{
			ID:      "initial-container",
			Names:   []string{"/initial"},
			Image:   "example/image:latest",
			ImageID: "sha256:initial",
			State:   "running",
			Status:  "Up 1 second",
		}})
	})
	mux.HandleFunc("/v1.44/containers/initial-container/json", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(docker.ContainerInspect{
			ID:      "initial-container",
			Name:    "/initial",
			State:   docker.ContainerState{Status: "running", Running: true},
			Config:  docker.ContainerConfig{Image: "example/image:latest"},
			Created: "2026-08-27T00:00:00Z",
		})
	})
	dockerServer := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	dockerDone := make(chan error, 1)
	go func() { dockerDone <- dockerServer.Serve(listener) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = dockerServer.Shutdown(ctx)
		_ = listener.Close()
		<-dockerDone
	}()

	dockerClient, err := docker.NewClient(sockPath, 5)
	if err != nil {
		t.Fatalf("docker.NewClient: %v", err)
	}
	drydockAdapter := drydock.NewAdapter(dockerClient, "startup-agent", drydock.AgentInfo{})
	eventsMux := http.NewServeMux()
	drydockAdapter.RegisterRoutes(eventsMux, func(handler http.HandlerFunc) http.Handler { return handler })
	eventsServer := httptest.NewServer(eventsMux)
	defer eventsServer.Close()

	eventsCtx, cancelEvents := context.WithCancel(context.Background())
	defer cancelEvents()
	req, err := http.NewRequestWithContext(eventsCtx, http.MethodGet, eventsServer.URL+"/api/events", nil)
	if err != nil {
		t.Fatalf("new SSE request: %v", err)
	}
	//nolint:bodyclose // The scanner captures resp.Body in a goroutine; the deferred close below owns it.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if resp != nil {
			_ = resp.Body.Close()
		}
		t.Fatalf("connect SSE client: %v", err)
	}
	defer resp.Body.Close()
	events := make(chan []byte, 8)
	scanDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				events <- []byte(strings.TrimPrefix(line, "data: "))
			}
		}
		scanDone <- scanner.Err()
	}()

	initial := nextWatcherSnapshot(t, events)
	if got := snapshotContainerCount(t, initial); got != 0 {
		t.Fatalf("initial watcher snapshot has %d containers, want empty startup state", got)
	}

	pollCtx, cancelPoll := context.WithCancel(context.Background())
	pollDone := make(chan struct{})
	s := &Server{cfg: &config.Config{DDPollInterval: 300}, adapter: drydockAdapter}
	go func() {
		defer close(pollDone)
		s.pollContainers(pollCtx)
	}()

	corrected := nextWatcherSnapshot(t, events)
	if got := snapshotContainerCount(t, corrected); got != 1 {
		t.Fatalf("corrected watcher snapshot has %d containers, want 1: %s", got, corrected)
	}
	cancelPoll()
	select {
	case <-pollDone:
	case <-time.After(time.Second):
		t.Fatal("pollContainers did not stop after cancellation")
	}
	cancelEvents()
	select {
	case <-scanDone:
	case <-time.After(time.Second):
		t.Fatal("SSE reader did not stop after cancellation")
	}
}

func nextWatcherSnapshot(t *testing.T, events <-chan []byte) []byte {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case event := <-events:
			var envelope struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(event, &envelope); err != nil {
				t.Fatalf("decode SSE event %q: %v", event, err)
			}
			if envelope.Type == "dd:watcher-snapshot" {
				return event
			}
		case <-deadline.C:
			t.Fatal("timed out waiting for watcher snapshot")
		}
	}
}

func snapshotContainerCount(t *testing.T, event []byte) int {
	t.Helper()
	var envelope struct {
		Data struct {
			Containers []json.RawMessage `json:"containers"`
		} `json:"data"`
	}
	if err := json.Unmarshal(event, &envelope); err != nil {
		t.Fatalf("decode watcher snapshot %q: %v", event, err)
	}
	return len(envelope.Data.Containers)
}
