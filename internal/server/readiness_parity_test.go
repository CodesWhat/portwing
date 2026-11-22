// SPDX-License-Identifier: MIT

package server

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/docker"
)

// TestReadinessParityWithEdge is Standard mode's half of the readiness parity
// pair: internal/edge's TestDockerReadyMatchesStandardReadiness asserts the
// same table. The daemon denies every versioned path and answers the
// unversioned /_ping with the given status; only a 200 is ready.
func TestReadinessParityWithEdge(t *testing.T) {
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
			dir, err := os.MkdirTemp("", "lk")
			if err != nil {
				t.Fatalf("mkdirtemp: %v", err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			sock := filepath.Join(dir, "d.sock")
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/_ping":
					w.WriteHeader(tc.status)
				case strings.HasPrefix(r.URL.Path, "/v"):
					http.Error(w, "denied", http.StatusForbidden)
				default:
					http.NotFound(w, r)
				}
			}))
			l, err := net.Listen("unix", sock)
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			srv.Listener = l
			srv.Start()
			t.Cleanup(srv.Close)
			client, err := docker.NewClient(sock, 5)
			if err != nil {
				t.Fatalf("docker.NewClient: %v", err)
			}

			s := &Server{dockerClient: client, startTime: time.Now()}
			rr := httptest.NewRecorder()
			s.handleHealth(rr, httptest.NewRequest(http.MethodGet, "/ready", nil))
			if got := rr.Code == http.StatusOK; got != tc.want {
				t.Errorf("/ready with /_ping %d = %d, ready=%v want %v", tc.status, rr.Code, got, tc.want)
			}
		})
	}
}
