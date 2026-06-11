package drydock

import (
	"testing"

	"github.com/codeswhat/lookout/internal/protocol"
)

func TestGetWatcherComponentsReturnsProtocolDescriptors(t *testing.T) {
	// Compile-time guard: the drydock API must expose protocol descriptors directly.
	//nolint:staticcheck // ST1023: explicit type annotation is the point of this guard.
	var components []protocol.ComponentDescriptor = GetWatcherComponents()
	if len(components) == 0 {
		t.Fatalf("expected at least one watcher component")
	}
}

func TestGetTriggerComponentsReturnsProtocolDescriptors(t *testing.T) {
	// Compile-time guard: the drydock API must expose protocol descriptors directly.
	//nolint:staticcheck // ST1023: explicit type annotation is the point of this guard.
	var components []protocol.ComponentDescriptor = GetTriggerComponents()
	if components == nil {
		t.Fatalf("expected non-nil trigger slice")
	}
}
