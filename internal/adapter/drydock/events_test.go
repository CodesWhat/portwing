// SPDX-License-Identifier: MIT

package drydock

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"

	"github.com/codeswhat/portwing/internal/docker"
	"github.com/codeswhat/portwing/internal/protocol"
)

func containerEvent(action, id string, attrs map[string]string) docker.DockerEvent {
	return docker.DockerEvent{
		Type:     "container",
		Action:   action,
		Actor:    docker.Actor{ID: id, Attributes: attrs},
		Time:     1700000000,
		TimeNano: 1700000000123456789,
	}
}

func intPtr(v int) *int    { return &v }
func boolPtr(v bool) *bool { return &v }

func TestContainerEventFromDocker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		event  docker.DockerEvent
		wantOK bool
		want   protocol.DDContainerEventMessage
	}{
		{
			name:   "die with exit code 137",
			event:  containerEvent("die", "abc", map[string]string{"name": "web", "exitCode": "137"}),
			wantOK: true,
			want:   protocol.DDContainerEventMessage{ContainerID: "abc", Name: "web", Action: "die", ExitCode: intPtr(137), Time: 1700000000123},
		},
		{
			name:   "die without exit code",
			event:  containerEvent("die", "abc", map[string]string{"name": "web"}),
			wantOK: true,
			want:   protocol.DDContainerEventMessage{ContainerID: "abc", Name: "web", Action: "die", Time: 1700000000123},
		},
		{
			name:   "die with unparseable exit code",
			event:  containerEvent("die", "abc", map[string]string{"name": "web", "exitCode": "nope"}),
			wantOK: true,
			want:   protocol.DDContainerEventMessage{ContainerID: "abc", Name: "web", Action: "die", Time: 1700000000123},
		},
		{
			name:   "die with exit code zero",
			event:  containerEvent("die", "abc", map[string]string{"name": "web", "exitCode": "0"}),
			wantOK: true,
			want:   protocol.DDContainerEventMessage{ContainerID: "abc", Name: "web", Action: "die", ExitCode: intPtr(0), Time: 1700000000123},
		},
		{
			name:   "oom",
			event:  containerEvent("oom", "abc", map[string]string{"name": "web"}),
			wantOK: true,
			want:   protocol.DDContainerEventMessage{ContainerID: "abc", Name: "web", Action: "oom", OOMKilled: boolPtr(true), Time: 1700000000123},
		},
		{
			name:   "stop ignores exit code attribute",
			event:  containerEvent("stop", "abc", map[string]string{"name": "web", "exitCode": "1"}),
			wantOK: true,
			want:   protocol.DDContainerEventMessage{ContainerID: "abc", Name: "web", Action: "stop", Time: 1700000000123},
		},
		{
			name:   "start",
			event:  containerEvent("start", "abc", map[string]string{"name": "web"}),
			wantOK: true,
			want:   protocol.DDContainerEventMessage{ContainerID: "abc", Name: "web", Action: "start", Time: 1700000000123},
		},
		{
			name:   "restart",
			event:  containerEvent("restart", "abc", map[string]string{"name": "web"}),
			wantOK: true,
			want:   protocol.DDContainerEventMessage{ContainerID: "abc", Name: "web", Action: "restart", Time: 1700000000123},
		},
		{
			name:   "leading slash name trimmed",
			event:  containerEvent("start", "abc", map[string]string{"name": "/web"}),
			wantOK: true,
			want:   protocol.DDContainerEventMessage{ContainerID: "abc", Name: "web", Action: "start", Time: 1700000000123},
		},
		{
			name: "time falls back to seconds when timeNano is zero",
			event: docker.DockerEvent{
				Type: "container", Action: "start",
				Actor: docker.Actor{ID: "abc", Attributes: map[string]string{"name": "web"}},
				Time:  1700000000,
			},
			wantOK: true,
			want:   protocol.DDContainerEventMessage{ContainerID: "abc", Name: "web", Action: "start", Time: 1700000000000},
		},
		{
			name: "legacy event with empty type, status and top-level id",
			event: docker.DockerEvent{
				Status: "die", ID: "legacy",
				Actor:    docker.Actor{Attributes: map[string]string{"name": "web", "exitCode": "2"}},
				TimeNano: 5_000_000,
			},
			wantOK: true,
			want:   protocol.DDContainerEventMessage{ContainerID: "legacy", Name: "web", Action: "die", ExitCode: intPtr(2), Time: 5},
		},
		{name: "kill ignored", event: containerEvent("kill", "abc", nil)},
		{name: "exec_start ignored", event: containerEvent("exec_start: sh", "abc", nil)},
		{name: "health_status ignored", event: containerEvent("health_status: unhealthy", "abc", nil)},
		{name: "attach ignored", event: containerEvent("attach", "abc", nil)},
		{name: "create ignored", event: containerEvent("create", "abc", nil)},
		{name: "empty action ignored", event: containerEvent("", "abc", nil)},
		{
			name:  "non-container type ignored",
			event: docker.DockerEvent{Type: "network", Action: "die", Actor: docker.Actor{ID: "n1"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := ContainerEventFromDocker(tt.event)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("message = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestContainerEventMessageJSONShape(t *testing.T) {
	t.Parallel()
	msg, _ := ContainerEventFromDocker(containerEvent("die", "abc", map[string]string{"name": "web", "exitCode": "137"}))
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"containerId":"abc","name":"web","action":"die","exitCode":137,"time":1700000000123}`
	if string(raw) != want {
		t.Fatalf("json = %s, want %s", raw, want)
	}
}

// orderedSender records every message in send order.
type orderedSender struct {
	mu   sync.Mutex
	msgs []sentMessage
}

type sentMessage struct {
	msgType string
	data    any
}

func (s *orderedSender) SendTypedMessage(msgType string, data any) error {
	s.mu.Lock()
	s.msgs = append(s.msgs, sentMessage{msgType, data})
	s.mu.Unlock()
	return nil
}

func (s *orderedSender) snapshot() []sentMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sentMessage(nil), s.msgs...)
}

func TestOnDockerEventEdgeSendsContainerEvent(t *testing.T) {
	t.Parallel()
	a := NewAdapter(nil, "agent", AgentInfo{})
	sender := &orderedSender{}

	a.OnDockerEvent(context.Background(), sender, containerEvent("die", "abc", map[string]string{"name": "web", "exitCode": "137"}))

	got := sender.snapshot()
	if len(got) != 1 || got[0].msgType != protocol.TypeDDContainerEvent {
		t.Fatalf("sent = %+v, want one %s", got, protocol.TypeDDContainerEvent)
	}
	msg, ok := got[0].data.(protocol.DDContainerEventMessage)
	if !ok || msg.Action != "die" || msg.ExitCode == nil || *msg.ExitCode != 137 {
		t.Fatalf("payload = %+v", got[0].data)
	}
}

// A user stop emits kill, die, stop. Drydock classifies the die from the stop
// that follows it, so both must arrive, in order, and kill must not.
func TestOnDockerEventKillDieStopSequence(t *testing.T) {
	t.Parallel()
	a := NewAdapter(nil, "agent", AgentInfo{})
	sender := &orderedSender{}

	attrs := map[string]string{"name": "web", "exitCode": "0"}
	for _, action := range []string{"kill", "die", "stop"} {
		a.OnDockerEvent(context.Background(), sender, containerEvent(action, "abc", attrs))
	}

	got := sender.snapshot()
	if len(got) != 2 {
		t.Fatalf("sent %d messages, want 2: %+v", len(got), got)
	}
	var actions []string
	for _, m := range got {
		if m.msgType != protocol.TypeDDContainerEvent {
			t.Fatalf("type = %s", m.msgType)
		}
		actions = append(actions, m.data.(protocol.DDContainerEventMessage).Action)
	}
	if !reflect.DeepEqual(actions, []string{"die", "stop"}) {
		t.Fatalf("actions = %v, want [die stop]", actions)
	}
	if die := got[0].data.(protocol.DDContainerEventMessage); die.ExitCode == nil || *die.ExitCode != 0 {
		t.Fatalf("die exitCode = %v, want 0", die.ExitCode)
	}
}

func TestOnDockerEventIgnoredSendsNothing(t *testing.T) {
	t.Parallel()
	a := NewAdapter(nil, "agent", AgentInfo{})
	sender := &orderedSender{}
	a.OnDockerEvent(context.Background(), sender, containerEvent("exec_start: sh", "abc", nil))
	if got := sender.snapshot(); len(got) != 0 {
		t.Fatalf("sent = %+v, want nothing", got)
	}
}

// BuildInventory and Refresh apply no filter (all containers, regardless of
// dd.watch), so a container the inventory carries must also produce events.
func TestOnDockerEventFilterParityWithInventory(t *testing.T) {
	t.Parallel()
	a := NewAdapter(nil, "agent", AgentInfo{})
	sender := &orderedSender{}
	a.OnDockerEvent(context.Background(), sender, containerEvent("die", "abc", map[string]string{
		"name": "web", LabelWatch: "false",
	}))
	if got := sender.snapshot(); len(got) != 1 {
		t.Fatalf("sent %d, want 1 for a dd.watch=false container", len(got))
	}
}

func TestOnDockerEventStandardBroadcastsSSE(t *testing.T) {
	t.Parallel()
	a := NewAdapter(nil, "agent", AgentInfo{})
	client := &SSEClient{id: "c1", events: make(chan []byte, 4)}
	a.sse.mu.Lock()
	a.sse.clients[client.id] = client
	a.sse.mu.Unlock()

	a.OnDockerEvent(context.Background(), nil, containerEvent("oom", "abc", map[string]string{"name": "web"}))

	select {
	case raw := <-client.events:
		var got struct {
			Type string                           `json:"type"`
			Data protocol.DDContainerEventMessage `json:"data"`
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.Type != "dd:container-event" || got.Data.Action != "oom" || got.Data.OOMKilled == nil || !*got.Data.OOMKilled {
			t.Fatalf("event = %s", raw)
		}
	default:
		t.Fatal("no SSE event broadcast")
	}
}

func TestBroadcastContainerEventNeverBlocks(t *testing.T) {
	t.Parallel()
	b := NewSSEBroadcaster(fakeContainerProvider{}, "v-test", AgentInfo{})
	msg := protocol.DDContainerEventMessage{ContainerID: "abc", Action: "die"}

	// No consumer: returns without panicking or blocking.
	b.BroadcastContainerEvent(msg)

	// Slow consumer with a full buffer: drops instead of blocking.
	slow := &SSEClient{id: "slow", events: make(chan []byte, 1)}
	slow.events <- []byte("occupied")
	b.mu.Lock()
	b.clients[slow.id] = slow
	b.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 10 {
			b.BroadcastContainerEvent(msg)
		}
	}()
	<-done
	if got := len(slow.events); got != 1 {
		t.Fatalf("slow client buffer = %d, want unchanged 1", got)
	}
}
