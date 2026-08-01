package drydock

import (
	"testing"

	"github.com/codeswhat/portwing/internal/protocol"
)

func TestGetWatcherComponentsReturnsProtocolDescriptors(t *testing.T) {
	// Compile-time guard: the drydock API must expose protocol descriptors directly.
	//nolint:staticcheck // ST1023: explicit type annotation is the point of this guard.
	var components []protocol.ComponentDescriptor = GetWatcherComponents()
	if len(components) == 0 {
		t.Fatalf("expected at least one watcher component")
	}

	want := map[string]any{
		"transport": "docker-api",
		"execution": "controller",
		"events":    "portwing",
	}
	for key, wantValue := range want {
		if got := components[0].Configuration[key]; got != wantValue {
			t.Errorf("watcher configuration %q = %#v, want %#v", key, got, wantValue)
		}
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
