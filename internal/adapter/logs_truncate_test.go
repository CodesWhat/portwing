// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/docker"
)

// newCustomLogDaemonClient points a docker client at a fake daemon whose
// container-logs endpoint runs logs. requestTimeout is the client's
// REQUEST_TIMEOUT in seconds.
func newCustomLogDaemonClient(t *testing.T, requestTimeout int, logs http.HandlerFunc) *docker.Client {
	t.Helper()

	socketPath := shortSocketPath(t)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on unix socket: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(docker.VersionResponse{Version: "26.0.0", APIVersion: "1.44"})
	})
	mux.HandleFunc("/v1.44/containers/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/logs") {
			http.NotFound(w, r)
			return
		}
		logs(w, r)
	})

	server := &http.Server{Handler: mux}
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = listener.Close()
		<-serverDone
	})

	client, err := docker.NewClient(socketPath, requestTimeout)
	if err != nil {
		t.Fatalf("new docker client: %v", err)
	}
	return client
}

func TestServeContainerLogsNonFollowOutlivesRequestTimeout(t *testing.T) {
	t.Parallel()

	const lines = 4
	// One-second REQUEST_TIMEOUT against a daemon that dribbles for about two.
	client := newCustomLogDaemonClient(t, 1, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.docker.multiplexed-stream")
		for i := range lines {
			_, _ = w.Write(dockerLogFrame(1, []byte("line "+strconv.Itoa(i)+"\n")))
			w.(http.Flusher).Flush()
			time.Sleep(550 * time.Millisecond)
		}
	})

	rec := httptest.NewRecorder()
	start := time.Now()
	ServeContainerLogs(rec, logRequest("/logs"), ContainerLogOptions{Client: client})

	if elapsed := time.Since(start); elapsed < 1500*time.Millisecond {
		t.Fatalf("read finished in %v, test did not run past the request timeout", elapsed)
	}
	want := "line 0\nline 1\nline 2\nline 3\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("body = %q, want the full log %q", got, want)
	}
}

func TestServeContainerLogsMidBodyFailureAbortsResponse(t *testing.T) {
	t.Parallel()

	for _, follow := range []bool{false, true} {
		name := "non-follow"
		target := "/logs"
		if follow {
			name = "follow"
			target = "/logs?follow=1"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Declare more body than is sent, then drop the connection.
			client := newCustomLogDaemonClient(t, 5, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/vnd.docker.multiplexed-stream")
				w.Header().Set("Content-Length", "1000")
				_, _ = w.Write(dockerLogFrame(1, []byte("before the failure\n")))
			})

			rec := httptest.NewRecorder()
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				ServeContainerLogs(rec, logRequest(target), ContainerLogOptions{Client: client})
			}()

			if recovered != http.ErrAbortHandler { //nolint:errorlint // recover() yields the exact sentinel net/http recognizes.
				t.Fatalf("recovered = %v, want http.ErrAbortHandler so the client sees a failed transfer, not a clean 200 (body %q)", recovered, rec.Body.String())
			}
		})
	}
}

func TestServeContainerLogsNonFollowStallAbortsResponse(t *testing.T) {
	t.Parallel()

	// A daemon that sends some bytes and then goes quiet, against a
	// one-second REQUEST_TIMEOUT.
	handlerDone := make(chan struct{})
	client := newCustomLogDaemonClient(t, 1, func(w http.ResponseWriter, r *http.Request) {
		defer close(handlerDone)
		w.Header().Set("Content-Type", "application/vnd.docker.multiplexed-stream")
		_, _ = w.Write(dockerLogFrame(1, []byte("before the stall\n")))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})

	rec := httptest.NewRecorder()
	var recovered any
	start := time.Now()
	func() {
		defer func() { recovered = recover() }()
		ServeContainerLogs(rec, logRequest("/logs"), ContainerLogOptions{Client: client})
	}()

	if recovered != http.ErrAbortHandler { //nolint:errorlint // recover() yields the exact sentinel net/http recognizes.
		t.Fatalf("recovered = %v, want http.ErrAbortHandler (body %q)", recovered, rec.Body.String())
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("stalled read took %v, want about the 1s REQUEST_TIMEOUT", elapsed)
	}
	select {
	case <-handlerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("daemon connection was not released after the stall")
	}
}
