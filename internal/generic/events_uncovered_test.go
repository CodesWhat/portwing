package generic

import (
	"context"
	"errors"
	"testing"

	"github.com/codeswhat/portwing/internal/docker"
)

// TestRemoveClientUnknownID exercises the `!ok` early-return in removeClient
// (events.go) — removing a client id that isn't registered (e.g. a duplicate
// disconnect race) must be a no-op rather than panic on close(nil-map-entry).
func TestRemoveClientUnknownID(t *testing.T) {
	t.Parallel()

	b := NewEventBroadcaster(nil)

	// No client with this id was ever registered.
	b.removeClient("does-not-exist")

	b.mu.RLock()
	n := len(b.clients)
	b.mu.RUnlock()
	if n != 0 {
		t.Fatalf("clients map = %d entries, want 0", n)
	}
}

// TestRemoveClientDoubleRemoval exercises the same `!ok` branch via a more
// realistic path: removing the same client twice, as could happen if two
// goroutines race to clean up one disconnect.
func TestRemoveClientDoubleRemoval(t *testing.T) {
	t.Parallel()

	b := NewEventBroadcaster(nil)
	client := &sseClient{id: "dup-client", events: make(chan []byte, 1)}

	b.mu.Lock()
	b.clients[client.id] = client
	b.mu.Unlock()

	b.removeClient(client.id)
	// Second removal must hit the `!ok` branch and return without trying to
	// close an already-closed channel again.
	b.removeClient(client.id)
}

// stubFailingSubscriber is an eventSubscriber whose Subscribe always fails
// synchronously, unlike a real *docker.EventStream (which never does — see
// the comment on newEventStream in events.go). It exists purely to reach the
// defensive error branch in startUpstreamLocked.
type stubFailingSubscriber struct{}

func (stubFailingSubscriber) Subscribe(ctx context.Context) (<-chan docker.DockerEvent, error) {
	return nil, errors.New("stub subscribe failure")
}

// TestStartUpstreamLockedSubscribeError exercises the error branch in
// startUpstreamLocked: when Subscribe fails synchronously, upstreamCancel
// must stay nil (not leak a cancel func for a subscription that never
// started) and registering the first client must not panic.
func TestStartUpstreamLockedSubscribeError(t *testing.T) {
	t.Parallel()

	b := NewEventBroadcaster(nil)
	// newEventStream is instance-scoped (not a package var), so overriding
	// it here can't race with any other test's broadcaster.
	b.newEventStream = func(client *docker.Client) eventSubscriber {
		return stubFailingSubscriber{}
	}
	client := &sseClient{id: "c1", events: make(chan []byte, 1)}

	// registerClient is the first (and only) caller of startUpstreamLocked;
	// this is the first client, so it triggers the failing Subscribe path.
	b.registerClient(client)

	b.mu.RLock()
	cancel := b.upstreamCancel
	b.mu.RUnlock()
	if cancel != nil {
		t.Fatal("upstreamCancel should remain nil when Subscribe fails synchronously")
	}
}

// TestPumpUpstreamMarshalError exercises the marshal-error branch in
// pumpUpstream: when marshalEvent fails, the event is dropped (logged and
// skipped via `continue`) instead of being broadcast or panicking.
func TestPumpUpstreamMarshalError(t *testing.T) {
	t.Parallel()

	b := NewEventBroadcaster(nil)
	// marshalEvent is instance-scoped (not a package var), so overriding it
	// here — before the pumpUpstream goroutine starts below — can't race
	// with any other test's broadcaster.
	b.marshalEvent = func(v any) ([]byte, error) {
		return nil, errors.New("stub marshal failure")
	}
	client := &sseClient{id: "c1", events: make(chan []byte, 1)}
	b.mu.Lock()
	b.clients[client.id] = client
	b.mu.Unlock()

	eventCh := make(chan docker.DockerEvent, 1)
	eventCh <- docker.DockerEvent{
		Type:   "container",
		Action: "start",
		Actor:  docker.Actor{ID: "abc123", Attributes: map[string]string{"name": "c"}},
	}
	close(eventCh)

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.pumpUpstream(eventCh)
	}()

	select {
	case <-done:
	case <-client.events:
		t.Fatal("client received an event despite the marshal failure")
	}

	select {
	case data := <-client.events:
		t.Fatalf("client.events unexpectedly received data: %s", data)
	default:
	}
}

// TestBroadcastDropsOnFullClientBuffer exercises the `default:` branch in
// broadcast: a client whose buffered channel is already full must have the
// event silently dropped for it rather than block the shared upstream pump,
// while other, non-full clients still receive the event.
func TestBroadcastDropsOnFullClientBuffer(t *testing.T) {
	t.Parallel()

	b := NewEventBroadcaster(nil)

	full := &sseClient{id: "full", events: make(chan []byte, 1)}
	full.events <- []byte(`{"already":"queued"}`)

	ok := &sseClient{id: "ok", events: make(chan []byte, 1)}

	b.mu.Lock()
	b.clients[full.id] = full
	b.clients[ok.id] = ok
	b.mu.Unlock()

	data := []byte(`{"type":"container"}`)
	b.broadcast(data)

	// full's buffer stays exactly as it was — the new event was dropped, not
	// queued or overwritten.
	select {
	case got := <-full.events:
		if string(got) != `{"already":"queued"}` {
			t.Fatalf("full client buffer = %s, want the original queued event untouched", got)
		}
	default:
		t.Fatal("full client's original queued event is gone")
	}
	select {
	case <-full.events:
		t.Fatal("full client received a second event; broadcast should have dropped it")
	default:
	}

	// ok's buffer received the broadcast normally.
	select {
	case got := <-ok.events:
		if string(got) != string(data) {
			t.Fatalf("ok client got %s, want %s", got, data)
		}
	default:
		t.Fatal("ok client did not receive the broadcast event")
	}
}
