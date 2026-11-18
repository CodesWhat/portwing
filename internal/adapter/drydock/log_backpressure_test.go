// SPDX-License-Identifier: MIT

package drydock

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/codeswhat/portwing/internal/protocol"
)

// bulkLogTestSender records which entry point each message used, so a test can
// tell the backpressured bulk path from the plain control one.
type bulkLogTestSender struct {
	logStreamTestSender
	mu   sync.Mutex
	bulk []string
}

func (s *bulkLogTestSender) SendBulkTypedMessage(ctx context.Context, msgType string, data any) error {
	s.mu.Lock()
	s.bulk = append(s.bulk, msgType)
	s.mu.Unlock()
	return s.SendTypedMessage(msgType, data)
}

// Log chunks are bulk traffic: a sender that supports backpressure must get
// them through SendBulkTypedMessage, with the end frame still a control frame.
func TestLogStreamChunksUseBulkSender(t *testing.T) {
	client, calls, shutdown := newRouteTestDockerClient(t)
	defer shutdown()
	calls.setLogsResponse("container-1", routeTestDockerLogFrame(1, []byte("one\n")))

	a := NewAdapter(client, "test-agent", AgentInfo{})
	sender := &bulkLogTestSender{logStreamTestSender: *newLogStreamTestSender()}
	payload, _ := json.Marshal(protocol.DDContainerLogRequestMessage{RequestID: "r", ContainerID: "container-1", Stream: true})
	if !a.HandleMessage(context.Background(), sender, protocol.TypeDDContainerLogRequest, payload) {
		t.Fatal("log request not handled")
	}

	if got := waitForLogStreamEvent(t, &sender.logStreamTestSender).msgType; got != protocol.TypeDDContainerLogChunk {
		t.Fatalf("first message = %q, want a log chunk", got)
	}
	if got := waitForLogStreamEvent(t, &sender.logStreamTestSender).msgType; got != protocol.TypeDDContainerLogEnd {
		t.Fatalf("second message = %q, want the end frame", got)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.bulk) != 1 || sender.bulk[0] != protocol.TypeDDContainerLogChunk {
		t.Fatalf("bulk sends = %v, want exactly the chunk", sender.bulk)
	}
}

// A legacy buffered log response over the cap errors out instead of sending a
// silently truncated log.
func TestLegacyLogRequestOverCapReturnsError(t *testing.T) {
	client, calls, shutdown := newRouteTestDockerClient(t)
	defer shutdown()
	calls.setLogsResponse("container-1", routeTestDockerLogFrame(1, []byte(strings.Repeat("x", maxContainerLogBytes+1024))))

	a := NewAdapter(client, "test-agent", AgentInfo{})
	sender := newLogStreamTestSender()
	a.handleContainerLogRequest(context.Background(), sender, protocol.DDContainerLogRequestMessage{RequestID: "r", ContainerID: "container-1"})

	event := waitForLogStreamEvent(t, sender)
	var resp protocol.DDContainerLogResponseMessage
	if err := json.Unmarshal(event.data, &resp); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(resp.Logs, "error: logs exceed ") || len(resp.Logs) > 200 {
		t.Fatalf("response = %.120q, want a short over-cap error", resp.Logs)
	}
}
