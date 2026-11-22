// SPDX-License-Identifier: MIT

package drydock

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/docker"
	"github.com/codeswhat/portwing/internal/protocol"
)

// TestLegacyContainerLogRequestOutlivesRequestTimeout runs a non-follow
// dd:container_log_request against a daemon that dribbles for about twice the
// client's one-second REQUEST_TIMEOUT. The full log must come back.
func TestLegacyContainerLogRequestOutlivesRequestTimeout(t *testing.T) {
	t.Parallel()

	const lines = 4
	socketPath := shortSocketPath(t)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on unix socket: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(docker.VersionResponse{Version: "26.0.0", APIVersion: "1.44"})
	})
	mux.HandleFunc("/v1.44/containers/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/logs") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.docker.multiplexed-stream")
		for i := range lines {
			_, _ = w.Write(routeTestDockerLogFrame(1, []byte("line "+strconv.Itoa(i)+"\n")))
			w.(http.Flusher).Flush()
			time.Sleep(550 * time.Millisecond)
		}
	})
	server := &http.Server{Handler: mux}
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = listener.Close()
		<-serverDone
	})

	client, err := docker.NewClient(socketPath, 1)
	if err != nil {
		t.Fatalf("new docker client: %v", err)
	}

	a := NewAdapter(client, "test-agent", AgentInfo{})
	sender := newSyncCaptureSender()
	start := time.Now()
	a.handleContainerLogRequest(context.Background(), sender, protocol.DDContainerLogRequestMessage{
		RequestID:   "req-slow",
		ContainerID: "slow-container",
	})

	if elapsed := time.Since(start); elapsed < 1500*time.Millisecond {
		t.Fatalf("read finished in %v, test did not run past the request timeout", elapsed)
	}
	resp, ok := sender.Data().(protocol.DDContainerLogResponseMessage)
	if !ok {
		t.Fatalf("response payload type = %T, want protocol.DDContainerLogResponseMessage", sender.Data())
	}
	if want := "line 0\nline 1\nline 2\nline 3\n"; resp.Logs != want {
		t.Fatalf("Logs = %q, want the full log %q", resp.Logs, want)
	}
}

// TestContainerLogStreamMidBodyFailureEndsWithError pins that a log stream cut
// off mid-frame finishes with dd:container_log_error and never with an "eof"
// dd:container_log_end.
func TestContainerLogStreamMidBodyFailureEndsWithError(t *testing.T) {
	t.Parallel()

	client, calls, shutdown := newRouteTestDockerClient(t)
	defer shutdown()
	calls.setLogsResponse("cut-container",
		append(routeTestDockerLogHeader(1, uint32(len("partial\n"))), []byte("part")...))

	a := NewAdapter(client, "test-agent", AgentInfo{})
	sender := newLogStreamTestSender()
	payload := json.RawMessage(`{"requestId":"stream-cut","containerId":"cut-container","stream":true}`)
	if !a.HandleMessage(context.Background(), sender, protocol.TypeDDContainerLogRequest, payload) {
		t.Fatal("expected streaming dd:container_log_request to be handled")
	}

	first := waitForLogStreamEvent(t, sender)
	if first.msgType != protocol.TypeDDContainerLogChunk {
		t.Fatalf("first message = %q, want the chunk read before the cut", first.msgType)
	}
	last := waitForLogStreamEvent(t, sender)
	if last.msgType != protocol.TypeDDContainerLogError {
		t.Fatalf("terminal message = %q (%s), want %q", last.msgType, last.data, protocol.TypeDDContainerLogError)
	}
}

// TestLegacyContainerLogRequestStallErrorsAndReleasesSlot runs a non-follow
// dd:container_log_request against a daemon that sends some bytes and goes
// quiet. The request must end in an error response, not a partial log, and
// give back the buffered-log semaphore slot.
func TestLegacyContainerLogRequestStallErrorsAndReleasesSlot(t *testing.T) {
	t.Parallel()

	socketPath := shortSocketPath(t)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on unix socket: %v", err)
	}
	handlerDone := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(docker.VersionResponse{Version: "26.0.0", APIVersion: "1.44"})
	})
	mux.HandleFunc("/v1.44/containers/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/logs") {
			http.NotFound(w, r)
			return
		}
		defer close(handlerDone)
		w.Header().Set("Content-Type", "application/vnd.docker.multiplexed-stream")
		_, _ = w.Write(routeTestDockerLogFrame(1, []byte("before the stall\n")))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	server := &http.Server{Handler: mux}
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = listener.Close()
		<-serverDone
	})

	client, err := docker.NewClient(socketPath, 1)
	if err != nil {
		t.Fatalf("new docker client: %v", err)
	}
	a := NewAdapter(client, "test-agent", AgentInfo{})
	sender := newSyncCaptureSender()
	payload := json.RawMessage(`{"requestId":"req-stall","containerId":"stall-container"}`)
	start := time.Now()
	if !a.HandleMessage(context.Background(), sender, protocol.TypeDDContainerLogRequest, payload) {
		t.Fatal("expected dd:container_log_request to be handled")
	}
	select {
	case <-sender.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("no response to a stalled log request")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("stalled read took %v, want about the 1s REQUEST_TIMEOUT", elapsed)
	}
	resp, ok := sender.Data().(protocol.DDContainerLogResponseMessage)
	if !ok {
		t.Fatalf("response payload type = %T, want protocol.DDContainerLogResponseMessage", sender.Data())
	}
	if !strings.HasPrefix(resp.Logs, "error: reading container logs") {
		t.Fatalf("Logs = %q, want a read error, not the partial log", resp.Logs)
	}

	sem := a.getLegacyLogSemaphore()
	deadline := time.Now().Add(2 * time.Second)
	for len(sem) != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(sem) != 0 {
		t.Fatal("buffered-log semaphore slot was not released")
	}
	select {
	case <-handlerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("daemon connection was not released after the stall")
	}
}
