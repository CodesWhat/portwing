package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDDContainerRequestIDJSONFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		value      any
		target     any
		wantJSON   string
		wantReqID  string
		emptyValue any
	}{
		{
			name:       "log request",
			value:      DDContainerLogRequestMessage{RequestID: "req-log", ContainerID: "container-1"},
			target:     &DDContainerLogRequestMessage{},
			wantJSON:   `"requestId":"req-log"`,
			wantReqID:  "req-log",
			emptyValue: DDContainerLogRequestMessage{ContainerID: "container-1"},
		},
		{
			name:       "log response",
			value:      DDContainerLogResponseMessage{RequestID: "req-log", ContainerID: "container-1", Logs: "hello\n"},
			target:     &DDContainerLogResponseMessage{},
			wantJSON:   `"requestId":"req-log"`,
			wantReqID:  "req-log",
			emptyValue: DDContainerLogResponseMessage{ContainerID: "container-1", Logs: "hello\n"},
		},
		{
			name:       "delete request",
			value:      DDContainerDeleteRequestMessage{RequestID: "req-delete", ContainerID: "container-1"},
			target:     &DDContainerDeleteRequestMessage{},
			wantJSON:   `"requestId":"req-delete"`,
			wantReqID:  "req-delete",
			emptyValue: DDContainerDeleteRequestMessage{ContainerID: "container-1"},
		},
		{
			name:       "delete response",
			value:      DDContainerDeleteResponseMessage{RequestID: "req-delete", ContainerID: "container-1", Success: true},
			target:     &DDContainerDeleteResponseMessage{},
			wantJSON:   `"requestId":"req-delete"`,
			wantReqID:  "req-delete",
			emptyValue: DDContainerDeleteResponseMessage{ContainerID: "container-1", Success: true},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !strings.Contains(string(data), tc.wantJSON) {
				t.Fatalf("json = %s, want to contain %s", data, tc.wantJSON)
			}
			if err := json.Unmarshal(data, tc.target); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := requestIDFromDDContainerMessage(t, tc.target); got != tc.wantReqID {
				t.Fatalf("RequestID = %q, want %q", got, tc.wantReqID)
			}

			emptyData, err := json.Marshal(tc.emptyValue)
			if err != nil {
				t.Fatalf("marshal empty request id: %v", err)
			}
			if strings.Contains(string(emptyData), "requestId") {
				t.Fatalf("json = %s, want requestId omitted when empty", emptyData)
			}
		})
	}
}

func requestIDFromDDContainerMessage(t *testing.T, value any) string {
	t.Helper()

	switch msg := value.(type) {
	case *DDContainerLogRequestMessage:
		return msg.RequestID
	case *DDContainerLogResponseMessage:
		return msg.RequestID
	case *DDContainerDeleteRequestMessage:
		return msg.RequestID
	case *DDContainerDeleteResponseMessage:
		return msg.RequestID
	default:
		t.Fatalf("unexpected message type %T", value)
		return ""
	}
}
