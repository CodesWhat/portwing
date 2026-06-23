package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestEventStream_run_ContextAlreadyCancelled verifies that run() returns
// immediately (without trying to open the event stream) when the context is
// already cancelled before run() is called.
func TestEventStream_run_ContextAlreadyCancelled(t *testing.T) {
	t.Parallel()

	var requested atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	es := &EventStream{
		client:       c,
		initialDelay: 10 * time.Millisecond,
		maxDelay:     10 * time.Millisecond,
	}

	// Cancel context BEFORE calling run.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ch := make(chan DockerEvent, 8)
	done := make(chan struct{})
	go func() {
		es.run(ctx, ch)
		close(done)
	}()

	select {
	case <-done:
		// run() returned promptly — expected.
	case <-time.After(2 * time.Second):
		t.Fatal("run() did not return promptly with pre-cancelled context")
	}

	if requested.Load() {
		t.Fatal("run() made a request despite pre-cancelled context")
	}
}

// TestEventStream_run_ErrorLogging verifies the non-EOF error path (slog.Warn
// branch) and that the stream reconnects after an error.
func TestEventStream_run_ErrorLogging(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		if n == 1 {
			// First call: return error status to trigger error path.
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		// Subsequent calls: return a valid event then EOF.
		w.WriteHeader(http.StatusOK)
		enc := json.NewEncoder(w)
		enc.Encode(DockerEvent{ID: "ctr1", Action: "start"}) //nolint:errcheck
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

	// Wait to receive one event (from the second+ connection).
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before receiving event")
		}
		if ev.Action != "start" {
			t.Fatalf("Action = %q, want %q", ev.Action, "start")
		}
		cancel()
	case <-ctx.Done():
		t.Fatal("timed out waiting for event after reconnect")
	}
}

// TestEventStream_run_BackoffCapped verifies that the backoff delay is capped
// at maxDelay.
func TestEventStream_run_BackoffCapped(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		// Return 403 every time so each attempt errors and backoff doubles.
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	// Use very short delays so test is fast; initialDelay < maxDelay so cap is exercised.
	es := &EventStream{
		client:       c,
		initialDelay: 5 * time.Millisecond,
		maxDelay:     10 * time.Millisecond,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ch, err := es.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Let it run for a bit (at least 4 reconnections so doubling → cap is exercised).
	time.AfterFunc(300*time.Millisecond, cancel)

	// Drain the channel.
	for range ch {
	}

	if n := callCount.Load(); n < 4 {
		t.Fatalf("expected at least 4 reconnections to exercise backoff cap, got %d", n)
	}
}

// TestEventStream_readEvents_ContextCancelledAtLoopTop verifies that readEvents
// returns ctx.Err() when the context is cancelled after one event is processed
// but before the next Decode call — hitting the ctx.Err() check at the top of
// the decode loop (events.go line 124).
func TestEventStream_readEvents_ContextCancelledAtLoopTop(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Synchronization: the server signals when it has sent the first event.
	firstEventSent := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		enc := json.NewEncoder(w)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Send ONE allowed event.
		enc.Encode(DockerEvent{ID: "ctr1", Action: "start"}) //nolint:errcheck
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		close(firstEventSent)
		// Block until client disconnects (context cancelled).
		<-r.Context().Done()
	}))
	defer srv.Close()

	c := newTestClient(srv)
	es := &EventStream{
		client:       c,
		initialDelay: 10 * time.Millisecond,
		maxDelay:     10 * time.Millisecond,
	}

	// Use a buffered channel so the send succeeds immediately.
	ch := make(chan DockerEvent, 8)

	done := make(chan error, 1)
	go func() {
		done <- es.readEvents(ctx, ch)
	}()

	// Wait for the first event to be sent, drain the channel, then cancel.
	// After the send to ch succeeds, readEvents loops back to the ctx.Err()
	// check. Cancelling now means the next iteration hits line 124.
	select {
	case <-firstEventSent:
		// Drain the buffered event.
		select {
		case <-ch:
		default:
		}
		// Now cancel — readEvents will see ctx.Err() != nil at the loop top.
		cancel()
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for first event")
	}

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected non-nil error after context cancel, got nil")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for readEvents to return")
	}
}

// TestEventStream_readEvents_SendCancelledByContext exercises the select in
// readEvents where ctx.Done() fires while trying to send an event to the channel.
func TestEventStream_readEvents_SendCancelledByContext(t *testing.T) {
	t.Parallel()

	// Server sends a continuous stream of events.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		enc := json.NewEncoder(w)
		flusher, _ := w.(http.Flusher)
		for {
			select {
			case <-r.Context().Done():
				return
			default:
				enc.Encode(DockerEvent{ID: "ctr1", Action: "start"}) //nolint:errcheck
				if flusher != nil {
					flusher.Flush()
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
	}))
	defer srv.Close()

	c := newTestClient(srv)
	es := &EventStream{
		client:       c,
		initialDelay: 10 * time.Millisecond,
		maxDelay:     10 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Use a zero-buffer channel so sends block when the receiver isn't reading.
	ch := make(chan DockerEvent)

	done := make(chan error, 1)
	go func() {
		done <- es.readEvents(ctx, ch)
	}()

	// Cancel immediately so the send-to-channel select hits ctx.Done().
	time.AfterFunc(10*time.Millisecond, cancel)

	// Do NOT drain the channel so the send blocks.
	select {
	case <-done:
		// readEvents returned after context cancel — expected.
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for readEvents to return")
	}
}

// TestEventStream_readEvents_BadJSON exercises the non-EOF decode error path
// (neither io.EOF nor ctx.Err, so the raw error is returned).
func TestEventStream_readEvents_BadJSON(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// Write a valid JSON event followed by garbage.
		w.Write([]byte("{\"Action\":\"start\"}\nNOT_JSON\n")) //nolint:errcheck
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

	// Should return a non-nil, non-EOF error from the malformed JSON.
	if err == nil {
		t.Fatal("expected non-nil error from bad JSON, got nil")
	}
}
