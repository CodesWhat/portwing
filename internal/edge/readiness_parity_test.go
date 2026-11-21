// SPDX-License-Identifier: MIT

package edge

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codeswhat/portwing/internal/docker"
)

// parityDockerClient serves a daemon on a Unix socket that denies every
// versioned path (as a socket proxy that only allows /_ping would) and answers
// the unversioned /_ping with pingStatus.
func parityDockerClient(t *testing.T, pingStatus int) *docker.Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "lk")
	if err != nil {
		t.Fatalf("mkdirtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_ping" {
			w.WriteHeader(pingStatus)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v") {
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		http.NotFound(w, r)
	}))
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)

	c, err := docker.NewClient(sock, 5)
	if err != nil {
		t.Fatalf("docker.NewClient: %v", err)
	}
	return c
}

// TestDockerReadyMatchesStandardReadiness pins edge readiness to Standard
// mode's: the unversioned /_ping, 200 only. The expectations are the same
// table internal/server's TestReadinessParityWithEdge asserts.
func TestDockerReadyMatchesStandardReadiness(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
		want   bool
	}{
		{"200 with versioned ping denied", http.StatusOK, true},
		{"204 is not ready", http.StatusNoContent, false},
		{"299 is not ready", 299, false},
		{"500 is not ready", http.StatusInternalServerError, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, _ := newTestClient(t)
			c.dockerClient = parityDockerClient(t, tc.status)
			if got := c.dockerReady(context.Background()); got != tc.want {
				t.Errorf("dockerReady() with /_ping %d = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}
