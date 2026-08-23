package docker

import "testing"

func TestIsStreamingPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "container logs", path: "/v1.44/containers/abc/logs", want: true},
		{name: "logs with query", path: "/v1.44/containers/abc/logs?follow=1", want: true},
		{name: "attach", path: "/v1.44/containers/abc/attach", want: true},
		{name: "events", path: "/v1.44/events", want: true},
		{name: "build", path: "/v1.44/build", want: true},
		{name: "images create", path: "/v1.44/images/create?fromImage=nginx", want: true},
		{name: "images push", path: "/v1.44/images/push?name=nginx", want: true},
		{name: "exec start", path: "/v1.44/exec/abc/start", want: true},
		{name: "non-stream endpoint", path: "/v1.44/containers/json", want: false},
		{name: "exec inspect not stream", path: "/v1.44/exec/abc/json", want: false},

		// Large-body export endpoints: docker save (single and multi-image)
		// and docker export (container filesystem tar) must stream instead
		// of buffering, both to avoid the 100MB memory spike and because the
		// body is a binary tar, not the JSON these responses get wrapped as
		// on the non-streaming path.
		{name: "container export", path: "/v1.44/containers/abc/export", want: true},
		{name: "images get (single, named)", path: "/v1.44/images/nginx/get", want: true},
		{name: "images get (single, namespaced repo)", path: "/v1.44/images/library%2Fnginx/get", want: true},
		{name: "images get (multi-image)", path: "/v1.44/images/get?names=nginx&names=alpine", want: true},

		// Near misses: paths that share a segment with the export endpoints
		// above but are not themselves streaming responses.
		{name: "images json is not an export", path: "/v1.44/images/json", want: false},
		{name: "image inspect is not an export", path: "/v1.44/images/nginx/json", want: false},
		{name: "container archive is not an export", path: "/v1.44/containers/abc/archive", want: false},
		{name: "images load is not an export", path: "/v1.44/images/load", want: false},
		{name: "bare /get without /images/ does not match", path: "/v1.44/secrets/abc/get", want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsStreamingPath(tt.path); got != tt.want {
				t.Fatalf("IsStreamingPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
