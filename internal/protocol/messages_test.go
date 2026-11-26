// SPDX-License-Identifier: MIT

package protocol

import (
	"encoding/json"
	"fmt"
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

func TestDDContainerLogStreamMessageJSONRoundTrips(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value any
		new   func() any
	}{
		{
			name: "chunk",
			value: DDContainerLogChunkMessage{
				RequestID: "stream-1", ContainerID: "container-1", Stream: "stderr", Logs: "failed\n",
			},
			new: func() any { return &DDContainerLogChunkMessage{} },
		},
		{
			name: "end",
			value: DDContainerLogEndMessage{
				RequestID: "stream-1", ContainerID: "container-1", Reason: "eof",
			},
			new: func() any { return &DDContainerLogEndMessage{} },
		},
		{
			name: "error",
			value: DDContainerLogErrorMessage{
				RequestID: "stream-1", ContainerID: "container-1", Error: "unavailable",
			},
			new: func() any { return &DDContainerLogErrorMessage{} },
		},
		{
			name: "cancel",
			value: DDContainerLogCancelMessage{
				RequestID: "stream-1", ContainerID: "container-1",
			},
			new: func() any { return &DDContainerLogCancelMessage{} },
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			encoded, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !strings.Contains(string(encoded), `"requestId":"stream-1"`) ||
				!strings.Contains(string(encoded), `"containerId":"container-1"`) {
				t.Fatalf("json = %s, want request and container IDs", encoded)
			}

			decoded := tc.new()
			if err := json.Unmarshal(encoded, decoded); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			roundTrip, err := json.Marshal(decoded)
			if err != nil {
				t.Fatalf("marshal round trip: %v", err)
			}
			if string(roundTrip) != string(encoded) {
				t.Fatalf("round trip = %s, want %s", roundTrip, encoded)
			}
		})
	}
}

func TestTimeoutHintDecodesLeniently(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   string
		want TimeoutHint
	}{
		{`60000`, TimeoutHint{Ms: 60000, Valid: true}},
		{` 7 `, TimeoutHint{Ms: 7, Valid: true}},
		{`null`, TimeoutHint{}},
		{``, TimeoutHint{}},
		{`"5000"`, TimeoutHint{}},
		{`1.5`, TimeoutHint{}},
		{`99999999999999999999`, TimeoutHint{}},
	} {
		got := TimeoutHint{Ms: 1, Valid: true}
		if err := got.UnmarshalJSON([]byte(tc.in)); err != nil {
			t.Fatalf("UnmarshalJSON(%q) = %v, want no error", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("UnmarshalJSON(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

func TestRequestMessageTimeoutHintRoundTrip(t *testing.T) {
	t.Parallel()
	out, err := json.Marshal(RequestMessage{RequestID: "r", Method: "GET", Path: "/x", TimeoutMs: &TimeoutHint{Ms: 4500, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"timeoutMs":4500`) {
		t.Fatalf("hint not marshaled as a bare number: %s", out)
	}
	var back RequestMessage
	if err := json.Unmarshal([]byte(`{"requestId":"r","timeoutMs":"junk"}`), &back); err != nil {
		t.Fatalf("a malformed hint must not reject the request: %v", err)
	}
	if back.TimeoutMs == nil || back.TimeoutMs.Valid {
		t.Fatalf("malformed hint = %+v, want present but not valid", back.TimeoutMs)
	}
}

func TestEnrollBlockNeverPrintsCode(t *testing.T) {
	t.Parallel()
	e := EnrollBlock{Code: "super-secret-enroll-code", PublicKey: "k"}
	for _, got := range []string{e.String(), e.GoString(), fmt.Sprintf("%v %+v %#v", e, e, e)} {
		if strings.Contains(got, e.Code) {
			t.Fatalf("code leaked: %q", got)
		}
	}
}
