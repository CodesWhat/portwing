// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/adapter"
	"github.com/codeswhat/portwing/internal/docker"
)

type capabilityListAdapter struct {
	stubServerAdapter
	capabilities []string
}

func (a *capabilityListAdapter) Capabilities() []string { return a.capabilities }

func TestInfoCapabilitiesPreserveFirstOccurrence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		adapter, want []string
	}{
		{"nil adapter", nil, []string{"compose", "exec", "metrics", "events"}},
		{"overlap", []string{"events", "future", "exec", "future", "Events"}, []string{"compose", "exec", "metrics", "events", "future", "Events"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, stop := newStubDockerClient(t)
			defer stop()
			s, err := NewServer(minimalConfig(), client, &capabilityListAdapter{capabilities: tc.adapter})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := s.Shutdown(context.Background()); err != nil {
					t.Error(err)
				}
			}()
			response := httptest.NewRecorder()
			s.httpServer.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://localhost/_portwing/info", nil))
			if response.Code != http.StatusOK {
				t.Fatalf("HTTP%d: %s", response.Code, response.Body)
			}
			var body struct {
				Capabilities []string `json:"capabilities"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(body.Capabilities, tc.want) {
				t.Fatalf("capabilities=%v, want%v", body.Capabilities, tc.want)
			}
		})
	}
}

type eventForwardingAdapter struct {
	stubServerAdapter
	mu      sync.Mutex
	events  []docker.DockerEvent
	senders []adapter.MessageSender
	got     chan struct{}
}

func (a *eventForwardingAdapter) OnDockerEvent(_ context.Context, sender adapter.MessageSender, ev docker.DockerEvent) {
	a.mu.Lock()
	a.events = append(a.events, ev)
	a.senders = append(a.senders, sender)
	a.mu.Unlock()
	a.got <- struct{}{}
}

func TestForwardContainerEventsStandardMode(t *testing.T) {
	t.Parallel()

	fw := &eventForwardingAdapter{got: make(chan struct{}, 4)}
	ch := make(chan docker.DockerEvent, 4)
	var subscribes atomic.Int32
	s := &Server{adapter: fw, eventSubscribe: func(context.Context) (<-chan docker.DockerEvent, error) {
		subscribes.Add(1)
		return ch, nil
	}}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.forwardContainerEvents(ctx)
	}()

	ch <- docker.DockerEvent{Action: "die"}
	ch <- docker.DockerEvent{Action: "stop"}
	for range 2 {
		select {
		case <-fw.got:
		case <-time.After(time.Second):
			t.Fatal("event not forwarded to adapter")
		}
	}
	fw.mu.Lock()
	if len(fw.events) != 2 || fw.events[0].Action != "die" || fw.events[1].Action != "stop" {
		t.Errorf("events = %+v, want die then stop", fw.events)
	}
	for _, sender := range fw.senders {
		if sender != nil {
			t.Errorf("standard mode passed sender %v, want nil", sender)
		}
	}
	fw.mu.Unlock()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("forwardContainerEvents did not stop on cancel")
	}
	if subscribes.Load() != 1 {
		t.Fatalf("subscribed %d times, want 1", subscribes.Load())
	}
}

func TestForwardContainerEventsSkipsAdapterWithoutSupport(t *testing.T) {
	t.Parallel()
	s := &Server{adapter: &stubServerAdapter{}, eventSubscribe: func(context.Context) (<-chan docker.DockerEvent, error) {
		t.Error("subscribed for an adapter that does not forward events")
		return nil, nil
	}}
	s.forwardContainerEvents(context.Background())
}
