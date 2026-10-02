package drydock

// wire_mutant_test.go adds tests that target Gremlins mutants surviving in
// wire.go.

import (
	"testing"

	"github.com/codeswhat/portwing/internal/adapter"
)

// TestToDrydockContainerDefaultsRegistryURLOnlyWhenUnset pins both sides of
// the `registryURL == ""` fallback in toDrydockContainer, killing the
// CONDITIONALS_NEGATION mutant at wire.go:96:17. Negated, the fallback
// rewrites every real registry to docker.io and leaves an unset one empty.
// The existing wire test only ever passes docker.io, which reads the same
// through either branch.
func TestToDrydockContainerDefaultsRegistryURLOnlyWhenUnset(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		registry string
		want     string
	}{
		{name: "unset falls back to docker.io", registry: "", want: "docker.io"},
		{name: "a real registry is kept", registry: "ghcr.io", want: "ghcr.io"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			wire := toDrydockContainer(adapter.Container{
				ID:    "container-1",
				Image: adapter.ContainerImage{Registry: tt.registry, Name: "owner/app", Tag: "1.0.0"},
			})

			if got := wire.Image.Registry.URL; got != tt.want {
				t.Fatalf("registry url = %q, want %q", got, tt.want)
			}
		})
	}
}
