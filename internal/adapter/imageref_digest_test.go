// SPDX-License-Identifier: MIT

package adapter

import (
	"strings"
	"testing"
)

func TestParseImageRefWithDigest(t *testing.T) {
	t.Parallel()
	digest := "@sha256:" + strings.Repeat("a", 64)
	for _, tc := range []struct{ name, input, registry, repository, tag string }{
		{"lscr tagged", "lscr.io/linuxserver/jackett:latest", "lscr.io", "linuxserver/jackett", "latest"},
		{"ghcr tagged", "ghcr.io/owner/image:v1.7", "ghcr.io", "owner/image", "v1.7"},
		{"docker hub tagged", "nginx:1.27", "docker.io", "nginx", "1.27"},
		{"docker hub digest only", "nginx", "docker.io", "nginx", "latest"},
		{"registry port tagged", "registry.example.com:5000/org/image:stable", "registry.example.com:5000", "org/image", "stable"},
		{"registry port digest only", "localhost:5000/org/image", "localhost:5000", "org/image", "latest"},
		{"namespace digest only", "library/nginx", "docker.io", "library/nginx", "latest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			registry, repository, tag := ParseImageRef(tc.input + digest)
			if registry != tc.registry || repository != tc.repository || tag != tc.tag {
				t.Fatalf("got (%q, %q, %q), want (%q, %q, %q)", registry, repository, tag, tc.registry, tc.repository, tc.tag)
			}
		})
	}
}
