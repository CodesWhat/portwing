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
