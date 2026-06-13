package drydock

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/codeswhat/lookout/internal/adapter"
	"github.com/codeswhat/lookout/internal/config"
)

type fakeContainerProvider struct {
	containers []adapter.Container
}

func (f fakeContainerProvider) GetContainers() []adapter.Container {
	return f.containers
}

func TestBuildAckPayloadWithContainerProvider(t *testing.T) {
	provider := fakeContainerProvider{
		containers: []adapter.Container{
			{
				ID:     "c1",
				Status: "running",
				Image:  adapter.ContainerImage{ID: "img-a"},
			},
			{
				ID:     "c2",
				Status: "exited",
				Image:  adapter.ContainerImage{ID: "img-b"},
			},
			{
				ID:     "c3",
				Status: "paused",
				Image:  adapter.ContainerImage{ID: "img-b"},
			},
		},
	}

	b := NewSSEBroadcaster(provider, "v-test", nil)
	b.startTime = time.Now().Add(-3 * time.Second)

	var payload ackPayload
	if err := json.Unmarshal(b.buildAckPayload(), &payload); err != nil {
		t.Fatalf("unmarshal ack payload: %v", err)
	}

	if payload.Type != "dd:ack" {
		t.Fatalf("unexpected payload type: got %q want %q", payload.Type, "dd:ack")
	}
	if payload.Data.Version != "v-test" {
		t.Fatalf("unexpected version: got %q want %q", payload.Data.Version, "v-test")
	}
	if payload.Data.Containers.Total != 3 {
		t.Fatalf("unexpected total containers: got %d want %d", payload.Data.Containers.Total, 3)
	}
	if payload.Data.Containers.Running != 1 {
		t.Fatalf("unexpected running containers: got %d want %d", payload.Data.Containers.Running, 1)
	}
	if payload.Data.Containers.Stopped != 2 {
		t.Fatalf("unexpected stopped containers: got %d want %d", payload.Data.Containers.Stopped, 2)
	}
	if payload.Data.Images != 2 {
		t.Fatalf("unexpected image count: got %d want %d", payload.Data.Images, 2)
	}
	if payload.Data.UptimeSeconds < 3 {
		t.Fatalf("unexpected uptime: got %d want at least %d", payload.Data.UptimeSeconds, 3)
	}
	if payload.Data.LastSeen == "" {
		t.Fatalf("expected lastSeen to be populated")
	}
}

func TestBuildWatcherSnapshotPayload(t *testing.T) {
	provider := fakeContainerProvider{
		containers: []adapter.Container{
			{ID: "c1", Name: "app-1", Status: "running"},
			{ID: "c2", Name: "app-2", Status: "exited"},
		},
	}

	b := NewSSEBroadcaster(provider, "v-test", nil)

	raw, err := b.buildWatcherSnapshotPayload()
	if err != nil {
		t.Fatalf("build watcher snapshot payload: %v", err)
	}

	var payload struct {
		Type string `json:"type"`
		Data struct {
			Watcher struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"watcher"`
			Containers []adapter.Container `json:"containers"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal watcher snapshot payload: %v", err)
	}

	if payload.Type != "dd:watcher-snapshot" {
		t.Fatalf("unexpected payload type: got %q want %q", payload.Type, "dd:watcher-snapshot")
	}
	if payload.Data.Watcher.Type != "docker" || payload.Data.Watcher.Name != "docker" {
		t.Fatalf("unexpected watcher descriptor: got %+v", payload.Data.Watcher)
	}
	if len(payload.Data.Containers) != 2 {
		t.Fatalf("unexpected container count: got %d want %d", len(payload.Data.Containers), 2)
	}
	if payload.Data.Containers[0].ID != "c1" {
		t.Fatalf("unexpected first container: got %q want %q", payload.Data.Containers[0].ID, "c1")
	}
}

func TestBuildWatcherSnapshotPayloadEmptyInventory(t *testing.T) {
	b := NewSSEBroadcaster(fakeContainerProvider{}, "v-test", nil)

	raw, err := b.buildWatcherSnapshotPayload()
	if err != nil {
		t.Fatalf("build watcher snapshot payload: %v", err)
	}

	var payload struct {
		Data struct {
			Containers json.RawMessage `json:"containers"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal watcher snapshot payload: %v", err)
	}

	// Drydock expects a JSON array, never null.
	if string(payload.Data.Containers) != "[]" {
		t.Fatalf("expected empty containers array, got %s", payload.Data.Containers)
	}
}

func TestParseProcMeminfo(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantGB  float64
	}{
		{
			name: "16 GiB",
			content: `MemTotal:       16777216 kB
MemFree:         1234567 kB
MemAvailable:    9876543 kB
`,
			wantGB: 16.0,
		},
		{
			name: "8 GiB rounded",
			content: `MemTotal:        8388608 kB
MemFree:          500000 kB
`,
			wantGB: 8.0,
		},
		{
			name: "fractional — 1.5 GiB",
			content: `MemTotal:        1572864 kB
MemFree:          100000 kB
`,
			wantGB: 1.5,
		},
		{
			name:    "missing MemTotal",
			content: "MemFree: 1234 kB\n",
			wantGB:  0,
		},
		{
			name:    "malformed value",
			content: "MemTotal: NOTANUMBER kB\n",
			wantGB:  0,
		},
		{
			name:    "empty file",
			content: "",
			wantGB:  0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "meminfo")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatalf("write temp meminfo: %v", err)
			}
			got := parseProcMeminfo(path)
			if got != tc.wantGB {
				t.Fatalf("parseProcMeminfo(%q) = %v, want %v", tc.name, got, tc.wantGB)
			}
		})
	}
}

func TestParseProcMeminfoMissingFile(t *testing.T) {
	got := parseProcMeminfo("/nonexistent/path/meminfo")
	if got != 0 {
		t.Fatalf("expected 0 for missing file, got %v", got)
	}
}

func TestBuildAckPayloadLogLevelAndPollInterval(t *testing.T) {
	cfg := &config.Config{
		LogLevel:       "debug",
		DDPollInterval: 60,
	}
	b := NewSSEBroadcaster(fakeContainerProvider{}, "v-test", cfg)

	var payload ackPayload
	if err := json.Unmarshal(b.buildAckPayload(), &payload); err != nil {
		t.Fatalf("unmarshal ack payload: %v", err)
	}

	if payload.Data.LogLevel != "debug" {
		t.Fatalf("unexpected logLevel: got %q want %q", payload.Data.LogLevel, "debug")
	}
	if payload.Data.PollInterval != "60" {
		t.Fatalf("unexpected pollInterval: got %q want %q", payload.Data.PollInterval, "60")
	}
}

func TestBuildAckPayloadNilConfigNoFields(t *testing.T) {
	b := NewSSEBroadcaster(fakeContainerProvider{}, "v-test", nil)

	var payload ackPayload
	if err := json.Unmarshal(b.buildAckPayload(), &payload); err != nil {
		t.Fatalf("unmarshal ack payload: %v", err)
	}

	// nil config must produce empty strings, not a panic
	if payload.Data.LogLevel != "" {
		t.Fatalf("expected empty logLevel for nil config, got %q", payload.Data.LogLevel)
	}
	if payload.Data.PollInterval != "" {
		t.Fatalf("expected empty pollInterval for nil config, got %q", payload.Data.PollInterval)
	}
}
