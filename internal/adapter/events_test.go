// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
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
