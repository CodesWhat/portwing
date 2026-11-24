// SPDX-License-Identifier: MIT

package server

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/adapter"
	"github.com/codeswhat/portwing/internal/adapter/drydock"
	"github.com/codeswhat/portwing/internal/docker"
	"github.com/codeswhat/portwing/internal/generic"
)

// TestShutdownEndsLongLivedStreams holds open an SSE events stream, a follow
// log stream and the Drydock /api/events SSE against a real listener, then
// calls Shutdown with a 10 s deadline. Shutdown must return promptly, every
// stream must end cleanly, and the audit sink must be closed.
func TestShutdownEndsLongLivedStreams(t *testing.T) {
	for _, tc := range []struct {
		name    string
		adapter func(*docker.Client) adapter.ServerAdapter
		path    string
	}{
		{"generic events SSE", func(c *docker.Client) adapter.ServerAdapter { return generic.New(c, "test") }, "/api/v1/events"},
		{"follow logs", func(c *docker.Client) adapter.ServerAdapter {
			return drydock.NewAdapter(c, "test", drydock.AgentInfo{})
		}, "/api/containers/abc123/logs?follow=true"},
		{"drydock events SSE", func(c *docker.Client) adapter.ServerAdapter {
			return drydock.NewAdapter(c, "test", drydock.AgentInfo{})
		}, "/api/events"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sockPath, cleanupSocket := shortSocketPath(t)
			listener := newUnixListener(t, sockPath)
			daemonDone := make(chan struct{})
			daemon := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/logs") && !strings.HasSuffix(r.URL.Path, "/events") {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"Version":"26.0.0","ApiVersion":"1.44"}`)
					return
				}
				w.WriteHeader(http.StatusOK)
				if strings.HasSuffix(r.URL.Path, "/logs") {
					// One multiplexed stdout frame so Portwing sends response headers.
					_, _ = w.Write([]byte{1, 0, 0, 0, 0, 0, 0, 3, 'h', 'i', '\n'})
				}
				w.(http.Flusher).Flush()
				select {
				case <-r.Context().Done():
				case <-daemonDone:
				}
			})}
			go func() { _ = daemon.Serve(listener) }()
			t.Cleanup(func() {
				close(daemonDone)
				_ = daemon.Close()
				cleanupSocket()
			})

			client, err := docker.NewClient(sockPath, 5)
			if err != nil {
				t.Fatalf("docker.NewClient: %v", err)
			}
			cfg := minimalConfig()
			cfg.AllowUnauthenticated = false
			cfg.Token = "stream-secret" //nolint:gosec // Test-only request credential.
			s, err := NewServer(cfg, client, tc.adapter(client))
			if err != nil {
				t.Fatalf("NewServer: %v", err)
			}
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			serveDone := make(chan error, 1)
			go func() { serveDone <- s.httpServer.Serve(ln) }()

			reqCtx, reqCancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer reqCancel()
			req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, "http://"+ln.Addr().String()+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set(headerPortwingToken, "stream-secret")
			resp, err := (&http.Client{}).Do(req)
			if err != nil {
				t.Fatalf("open stream: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("stream status = %d, want 200", resp.StatusCode)
			}
			// Headers are in, so the handler is live; Drydock also sends an ack frame.
			br := bufio.NewReader(resp.Body)
			if strings.HasPrefix(tc.name, "drydock") {
				line, err := br.ReadString('\n')
				if err != nil || !strings.HasPrefix(line, "data:") && !strings.HasPrefix(line, ":") {
					t.Fatalf("first SSE line = %q, err %v", line, err)
				}
			}

			bodyDone := make(chan error, 1)
			go func() {
				_, readErr := io.Copy(io.Discard, br)
				bodyDone <- readErr
			}()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			start := time.Now()
			if err := s.Shutdown(ctx); err != nil {
				t.Fatalf("Shutdown: %v (after %s)", err, time.Since(start))
			}
			if elapsed := time.Since(start); elapsed > 2*time.Second {
				t.Fatalf("Shutdown took %s with an open stream, want under 2s", elapsed)
			}

			select {
			case readErr := <-bodyDone:
				if readErr != nil {
					t.Fatalf("stream did not end cleanly: %v", readErr)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("stream still open after Shutdown")
			}
			select {
			case serveErr := <-serveDone:
				if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
					t.Fatalf("Serve: %v", serveErr)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Serve did not return after Shutdown")
			}

			// Audit close ran: the Once is already spent, so this func never fires.
			ranNow := false
			s.auditCloseOnce.Do(func() { ranNow = true })
			if ranNow {
				t.Fatal("audit logger was not closed by Shutdown")
			}
		})
	}
}
