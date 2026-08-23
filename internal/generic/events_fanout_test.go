package generic

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/docker"
)

// countedEventCalls tracks how many times the stub /events endpoint below was
// dialed and how many of those connections are currently open, so a test can
// assert on the number of upstream Docker connections a broadcaster holds
// regardless of how many SSE clients it is serving.
type countedEventCalls struct {
	connectCount      atomic.Int64
	activeConnections atomic.Int64
}

// newTestDockerClientWithCountedEvents creates a stub whose /events endpoint
// counts connections and streams one event every 10ms until the caller
// disconnects, instead of sending a single event and closing. That keeps an
// upstream connection open for as long as the broadcaster holds it, so the
// test can observe how many connections exist at once.
func newTestDockerClientWithCountedEvents(t *testing.T) (*docker.Client, *countedEventCalls, func()) {
	t.Helper()

	socketPath := shortSocketPath(t)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on unix socket: %v", err)
	}

	calls := &countedEventCalls{}

	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(docker.VersionResponse{Version: "26.0.0", APIVersion: "1.44"})
	})
	mux.HandleFunc("/v1.44/events", func(w http.ResponseWriter, r *http.Request) {
		calls.connectCount.Add(1)
		calls.activeConnections.Add(1)
		defer calls.activeConnections.Add(-1)

		w.Header().Set("Content-Type", "application/json")
		flusher, _ := w.(http.Flusher)

		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				event := docker.DockerEvent{
					Type:   "container",
					Action: "start",
					Actor: docker.Actor{
						ID:         "shared123",
						Attributes: map[string]string{"name": "shared-container", "image": "nginx:latest"},
					},
					Time: time.Now().Unix(),
				}
				if err := json.NewEncoder(w).Encode(event); err != nil {
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
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

// waitForSSEDataLine polls rec's body until it contains a "data:" line (an
// event, not the ": heartbeat" comment line) or the deadline passes.
func waitForSSEDataLine(t *testing.T, rec *syncRecorder, deadline time.Time) string {
	t.Helper()

	for time.Now().Before(deadline) {
		scanner := bufio.NewScanner(strings.NewReader(rec.BodyString()))
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data:") {
				return line
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for SSE data line")
	return ""
}

// TestEventBroadcasterSharesOneUpstreamAcrossClients is the regression test
// for the dead fan-out registry: EventBroadcaster.clients was written to but
// never read from, so ServeHTTP opened a brand new docker.NewEventStream
// subscription per connecting client instead of sharing one. This asserts
// the fix's contract directly — two concurrent SSE clients produce exactly
// one upstream /events connection, both receive events, and the upstream
// connection closes once both clients are gone.
func TestEventBroadcasterSharesOneUpstreamAcrossClients(t *testing.T) {
	client, calls, shutdown := newTestDockerClientWithCountedEvents(t)
	defer shutdown()

	b := NewEventBroadcaster(client)

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel1()
	defer cancel2()

	req1 := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil).WithContext(ctx1)
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil).WithContext(ctx2)
	rec1 := &syncRecorder{rec: httptest.NewRecorder()}
	rec2 := &syncRecorder{rec: httptest.NewRecorder()}

	done1 := make(chan struct{})
	go func() {
		defer close(done1)
		b.ServeHTTP(rec1, req1)
	}()

	// Wait for the first client to actually be receiving events (and so the
	// upstream subscription to be up) before the second joins, so this
	// deterministically exercises "join while already subscribed" rather
	// than a race between two simultaneous first joins.
	deadline := time.Now().Add(5 * time.Second)
	waitForSSEDataLine(t, rec1, deadline)

	done2 := make(chan struct{})
	go func() {
		defer close(done2)
		b.ServeHTTP(rec2, req2)
	}()
	waitForSSEDataLine(t, rec2, deadline)

	if got := calls.connectCount.Load(); got != 1 {
		t.Fatalf("upstream /events connections opened = %d, want exactly 1 for two concurrent SSE clients", got)
	}
	if got := calls.activeConnections.Load(); got != 1 {
		t.Fatalf("upstream /events active connections = %d, want exactly 1", got)
	}

	cancel1()
	<-done1

	// The second client is still connected, so the shared subscription must
	// still be alive.
	if got := calls.activeConnections.Load(); got != 1 {
		t.Fatalf("upstream connection closed after only one of two clients disconnected (active=%d)", got)
	}

	cancel2()
	<-done2

	// Both clients are gone; the shared subscription must wind down.
	closeDeadline := time.Now().Add(5 * time.Second)
	for calls.activeConnections.Load() != 0 {
		if time.Now().After(closeDeadline) {
			t.Fatalf("upstream /events connection still open after both SSE clients disconnected")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// No further connection attempts should occur once idle.
	if got := calls.connectCount.Load(); got != 1 {
		t.Fatalf("upstream /events connections opened = %d, want still exactly 1 after both clients left", got)
	}
}
