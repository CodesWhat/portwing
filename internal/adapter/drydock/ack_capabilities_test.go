// SPDX-License-Identifier: MIT

package drydock

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/codeswhat/portwing/internal/protocol"
)

func TestBuildAckPayloadAdvertisesCapabilities(t *testing.T) {
	b := NewSSEBroadcaster(fakeContainerProvider{}, "v-test", AgentInfo{})

	var payload ackPayload
	if err := json.Unmarshal(b.buildAckPayload(), &payload); err != nil {
		t.Fatalf("unmarshal ack payload: %v", err)
	}
	if !slices.Contains(payload.Data.Capabilities, protocol.CapRequestTimeout) {
		t.Fatalf("dd:ack capabilities %v missing %q", payload.Data.Capabilities, protocol.CapRequestTimeout)
	}
}

// dd:ack must carry the same capability set a Standard /_portwing/info
// response carries: the shared standard list followed by the adapter's own.
func TestBuildAckPayloadCapabilitiesMatchInfoList(t *testing.T) {
	b := NewSSEBroadcaster(fakeContainerProvider{}, "v-test", AgentInfo{})

	var payload ackPayload
	if err := json.Unmarshal(b.buildAckPayload(), &payload); err != nil {
		t.Fatalf("unmarshal ack payload: %v", err)
	}
	want := protocol.MergeCapabilities(protocol.StandardCapabilities(), capabilities())
	if !slices.Equal(payload.Data.Capabilities, want) {
		t.Fatalf("dd:ack capabilities = %v, want %v", payload.Data.Capabilities, want)
	}
	for _, c := range []string{"compose", "exec", "metrics", "events"} {
		if !slices.Contains(payload.Data.Capabilities, c) {
			t.Fatalf("dd:ack capabilities %v missing %q", payload.Data.Capabilities, c)
		}
	}
}
