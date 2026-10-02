package generic

// events_mutant_test.go adds tests that target Gremlins mutants in events.go.

import (
	"testing"
	"time"
)

// TestNewEventBroadcasterHeartbeatInterval pins the production keepalive
// interval against the ARITHMETIC_BASE mutant on `30 * time.Second`
// (events.go:81:25). Turned into a division it is a zero interval, which the
// other tests only notice because time.NewTicker panics on it.
func TestNewEventBroadcasterHeartbeatInterval(t *testing.T) {
	t.Parallel()

	if got, want := NewEventBroadcaster(nil).heartbeatInterval, 30*time.Second; got != want {
		t.Fatalf("heartbeatInterval = %v, want %v", got, want)
	}
}

// TestRemoveClientStopsUpstreamOnlyWhenTheLastClientLeaves pins both sides of
// the `len(b.clients) == 0` check in removeClient, killing the
// CONDITIONALS_NEGATION mutant at events.go:164:20. Negated, the shared
// upstream subscription is cancelled as soon as one of several clients leaves,
// so the ones still connected stop receiving events, and it is left running
// when the last one goes.
//
// The fan-out test covers the same contract end to end but cannot see this: it
// watches the daemon side of the connection, which closes some time after the
// cancel, and by then both of its clients have gone either way. Here the
// cancel func is a counter, so the moment it is called is exact.
func TestRemoveClientStopsUpstreamOnlyWhenTheLastClientLeaves(t *testing.T) {
	t.Parallel()

	b := NewEventBroadcaster(nil)
	stops := 0

	b.mu.Lock()
	b.clients["first"] = &sseClient{id: "first", events: make(chan []byte, 1)}
	b.clients["second"] = &sseClient{id: "second", events: make(chan []byte, 1)}
	b.upstreamCancel = func() { stops++ }
	b.mu.Unlock()

	subscribed := func() bool {
		b.mu.RLock()
		defer b.mu.RUnlock()
		return b.upstreamCancel != nil
	}

	b.removeClient("first")
	if stops != 0 {
		t.Fatalf("upstream stopped %d time(s) while a client is still connected, want 0", stops)
	}
	if !subscribed() {
		t.Fatal("upstream subscription was dropped while a client is still connected")
	}

	b.removeClient("second")
	if stops != 1 {
		t.Fatalf("upstream stopped %d time(s) after the last client left, want 1", stops)
	}
	if subscribed() {
		t.Fatal("upstream subscription is still held after the last client left")
	}
}
