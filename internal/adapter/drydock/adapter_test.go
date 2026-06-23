package drydock

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/adapter"
	"github.com/codeswhat/portwing/internal/protocol"
)

type captureSender struct {
	msgType string
	data    any
}

func (s *captureSender) SendTypedMessage(msgType string, data any) error {
	s.msgType = msgType
	s.data = data
	return nil
}

type failingSender struct {
	err error
}

func (s *failingSender) SendTypedMessage(string, any) error {
	return s.err
}

type blockingSender struct {
	started chan struct{}
	release <-chan struct{}
	calls   atomic.Int32
}

func (s *blockingSender) SendTypedMessage(string, any) error {
	s.calls.Add(1)
	select {
	case s.started <- struct{}{}:
	default:
	}
	<-s.release
	return nil
}

func TestSendContainerSync_DoesNotPreMarshalContainers(t *testing.T) {
	a := &Adapter{}
	sender := &captureSender{}
	containers := []adapter.Container{
		{
			ID:          "abc123",
			Name:        "web",
			DisplayName: "web",
			Status:      "running",
			Watcher:     "docker",
			Image: adapter.ContainerImage{
				ID:       "sha256:deadbeef",
				Registry: "docker.io",
				Name:     "nginx",
				Tag:      "latest",
			},
		},
	}

	a.sendContainerSync(sender, containers)

	if sender.msgType != protocol.TypeDDContainerSync {
		t.Fatalf("expected message type %q, got %q", protocol.TypeDDContainerSync, sender.msgType)
	}
	if sender.data == nil {
		t.Fatal("expected data to be sent")
	}
	if containsRawMessage(reflect.TypeOf(sender.data)) {
		t.Fatalf("expected sync payload without pre-marshaled json.RawMessage containers, got %T", sender.data)
	}

	payload, err := json.Marshal(sender.data)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	var got struct {
		Containers []adapter.Container `json:"containers"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if !reflect.DeepEqual(containers, got.Containers) {
		t.Fatalf("unexpected containers payload: got %#v want %#v", got.Containers, containers)
	}
}

func containsRawMessage(t reflect.Type) bool {
	if t == nil {
		return false
	}

	rawMessageType := reflect.TypeOf(json.RawMessage{})
	if t == rawMessageType {
		return true
	}

	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return containsRawMessage(t.Elem())
	case reflect.Map:
		return containsRawMessage(t.Key()) || containsRawMessage(t.Elem())
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			if containsRawMessage(t.Field(i).Type) {
				return true
			}
		}
	}

	return false
}

func TestSendTypedMessage_LogsSenderError(t *testing.T) {
	logBuf := &bytes.Buffer{}
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logBuf, nil)))
	defer slog.SetDefault(oldLogger)

	a := &Adapter{}
	a.sendTypedMessage(&failingSender{err: errors.New("send failed")}, protocol.TypeDDContainerSync, map[string]string{"ok": "no"})

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "failed to send typed message") {
		t.Fatalf("expected send failure log, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, protocol.TypeDDContainerSync) {
		t.Fatalf("expected log to include message type %q, got: %s", protocol.TypeDDContainerSync, logOutput)
	}
}

func TestSendTypedMessage_LogsNilSender(t *testing.T) {
	logBuf := &bytes.Buffer{}
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logBuf, nil)))
	defer slog.SetDefault(oldLogger)

	a := &Adapter{}
	a.sendTypedMessage(nil, protocol.TypeDDWatchResponse, struct{}{})

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "sender is nil") {
		t.Fatalf("expected nil-sender log, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, protocol.TypeDDWatchResponse) {
		t.Fatalf("expected log to include message type %q, got: %s", protocol.TypeDDWatchResponse, logOutput)
	}
}

func TestHandleMessage_UsesSemaphoreToLimitConcurrency(t *testing.T) {
	a := &Adapter{
		messageSem: make(chan struct{}, 1),
	}

	release := make(chan struct{})
	sender := &blockingSender{
		started: make(chan struct{}, 4),
		release: release,
	}
	payload := json.RawMessage(`{"watcherType":"docker","watcherName":"main"}`)

	if handled := a.HandleMessage(context.Background(), sender, protocol.TypeDDWatchRequest, payload); !handled {
		t.Fatalf("expected watch request to be handled")
	}

	select {
	case <-sender.started:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for first handler start")
	}

	secondDone := make(chan struct{})
	go func() {
		_ = a.HandleMessage(context.Background(), sender, protocol.TypeDDWatchRequest, payload)
		close(secondDone)
	}()

	select {
	case <-secondDone:
		t.Fatalf("expected second HandleMessage call to block on semaphore")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)

	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for second HandleMessage to complete")
	}
}

func TestHandleMessage_RespectsContextCancellationWhileWaitingForSemaphore(t *testing.T) {
	a := &Adapter{
		messageSem: make(chan struct{}, 1),
	}
	a.messageSem <- struct{}{}
	defer func() { <-a.messageSem }()

	sender := &captureSender{}
	payload := json.RawMessage(`{"watcherType":"docker","watcherName":"main"}`)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resultCh := make(chan bool, 1)
	go func() {
		resultCh <- a.HandleMessage(ctx, sender, protocol.TypeDDWatchRequest, payload)
	}()

	select {
	case handled := <-resultCh:
		if !handled {
			t.Fatalf("expected message type to be recognized")
		}
	case <-time.After(time.Second):
		t.Fatalf("expected HandleMessage to return after context cancellation")
	}

	if sender.msgType != "" {
		t.Fatalf("expected no message to be sent when context is canceled, got %q", sender.msgType)
	}
}
