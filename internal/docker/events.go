package docker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"time"
)

var allowedActions = map[string]bool{
	"create":        true,
	"start":         true,
	"stop":          true,
	"die":           true,
	"kill":          true,
	"restart":       true,
	"pause":         true,
	"unpause":       true,
	"destroy":       true,
	"rename":        true,
	"update":        true,
	"oom":           true,
	"health_status": true,
}

type DockerEvent struct {
	Status   string `json:"status"`
	ID       string `json:"id"`
	From     string `json:"from"`
	Type     string `json:"Type"`
	Action   string `json:"Action"`
	Actor    Actor  `json:"Actor"`
	Time     int64  `json:"time"`
	TimeNano int64  `json:"timeNano"`
}

type Actor struct {
	ID         string            `json:"ID"`
	Attributes map[string]string `json:"Attributes"`
}

// EventStream manages a resilient subscription to Docker container events.
type EventStream struct {
	client       *Client
	initialDelay time.Duration
	maxDelay     time.Duration
}

// NewEventStream creates an EventStream that will reconnect with exponential
// backoff on failures.
func NewEventStream(client *Client) *EventStream {
	return &EventStream{
		client:       client,
		initialDelay: 5 * time.Second,
		maxDelay:     60 * time.Second,
	}
}

// Subscribe opens the Docker event stream and returns a channel of filtered
// container events. The stream automatically reconnects with exponential
// backoff on errors. Cancel the context to stop the subscription; the
// channel will be closed when the goroutine exits.
func (es *EventStream) Subscribe(ctx context.Context) (<-chan DockerEvent, error) {
	ch := make(chan DockerEvent, 64)

	go es.run(ctx, ch)

	return ch, nil
}

func (es *EventStream) run(ctx context.Context, ch chan<- DockerEvent) {
	defer close(ch)

	delay := es.initialDelay

	for {
		if ctx.Err() != nil {
			return
		}

		connectedAt := time.Now()
		err := es.readEvents(ctx, ch)

		if ctx.Err() != nil {
			return
		}

		if err != nil {
			slog.Warn("docker event stream disconnected", "error", err)
		}

		// If the connection was stable for >30s, reset backoff.
		if time.Since(connectedAt) > 30*time.Second {
			delay = es.initialDelay
		}

		slog.Info("reconnecting to docker event stream", "delay", delay)

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}

		// Exponential backoff.
		delay *= 2
		if delay > es.maxDelay {
			delay = es.maxDelay
		}
	}
}

func (es *EventStream) readEvents(ctx context.Context, ch chan<- DockerEvent) error {
	body, err := es.client.GetEvents(ctx)
	if err != nil {
		return err
	}
	defer body.Close()

	dec := json.NewDecoder(body)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		var event DockerEvent
		if err := dec.Decode(&event); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}

		if !allowedActions[event.Action] {
			continue
		}

		select {
		case ch <- event:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
