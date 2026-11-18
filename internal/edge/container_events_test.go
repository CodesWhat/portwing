// SPDX-License-Identifier: MIT

package edge

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestForwardContainerEventsDefaultsToDockerEventStream(t *testing.T) {
	t.Parallel()

	c := newEventClient(t, nil, new(atomic.Int32))
	c.eventSubscribe = nil
	c.rawDockerClient = fakeEventDaemon(t, `{"Type":"container","Action":"die","Actor":{"ID":"abc","Attributes":{"name":"web"}}}`)

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

	select {
	case env := <-sendCh:
		if env.Type != protocol.TypeDDContainerEvent {
			t.Fatalf("type = %s, want %s", env.Type, protocol.TypeDDContainerEvent)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no dd:container_event from the default Docker event stream")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("forwardContainerEvents did not stop on cancel")
	}
}

func TestForwardContainerEventsWithoutDockerClientReturns(t *testing.T) {
	t.Parallel()

	c := newEventClient(t, nil, new(atomic.Int32))
	c.eventSubscribe = nil
	c.rawDockerClient = nil

	done := make(chan struct{})
	go func() {
		defer close(done)
		c.forwardContainerEvents(context.Background())
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("forwardContainerEvents blocked with no Docker client and no subscriber")
	}
}

// fakeEventDaemon serves one Docker event on /events over a unix socket, then
// holds the stream open until the test ends.
func fakeEventDaemon(t *testing.T, eventJSON string) *docker.Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "pw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, eventJSON+"\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		close(release)
		_ = srv.Close()
	})
	client, err := docker.NewClient(sock, 5)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func eventSender(t *testing.T, c *Client) adapter.MessageSender {
	t.Helper()
	sender, ok := c.connectedMessageSender()
	if !ok {
		t.Fatal("connectedMessageSender not connected")
	}
	return sender
}

func TestContainerEventsDropOnFullQueueWithoutEviction(t *testing.T) {
	t.Parallel()

	c, ctrl := newHandshakeTestClient(t)
	registry := newEdgeMetricsForBackpressureTest()
	c.metrics = registry
	sendCh := make(chan protocol.Envelope, 2)
	c.connMu.Lock()
	c.sendCh = sendCh
	c.sendState = &outboundQueueState{}
	c.connMu.Unlock()

	sender := eventSender(t, c)
	for i := 0; i < cap(sendCh); i++ {
		if err := sender.SendTypedMessage(protocol.TypeDDContainerEvent, map[string]int{"n": i}); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	// Queue is full: further events are dropped, not evicting the connection.
	for i := 0; i < 3; i++ {
		if err := sender.SendTypedMessage(protocol.TypeDDContainerEvent, map[string]int{"n": 99}); err != nil {
			t.Fatalf("overflow send: %v", err)
		}
	}
	if len(sendCh) != cap(sendCh) {
		t.Fatalf("queue len = %d, want %d", len(sendCh), cap(sendCh))
	}
	requireBackpressureCount(t, registry, 0)
	var out strings.Builder
	registry.WritePrometheus(&out, func(v string) string { return v })
	if !strings.Contains(out.String(), "portwing_edge_dropped_events_total 3\n") {
		t.Fatalf("dropped counter missing:\n%s", out.String())
	}

	// The connection stays up: a reliable message still enqueues after a drain.
	<-sendCh
	if err := c.sendTypedMessage(protocol.TypePong, protocol.PongMessage{Timestamp: 1}); err != nil {
		t.Fatalf("reliable send: %v", err)
	}
	if got := len(sendCh); got != cap(sendCh) {
		t.Fatalf("queue len after reliable send = %d, want %d", got, cap(sendCh))
	}
	_ = ctrl
	c.connMu.Lock()
	stillUp := c.sendCh != nil && c.conn != nil
	c.connMu.Unlock()
	if !stillUp {
		t.Fatal("connection was evicted")
	}
}

func TestContainerEventsDropOnClosedQueue(t *testing.T) {
	t.Parallel()

	c, _ := newHandshakeTestClient(t)
	sendCh := make(chan protocol.Envelope, 2)
	state := &outboundQueueState{}
	c.connMu.Lock()
	c.sendCh = sendCh
	c.sendState = state
	c.connMu.Unlock()

	sender := eventSender(t, c)
	state.closeAndDiscard(sendCh)
	if err := sender.SendTypedMessage(protocol.TypeDDContainerEvent, map[string]int{"n": 1}); err != nil {
		t.Fatalf("send on closed queue: %v", err)
	}
	if len(sendCh) != 0 {
		t.Fatal("event enqueued on a closed queue")
	}
}

func TestContainerEventSenderNeverWritesDirectlyDuringHandshake(t *testing.T) {
	t.Parallel()

	c, ctrl := newHandshakeTestClient(t)
	if _, ok := c.connectedMessageSender(); ok {
		t.Fatal("connectedMessageSender connected with a nil queue")
	}

	// A sender captured with a nil queue (as if connection B were mid-handshake)
	// must drop, not fall through to the direct handshake write.
	target := c.currentOutboundTarget()
	sender := &edgeMessageSender{client: c, target: &target, lossy: true}
	if err := sender.SendTypedMessage(protocol.TypeDDContainerEvent, map[string]int{"n": 1}); err != nil {
		t.Fatalf("send: %v", err)
	}
	_ = ctrl.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if _, _, err := ctrl.ReadMessage(); err == nil {
		t.Fatal("event was written directly during the handshake")
	}
}
