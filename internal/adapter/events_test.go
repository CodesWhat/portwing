// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/docker"
)

func TestRunContainerEventsDeliversInOrderAndStopsOnCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan docker.DockerEvent, 4)
	var subscribes atomic.Int32
	subscribe := func(context.Context) (<-chan docker.DockerEvent, error) {
		subscribes.Add(1)
		return ch, nil
	}
	got := make(chan string, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunContainerEvents(ctx, subscribe, func(ev docker.DockerEvent) { got <- ev.Action })
	}()

	ch <- docker.DockerEvent{Action: "die"}
	ch <- docker.DockerEvent{Action: "stop"}
	for _, want := range []string{"die", "stop"} {
		select {
		case a := <-got:
			if a != want {
				t.Fatalf("got %s, want %s", a, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s", want)
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunContainerEvents did not stop after cancel")
	}
	if n := subscribes.Load(); n != 1 {
		t.Fatalf("subscribe called %d times, want 1", n)
	}
}

func TestRunContainerEventsEndCases(t *testing.T) {
	t.Parallel()

	closed := make(chan docker.DockerEvent)
	close(closed)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name      string
		ctx       context.Context
		subscribe EventSubscribe
		wantCalls int32
	}{
		{"nil subscribe", context.Background(), nil, 0},
		{"already cancelled", cancelled, nil, 0},
		{"subscribe error", context.Background(), func(context.Context) (<-chan docker.DockerEvent, error) {
			return nil, errors.New("boom")
		}, 0},
		{"stream closed", context.Background(), func(context.Context) (<-chan docker.DockerEvent, error) {
			return closed, nil
		}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			done := make(chan struct{})
			go func() {
				defer close(done)
				RunContainerEvents(tt.ctx, tt.subscribe, func(docker.DockerEvent) { calls.Add(1) })
			}()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("RunContainerEvents did not return")
			}
			if calls.Load() != tt.wantCalls {
				t.Fatalf("handler calls = %d, want %d", calls.Load(), tt.wantCalls)
			}
		})
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

func TestDockerEventSubscriberStreamsDaemonEventsAndClosesOnCancel(t *testing.T) {
	t.Parallel()

	client := fakeEventDaemon(t, `{"Type":"container","Action":"die","Actor":{"ID":"abc","Attributes":{"name":"web"}}}`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := DockerEventSubscriber(client)(ctx)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	select {
	case ev := <-events:
		if ev.Action != "die" || ev.Actor.ID != "abc" {
			t.Fatalf("event = %+v, want die for abc", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event from the daemon stream")
	}

	cancel()
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("expected the channel to close after cancel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("event channel did not close after cancel")
	}
}
