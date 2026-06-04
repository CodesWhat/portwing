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
