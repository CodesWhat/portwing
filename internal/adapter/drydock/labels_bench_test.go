package drydock

import "testing"

// BenchmarkParseLabels measures extraction of the Drydock-specific labels from a
// container's label map, which runs once per container on every sync. The "full"
// case exercises a label set padded with unrelated keys, the realistic shape on
// a busy host.
func BenchmarkParseLabels(b *testing.B) {
	empty := map[string]string{}

	full := map[string]string{
		LabelDisplayName:  "web frontend",
		LabelDisplayIcon:  "mdi:web",
		LabelTagInclude:   "^v\\d+",
		LabelTagExclude:   "rc|beta",
		LabelTagTransform: "$1",
		LabelWatch:        "true",
		// Unrelated labels a real container carries alongside the dd.* ones.
		"com.docker.compose.project": "portwing",
		"com.docker.compose.service": "web",
		"org.opencontainers.version": "1.2.3",
		"maintainer":                 "ops@example.com",
	}

	cases := []struct {
		name   string
		labels map[string]string
	}{
		{"empty", empty},
		{"full", full},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = ParseLabels(c.labels)
			}
		})
	}
}
