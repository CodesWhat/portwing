package adapter

// containers_mutant_test.go adds tests that target Gremlins mutants surviving
// in containers.go: conditions existing tests exercised but only ever from one
// side, so negating the comparison changed nothing they looked at.

import (
	"testing"

	"github.com/codeswhat/portwing/internal/docker"
)

// TestToContainerDefaultsWatcherOnlyWhenTheParserLeavesItEmpty pins both sides
// of the `lr.Watcher == ""` fallback in toContainer, killing the
// CONDITIONALS_NEGATION mutant at containers.go:249:16. Negated, the fallback
// overwrites a watcher the label parser did supply with "docker" and leaves an
// unset one empty, so each row fails on its own.
func TestToContainerDefaultsWatcherOnlyWhenTheParserLeavesItEmpty(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		parsed string
		want   string
	}{
		{name: "unset falls back to docker", parsed: "", want: "docker"},
		{name: "parser-supplied watcher is kept", parsed: "swarm", want: "swarm"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			manager := NewContainerManager(nil, "test-agent", func(map[string]string) LabelResult {
				return LabelResult{Watcher: tt.parsed}
			})

			inspect := &docker.ContainerInspect{
				ID:     "cid",
				Name:   "/web",
				Config: docker.ContainerConfig{Image: "nginx:latest"},
			}
			listEntry := &docker.ContainerJSON{ID: "cid", Image: "nginx:latest", ImageID: "sha256:abc"}

			if got := manager.toContainer(inspect, listEntry).Watcher; got != tt.want {
				t.Fatalf("Watcher = %q, want %q", got, tt.want)
			}
		})
	}
}
