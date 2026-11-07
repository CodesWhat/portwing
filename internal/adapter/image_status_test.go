// SPDX-License-Identifier: MIT

package adapter

import (
	"testing"

	"github.com/codeswhat/portwing/internal/docker"
)

func TestParseImageRef(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		imageRef     string
		wantRegistry string
		wantName     string
		wantTag      string
	}{
		{
			name:         "explicit registry and tag",
			imageRef:     "registry.example.com/org/image:tag",
			wantRegistry: "registry.example.com",
			wantName:     "org/image",
			wantTag:      "tag",
		},
		{
			name:         "implicit docker.io with explicit tag",
			imageRef:     "nginx:latest",
			wantRegistry: "docker.io",
			wantName:     "nginx",
			wantTag:      "latest",
		},
		{
			name:         "implicit docker.io and implicit latest tag",
			imageRef:     "nginx",
			wantRegistry: "docker.io",
			wantName:     "nginx",
			wantTag:      "latest",
		},
		{
			name:         "ghcr registry namespace and tag",
			imageRef:     "ghcr.io/owner/repo:v1.2",
			wantRegistry: "ghcr.io",
			wantName:     "owner/repo",
			wantTag:      "v1.2",
		},
		{
			name:         "registry with port and no tag",
			imageRef:     "localhost:5000/org/api",
			wantRegistry: "localhost:5000",
			wantName:     "org/api",
			wantTag:      "latest",
		},
		{
			name:         "localhost registry without port",
			imageRef:     "localhost/worker",
			wantRegistry: "localhost",
			wantName:     "worker",
			wantTag:      "latest",
		},
		{
			name:         "namespace without explicit registry",
			imageRef:     "library/redis:7",
			wantRegistry: "docker.io",
			wantName:     "library/redis",
			wantTag:      "7",
		},
		{
			name:         "tag pinned by digest",
			imageRef:     "lscr.io/linuxserver/jackett:latest@sha256:ed134f44644d26c1c1f30f973580cadf6d2f79db6919db961bdd57a633387750",
			wantRegistry: "lscr.io",
			wantName:     "linuxserver/jackett",
			wantTag:      "latest",
		},
		{
			name:         "digest only keeps default latest tag",
			imageRef:     "ghcr.io/owner/repo@sha256:ed134f44644d26c1c1f30f973580cadf6d2f79db6919db961bdd57a633387750",
			wantRegistry: "ghcr.io",
			wantName:     "owner/repo",
			wantTag:      "latest",
		},
		{
			name:         "implicit docker.io with tag pinned by digest",
			imageRef:     "nginx:1.25@sha256:ed134f44644d26c1c1f30f973580cadf6d2f79db6919db961bdd57a633387750",
			wantRegistry: "docker.io",
			wantName:     "nginx",
			wantTag:      "1.25",
		},
		{
			name:         "registry port with tag pinned by digest",
			imageRef:     "localhost:5000/org/api:v2@sha256:ed134f44644d26c1c1f30f973580cadf6d2f79db6919db961bdd57a633387750",
			wantRegistry: "localhost:5000",
			wantName:     "org/api",
			wantTag:      "v2",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotRegistry, gotName, gotTag := ParseImageRef(tt.imageRef)
			if gotRegistry != tt.wantRegistry || gotName != tt.wantName || gotTag != tt.wantTag {
				t.Fatalf(
					"ParseImageRef(%q) = (%q, %q, %q), want (%q, %q, %q)",
					tt.imageRef, gotRegistry, gotName, gotTag, tt.wantRegistry, tt.wantName, tt.wantTag,
				)
			}
		})
	}
}

func TestContainerStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state docker.ContainerState
		want  string
	}{
		{
			name:  "running",
			state: docker.ContainerState{Running: true},
			want:  "running",
		},
		{
			name:  "paused",
			state: docker.ContainerState{Paused: true},
			want:  "paused",
		},
		{
			name:  "restarting",
			state: docker.ContainerState{Running: true, Restarting: true},
			want:  "restarting",
		},
		{
			name:  "dead",
			state: docker.ContainerState{Dead: true},
			want:  "dead",
		},
		{
			name:  "created",
			state: docker.ContainerState{Status: "created"},
			want:  "created",
		},
		{
			name:  "default stopped",
			state: docker.ContainerState{Status: "exited"},
			want:  "stopped",
		},
		{
			name:  "paused takes precedence over running",
			state: docker.ContainerState{Running: true, Paused: true},
			want:  "paused",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := ContainerStatus(&tt.state)
			if got != tt.want {
				t.Fatalf("ContainerStatus(%+v) = %q, want %q", tt.state, got, tt.want)
			}
		})
	}
}
