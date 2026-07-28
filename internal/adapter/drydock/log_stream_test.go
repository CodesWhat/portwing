package drydock

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/protocol"
)

type logStreamTestEvent struct {
	msgType string
	data    json.RawMessage
}

type logStreamTestSender struct {
	events chan logStreamTestEvent
}

func newLogStreamTestSender() *logStreamTestSender {
	return &logStreamTestSender{events: make(chan logStreamTestEvent, 16)}
}

func (s *logStreamTestSender) SendTypedMessage(msgType string, data any) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	s.events <- logStreamTestEvent{msgType: msgType, data: encoded}
	return nil
}

func waitForLogStreamEvent(t *testing.T, sender *logStreamTestSender) logStreamTestEvent {
	t.Helper()

	select {
	case event := <-sender.events:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for log stream event")
		return logStreamTestEvent{}
	}
}

func waitForDockerLogCall(t *testing.T, calls *routeTestDockerCalls) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for calls.logsCalls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for Docker log request")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestContainerLogStreamPreservesStdoutStderrAndEnds(t *testing.T) {
	t.Parallel()

	client, calls, shutdown := newRouteTestDockerClient(t)
	defer shutdown()
	calls.setLogsResponse("container-1", append(
		routeTestDockerLogFrame(1, []byte("out\n")),
		routeTestDockerLogFrame(2, []byte("err\n"))...,
	))

	a := NewAdapter(client, "test-agent", AgentInfo{})
	sender := newLogStreamTestSender()
	payload := json.RawMessage(`{
		"requestId":"stream-1",
		"containerId":"container-1",
		"stream":true,
		"follow":true,
		"tail":25,
		"timestamps":true
	}`)

	if !a.HandleMessage(context.Background(), sender, protocol.TypeDDContainerLogRequest, payload) {
		t.Fatal("expected streaming dd:container_log_request to be handled")
	}

	for _, want := range []struct {
		msgType string
		stream  string
		logs    string
	}{
		{msgType: "dd:container_log_chunk", stream: "stdout", logs: "out\n"},
		{msgType: "dd:container_log_chunk", stream: "stderr", logs: "err\n"},
	} {
		event := waitForLogStreamEvent(t, sender)
		if event.msgType != want.msgType {
			t.Fatalf("message type = %q, want %q", event.msgType, want.msgType)
		}

		var got struct {
			RequestID   string `json:"requestId"`
			ContainerID string `json:"containerId"`
			Stream      string `json:"stream"`
			Logs        string `json:"logs"`
		}
		if err := json.Unmarshal(event.data, &got); err != nil {
			t.Fatalf("decode chunk: %v", err)
		}
		if got.RequestID != "stream-1" || got.ContainerID != "container-1" ||
			got.Stream != want.stream || got.Logs != want.logs {
			t.Fatalf("chunk = %+v, want request/container %q/%q stream/logs %q/%q",
				got, "stream-1", "container-1", want.stream, want.logs)
		}
	}

	end := waitForLogStreamEvent(t, sender)
	if end.msgType != "dd:container_log_end" {
		t.Fatalf("message type = %q, want dd:container_log_end", end.msgType)
	}
	var endData struct {
		RequestID   string `json:"requestId"`
		ContainerID string `json:"containerId"`
	}
	if err := json.Unmarshal(end.data, &endData); err != nil {
		t.Fatalf("decode end: %v", err)
	}
	if endData.RequestID != "stream-1" || endData.ContainerID != "container-1" {
		t.Fatalf("end = %+v, want stream-1/container-1", endData)
	}

	query := lastLogsQuery(t, calls)
	if query.Get("follow") != "1" || query.Get("tail") != "25" || query.Get("timestamps") != "1" {
		t.Fatalf("Docker log query = %q, want follow=1 tail=25 timestamps=1", query.Encode())
	}
}

func TestContainerLogStreamCancelStopsActiveRequest(t *testing.T) {
	t.Parallel()

	client, calls, shutdown := newRouteTestDockerClient(t)
	defer shutdown()
	calls.setBlockingLogsResponse("container-1", routeTestDockerLogFrame(1, []byte("live\n")))

	a := NewAdapter(client, "test-agent", AgentInfo{})
	sender := newLogStreamTestSender()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	request := json.RawMessage(`{
		"requestId":"stream-cancel",
		"containerId":"container-1",
		"stream":true,
		"follow":true
	}`)
	if !a.HandleMessage(ctx, sender, protocol.TypeDDContainerLogRequest, request) {
		t.Fatal("expected streaming dd:container_log_request to be handled")
	}
	waitForDockerLogCall(t, calls)

	cancelMessage := json.RawMessage(`{"requestId":"stream-cancel","containerId":"container-1"}`)
	if !a.HandleMessage(ctx, sender, "dd:container_log_cancel", cancelMessage) {
		t.Fatal("expected dd:container_log_cancel to be handled")
	}

	for {
		event := waitForLogStreamEvent(t, sender)
		if event.msgType == "dd:container_log_end" {
			return
		}
	}
}

func TestContainerLogStreamOpenFailureUsesTerminalError(t *testing.T) {
	t.Parallel()

	client, _, shutdown := newRouteTestDockerClient(t)
	defer shutdown()

	a := NewAdapter(client, "test-agent", AgentInfo{})
	sender := newLogStreamTestSender()
	payload := json.RawMessage(`{
		"requestId":"stream-error",
		"containerId":"../invalid",
		"stream":true,
		"follow":true
	}`)

	if !a.HandleMessage(context.Background(), sender, protocol.TypeDDContainerLogRequest, payload) {
		t.Fatal("expected streaming dd:container_log_request to be handled")
	}

	event := waitForLogStreamEvent(t, sender)
	if event.msgType != "dd:container_log_error" {
		t.Fatalf("message type = %q, want dd:container_log_error", event.msgType)
	}
	var got struct {
		RequestID   string `json:"requestId"`
		ContainerID string `json:"containerId"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(event.data, &got); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if got.RequestID != "stream-error" || got.ContainerID != "../invalid" || got.Error == "" {
		t.Fatalf("error = %+v, want matching request/container and non-empty error", got)
	}
}

func TestContainerLogStreamRawTTYUsesStdoutChunk(t *testing.T) {
	t.Parallel()

	client, calls, shutdown := newRouteTestDockerClient(t)
	defer shutdown()
	calls.setLogsResponse("container-1", []byte("raw tty output\n"))

	a := NewAdapter(client, "test-agent", AgentInfo{})
	sender := newLogStreamTestSender()
	payload := json.RawMessage(`{
		"requestId":"stream-tty",
		"containerId":"container-1",
		"stream":true
	}`)
	if !a.HandleMessage(context.Background(), sender, protocol.TypeDDContainerLogRequest, payload) {
		t.Fatal("expected streaming dd:container_log_request to be handled")
	}

	event := waitForLogStreamEvent(t, sender)
	if event.msgType != protocol.TypeDDContainerLogChunk {
		t.Fatalf("message type = %q, want %q", event.msgType, protocol.TypeDDContainerLogChunk)
	}
	var got protocol.DDContainerLogChunkMessage
	if err := json.Unmarshal(event.data, &got); err != nil {
		t.Fatalf("decode chunk: %v", err)
	}
	if got.Stream != "stdout" || got.Logs != "raw tty output\n" {
		t.Fatalf("chunk = %+v, want raw output on stdout", got)
	}
}

func TestContainerLogStreamRejectsDuplicateAndCapacity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		fill func(*Adapter)
	}{
		{
			name: "duplicate request ID",
			fill: func(a *Adapter) {
				_, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)
				a.logStreams["stream-rejected"] = activeContainerLogStream{
					containerID: "container-1",
					cancel:      cancel,
				}
			},
		},
		{
			name: "stream capacity reached",
			fill: func(a *Adapter) {
				for i := 0; i < maxContainerLogStreams; i++ {
					_, cancel := context.WithCancel(context.Background())
					t.Cleanup(cancel)
					a.logStreams[time.Unix(int64(i), 0).String()] = activeContainerLogStream{
						containerID: "container-1",
						cancel:      cancel,
					}
				}
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			a := &Adapter{logStreams: make(map[string]activeContainerLogStream)}
			tc.fill(a)
			sender := newLogStreamTestSender()
			payload := json.RawMessage(`{
				"requestId":"stream-rejected",
				"containerId":"container-1",
				"stream":true
			}`)

			if !a.HandleMessage(context.Background(), sender, protocol.TypeDDContainerLogRequest, payload) {
				t.Fatal("expected streaming dd:container_log_request to be handled")
			}
			event := waitForLogStreamEvent(t, sender)
			if event.msgType != protocol.TypeDDContainerLogError {
				t.Fatalf("message type = %q, want %q", event.msgType, protocol.TypeDDContainerLogError)
			}
		})
	}
}

func TestContainerLogStreamSenderFailureReleasesSlot(t *testing.T) {
	t.Parallel()

	client, _, shutdown := newRouteTestDockerClient(t)
	defer shutdown()

	a := NewAdapter(client, "test-agent", AgentInfo{})
	sender := &failingSender{err: errors.New("send failed")}
	payload := json.RawMessage(`{
		"requestId":"stream-send-error",
		"containerId":"container-1",
		"stream":true
	}`)
	if !a.HandleMessage(context.Background(), sender, protocol.TypeDDContainerLogRequest, payload) {
		t.Fatal("expected streaming dd:container_log_request to be handled")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		a.logStreamsMu.Lock()
		active := len(a.logStreams)
		a.logStreamsMu.Unlock()
		if active == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("active log streams = %d after sender failure, want 0", active)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestContainerLogRequestStreamFieldRoundTrips(t *testing.T) {
	t.Parallel()

	var request protocol.DDContainerLogRequestMessage
	if err := json.Unmarshal([]byte(`{"requestId":"stream-1","containerId":"container-1","stream":true}`), &request); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("unmarshal encoded request: %v", err)
	}
	if got["stream"] != true {
		t.Fatalf("stream field = %v, want true; encoded request: %s", got["stream"], encoded)
	}
}
