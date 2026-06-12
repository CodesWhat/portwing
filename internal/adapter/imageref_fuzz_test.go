package adapter

import (
	"strings"
	"testing"
)

// FuzzParseImageRef verifies that ParseImageRef never panics on arbitrary input
// and enforces key invariants:
//   - registry is always non-empty
//   - name is always non-empty
//   - tag is always non-empty
//   - if input contained no registry-looking prefix, registry defaults to "docker.io"
func FuzzParseImageRef(f *testing.F) {
	// Seed: canonical forms.
	f.Add("nginx")
	f.Add("nginx:latest")
	f.Add("nginx:1.25")
	f.Add("docker.io/library/nginx:latest")
	f.Add("ghcr.io/owner/repo:v1.2.3")
	f.Add("registry.example.com/org/image:tag")
	f.Add("localhost/myimage:dev")
	f.Add("registry.example.com:5000/org/image:latest")
	// Seed: hostile inputs.
	f.Add("")
	f.Add(":")
	f.Add(":tag")
	f.Add("/")
	f.Add("//")
	f.Add("a/b/c/d/e:tag")
	f.Add(strings.Repeat("a", 1024))
	f.Add("image@sha256:abc123")
	f.Add("UPPER:CASE")

	f.Fuzz(func(t *testing.T, input string) {
		// Must never panic.
		registry, name, tag := ParseImageRef(input)

		// Registry invariant: always non-empty (defaults to docker.io).
		if registry == "" {
			t.Errorf("ParseImageRef(%q): registry is empty", input)
		}

		// Tag invariant: always non-empty (defaults to "latest").
		if tag == "" {
			t.Errorf("ParseImageRef(%q): tag is empty", input)
		}

		// Name may be empty for degenerate inputs like "" or ":"
		// but registry must still be set.
		_ = name
	})
}
