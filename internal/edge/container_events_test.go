// SPDX-License-Identifier: MIT

package edge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/adapter"
	"github.com/codeswhat/portwing/internal/adapter/drydock"
	"github.com/codeswhat/portwing/internal/config"
	"github.com/codeswhat/portwing/internal/docker"
	"github.com/codeswhat/portwing/internal/protocol"
)

// eventAdapter is a fakeAdapter that forwards Docker events through the real
// Drydock mapping, so the envelope that reaches the sender is the wire message.
type eventAdapter struct {
	fakeAdapter
	dd *drydock.Adapter
}

func (a *eventAdapter) OnDockerEvent(ctx context.Context, sender adapter.MessageSender, ev docker.DockerEvent) {
	a.dd.OnDockerEvent(ctx, sender, ev)
}

func newEventClient(t *testing.T, ch chan docker.DockerEvent, subscribes *atomic.Int32) *Client {
	t.Helper()
	c := newWireClient(t, &config.Config{})
	c.adapter = &eventAdapter{
		fakeAdapter: fakeAdapter{caps: []string{"test"}},
		dd:          drydock.NewAdapter(nil, "agent", drydock.AgentInfo{}),
	}
	c.eventSubscribe = func(context.Context) (<-chan docker.DockerEvent, error) {
		subscribes.Add(1)
		return ch, nil
	}
	return c
}

func TestForwardContainerEventsSendsOverTunnel(t *testing.T) {
	t.Parallel()

	ch := make(chan docker.DockerEvent, 4)
	var subscribes atomic.Int32
	c := newEventClient(t, ch, &subscribes)

	sendCh := make(chan protocol.Envelope, 8)
	c.connMu.Lock()
	c.sendCh = sendCh
	c.sendState = &outboundQueueState{}
	c.connMu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.forwardContainerEvents(ctx)
	}()

	ch <- docker.DockerEvent{Type: "container", Action: "die", Actor: docker.Actor{ID: "abc", Attributes: map[string]string{"name": "web", "exitCode": "137"}}}
	select {
	case env := <-sendCh:
		if env.Type != protocol.TypeDDContainerEvent {
			t.Fatalf("type = %s, want %s", env.Type, protocol.TypeDDContainerEvent)
		}
	case <-time.After(time.Second):
		t.Fatal("no dd:container_event on the tunnel")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("forwardContainerEvents did not stop on cancel")
	}
}

func TestForwardContainerEventsDropsWhileDisconnected(t *testing.T) {
	t.Parallel()

	ch := make(chan docker.DockerEvent, 4)
	var subscribes atomic.Int32
	c := newEventClient(t, ch, &subscribes)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.forwardContainerEvents(ctx)
	}()

	// No sendCh: the event is dropped and the reader keeps draining.
	for range 4 {
		ch <- docker.DockerEvent{Type: "container", Action: "die", Actor: docker.Actor{ID: "abc"}}
	}
	deadline := time.After(time.Second)
	for len(ch) != 0 {
		select {
		case <-deadline:
			t.Fatal("event reader stalled while disconnected")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	<-done
}

func TestForwardContainerEventsSkipsAdapterWithoutSupport(t *testing.T) {
	t.Parallel()
	c := newWireClient(t, &config.Config{})
	c.eventSubscribe = func(context.Context) (<-chan docker.DockerEvent, error) {
		t.Error("subscribed for an adapter that does not forward events")
		return nil, nil
	}
	c.forwardContainerEvents(context.Background())
}

// One subscription per Run, however many times the controller connection is
// retried. Not parallel: it swaps the package-level reconnectWait.
func TestRunSubscribesToContainerEventsOnceAcrossReconnects(t *testing.T) {
	var dials atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dials.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var waits atomic.Int32
	oldWait := reconnectWait
	reconnectWait = func(time.Duration) <-chan time.Time {
		if waits.Add(1) >= 3 {
			cancel()
		}
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}
	t.Cleanup(func() { reconnectWait = oldWait })

	addr := freeAddr(t)
	var subscribes atomic.Int32
	c := newEventClient(t, make(chan docker.DockerEvent), &subscribes)
	c.cfg = &config.Config{
		DrydockURL:        srv.URL,
		HeartbeatInterval: 30,
		WelcomeTimeout:    5,
		ReconnectDelay:    1,
		MaxReconnectDelay: 4,
		DDPollInterval:    300,
		BindAddress:       "127.0.0.1",
		Port:              portFrom(addr),
		SkipDFCollection:  true,
	}

	_ = c.Run(ctx)

	if dials.Load() < 3 {
		t.Fatalf("controller dialed %d times, want at least 3 reconnect attempts", dials.Load())
	}
	// The subscribe goroutine starts before the first dial, but give it a beat.
	deadline := time.Now().Add(time.Second)
	for subscribes.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := subscribes.Load(); got != 1 {
		t.Fatalf("subscribed %d times across reconnects, want 1", got)
	}
}
