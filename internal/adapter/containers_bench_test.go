package adapter

import "testing"

// BenchmarkParseImageRef measures Docker image-reference parsing, which runs
// once per container on every inventory refresh. It's a fuzz target and a pure
// string-splitting hot path, so we track it across the common reference shapes.
func BenchmarkParseImageRef(b *testing.B) {
	cases := []struct {
		name string
		ref  string
	}{
		{"bare_name", "nginx"},
		{"name_tag", "nginx:latest"},
		{"registry_org_tag", "registry.example.com/org/image:1.2.3"},
		{"ghcr", "ghcr.io/owner/repo:v1.2"},
		{"registry_port", "localhost:5000/team/app:dev"},
		{"digest", "ubuntu@sha256:cafebabe"},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, _, _ = ParseImageRef(c.ref)
			}
		})
	}
}
