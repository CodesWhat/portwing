package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ---- NewEventStream ----

func TestNewEventStream_DefaultDelays(t *testing.T) {
	t.Parallel()

	c := &Client{apiVersion: "v1.44"}
	es := NewEventStream(c)
	if es.client != c {
		t.Fatal("NewEventStream: client not set")
	}
	if es.initialDelay != 5*time.Second {
		t.Fatalf("initialDelay = %v, want %v", es.initialDelay, 5*time.Second)
	}
	if es.maxDelay != 60*time.Second {
		t.Fatalf("maxDelay = %v, want %v", es.maxDelay, 60*time.Second)
	}
}

// ---- allowedActions ----

func TestAllowedActions_ContainsExpected(t *testing.T) {
	t.Parallel()

	expected := []string{
		"create", "start", "stop", "die", "kill",
		"restart", "pause", "unpause", "destroy",
		"rename", "update", "oom", "health_status",
	}
	for _, action := range expected {
		if !allowedActions[action] {
			t.Errorf("allowedActions[%q] = false, want true", action)
		}
	}

	notAllowed := []string{"exec_create", "exec_start", "attach", "commit"}
	for _, action := range notAllowed {
		if allowedActions[action] {
			t.Errorf("allowedActions[%q] = true, want false", action)
		}
	}
}

// ---- Subscribe: events are received and filtered ----

func TestEventStream_Subscribe_ReceivesFilteredEvents(t *testing.T) {
	t.Parallel()

	// Events to serve: one allowed, one not.
	events := []DockerEvent{
		{ID: "ctr1", Action: "start", Type: "container", Actor: Actor{ID: "ctr1", Attributes: map[string]string{"name": "app"}}},
		{ID: "ctr1", Action: "exec_create", Type: "container"}, // not in allowedActions — must be filtered
		{ID: "ctr1", Action: "stop", Type: "container"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		enc := json.NewEncoder(w)
		for _, e := range events {
			enc.Encode(e) //nolint:errcheck
		}
		// Body ends here; the decoder will get EOF and readEvents returns nil.
	}))
	defer srv.Close()

	c := newTestClient(srv)
	es := &EventStream{
		client:       c,
		initialDelay: 100 * time.Millisecond,
		maxDelay:     100 * time.Millisecond,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ch, err := es.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	var received []DockerEvent
	// Collect up to 2 events (the 2 allowed ones). The goroutine will reconnect
	// after EOF, so we cancel as soon as we have enough.
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				goto done
			}
			received = append(received, ev)
			if len(received) >= 2 {
				cancel()
			}
		case <-ctx.Done():
			goto done
		}
	}
done:

	// We should have received exactly the allowed events.
	if len(received) < 2 {
		t.Fatalf("expected at least 2 events, got %d: %v", len(received), received)
	}
	if received[0].Action != "start" {
		t.Fatalf("first event Action = %q, want %q", received[0].Action, "start")
	}
	if received[1].Action != "stop" {
		t.Fatalf("second event Action = %q, want %q", received[1].Action, "stop")
	}
}

// TestEventStream_Subscribe_ChannelClosedOnCancel verifies that cancelling the
// context causes the channel to eventually close.
func TestEventStream_Subscribe_ChannelClosedOnCancel(t *testing.T) {
	t.Parallel()

	// Serve a slow stream that blocks until the context is cancelled.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Write headers but don't send any events; just keep the connection open.
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	c := newTestClient(srv)
	es := &EventStream{
		client:       c,
		initialDelay: 10 * time.Millisecond,
		maxDelay:     10 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())

	ch, err := es.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Cancel after a tiny delay.
	time.AfterFunc(50*time.Millisecond, cancel)

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel to be closed, got a value")
		}
		// Channel closed as expected.
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for channel to close after context cancel")
	}
}

// TestEventStream_Subscribe_ReconnectsOnError verifies that the stream
// reconnects when the server closes the connection after sending one event,
// and continues delivering events from the new connection.
func TestEventStream_Subscribe_ReconnectsOnError(t *testing.T) {
	t.Parallel()

	// Count how many times we've been called.
	var requestCount int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusOK)
		enc := json.NewEncoder(w)
		enc.Encode(DockerEvent{ID: "ctr1", Action: "start"}) //nolint:errcheck
		// Close the connection (EOF) to trigger reconnect.
	}))
	defer srv.Close()

	c := newTestClient(srv)
	es := &EventStream{
		client:       c,
		initialDelay: 10 * time.Millisecond,
		maxDelay:     20 * time.Millisecond,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ch, err := es.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Collect 3 events across reconnections.
	var count int
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				goto reconnectDone
			}
			count++
			if count >= 3 {
				cancel()
				goto reconnectDone
			}
		case <-ctx.Done():
			goto reconnectDone
		}
	}
reconnectDone:

	if count < 3 {
		t.Fatalf("expected at least 3 events across reconnections, got %d (requests: %d)", count, requestCount)
	}
	if requestCount < 3 {
		t.Fatalf("expected at least 3 reconnections, got %d", requestCount)
	}
}

// TestEventStream_readEvents_ContextCancelDuringDecode verifies that context
// cancellation during decode causes readEvents to return promptly.
func TestEventStream_readEvents_ContextCancelDuringDecode(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Block until the request context is cancelled (client disconnects).
		<-r.Context().Done()
	}))
	defer srv.Close()

	c := newTestClient(srv)
	es := &EventStream{
		client:       c,
		initialDelay: 10 * time.Millisecond,
		maxDelay:     10 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan DockerEvent, 8)

	go func() {
		defer close(ch)
		es.readEvents(ctx, ch) //nolint:errcheck
	}()

	time.AfterFunc(50*time.Millisecond, cancel)

	select {
	case <-ch:
		// Channel closed after context cancel — expected.
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for readEvents to return after context cancel")
	}
}

// TestEventStream_readEvents_GetEventsError exercises the error path where the
// server returns a non-200 status, making GetEvents return an error.
func TestEventStream_readEvents_GetEventsError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	es := &EventStream{
		client:       c,
		initialDelay: 10 * time.Millisecond,
		maxDelay:     10 * time.Millisecond,
	}

	ctx := context.Background()
	ch := make(chan DockerEvent, 8)
	err := es.readEvents(ctx, ch)
	if err == nil {
		t.Fatal("expected error from GetEvents (403), got nil")
	}
}

// TestDockerEvent_JSONDecoding verifies that the DockerEvent struct decodes
// correctly from a real Docker-shaped JSON payload.
func TestDockerEvent_JSONDecoding(t *testing.T) {
	t.Parallel()

	payload := `{
		"status": "start",
		"id": "abc123def456",
		"from": "nginx:latest",
		"Type": "container",
		"Action": "start",
		"Actor": {
			"ID": "abc123def456",
			"Attributes": {
				"name": "my-nginx",
				"image": "nginx:latest"
			}
		},
		"time": 1700000000,
		"timeNano": 1700000000000000000
	}`

	var ev DockerEvent
	if err := json.Unmarshal([]byte(payload), &ev); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if ev.ID != "abc123def456" {
		t.Errorf("ID = %q, want %q", ev.ID, "abc123def456")
	}
	if ev.Action != "start" {
		t.Errorf("Action = %q, want %q", ev.Action, "start")
	}
	if ev.Type != "container" {
		t.Errorf("Type = %q, want %q", ev.Type, "container")
	}
	if ev.Actor.Attributes["name"] != "my-nginx" {
		t.Errorf("Actor.Attributes[name] = %q, want %q", ev.Actor.Attributes["name"], "my-nginx")
	}
	if ev.Time != 1700000000 {
		t.Errorf("Time = %d, want %d", ev.Time, 1700000000)
	}
}
