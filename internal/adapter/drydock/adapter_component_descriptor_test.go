package drydock

import (
	"testing"

	"github.com/codeswhat/lookout/internal/protocol"
)

func TestGetWatcherComponentsReturnsProtocolDescriptors(t *testing.T) {
	components := GetWatcherComponents()

	// Compile-time guard: the drydock API should expose protocol descriptors directly.
	var typed []protocol.ComponentDescriptor = components
	if len(typed) == 0 {
		t.Fatalf("expected at least one watcher component")
	}
}

func TestGetTriggerComponentsReturnsProtocolDescriptors(t *testing.T) {
	components := GetTriggerComponents()

	// Compile-time guard: the drydock API should expose protocol descriptors directly.
	var typed []protocol.ComponentDescriptor = components
	if typed == nil {
		t.Fatalf("expected non-nil trigger slice")
	}
}
