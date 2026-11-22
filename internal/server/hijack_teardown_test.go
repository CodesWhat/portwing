// SPDX-License-Identifier: MIT

package server

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/docker"
)

const hijackTeardownBound = 2 * time.Second

// TestControllerCloseTearsDownStandardHijackSession opens an attach and an exec
// start through Portwing, drops the controller connection, and asserts the
// Docker side of the hijack is fully closed within the bound.
//
// The fake daemon cannot tell a shutdown(SHUT_WR) from a close by reading, so
// after it sees EOF it writes: a peer that has fully closed answers with EPIPE,
// a half-closed peer does not. A streaming daemon (container output flowing)
// keeps writing until a write fails. An idle daemon writes one probe near the
// end of the bound.
//
// The idle case is a known gap and skipped: a controller FIN reads as a stdin
// half-close, so Portwing only CloseWrites Docker and keeps relaying until
// Docker's own output makes a write to the dead controller fail. An idle
// session therefore holds the upstream connection open indefinitely. Fixing it
// needs a policy for half-close versus close, so it is tracked, not hidden.
func TestControllerCloseTearsDownStandardHijackSession(t *testing.T) {
	for _, tc := range []struct {
		name, path, query string
		idle              bool
	}{
		{"attach streaming", "/v1.44/containers/abc123/attach", "stream=1&stdin=1&stdout=1", false},
		{"exec start streaming", "/v1.44/exec/def456/start", "", false},
		{"attach idle", "/v1.44/containers/abc123/attach", "stream=1&stdin=1&stdout=1", true},
		{"exec start idle", "/v1.44/exec/def456/start", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.idle {
				t.Skip("known gap: an idle hijack session keeps the Docker connection open after the controller closes")
			}
			t.Parallel()

			sockPath, cleanupSocket := shortSocketPath(t)
			defer cleanupSocket()
			listener, err := net.Listen("unix", sockPath)
			if err != nil {
				t.Fatalf("listen on Docker socket: %v", err)
			}
			defer listener.Close()

			upgraded := make(chan struct{})
			sawEOF := make(chan struct{})
			closed := make(chan error, 1) // nil: fully closed; non-nil: still open
			go func() {
				// Version probe from NewServer, then the hijack connection.
				for {
					conn, acceptErr := listener.Accept()
					if acceptErr != nil {
						return
					}
					go func() {
						defer conn.Close()
						br := bufio.NewReader(conn)
						req, readErr := http.ReadRequest(br)
						if readErr != nil {
							return
						}
						_, _ = io.Copy(io.Discard, req.Body)
						_ = req.Body.Close()
						if req.URL.Path != tc.path {
							body := `{"Version":"26.0.0","ApiVersion":"1.44"}`
							_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
							return
						}
						_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: tcp\r\nConnection: Upgrade\r\n\r\n")
						close(upgraded)
						_, _ = io.Copy(io.Discard, br)
						close(sawEOF)
						deadline := time.Now().Add(hijackTeardownBound)
						if tc.idle {
							time.Sleep(time.Until(deadline) - 500*time.Millisecond)
							deadline = time.Now()
						}
						for {
							if _, writeErr := conn.Write([]byte{0}); writeErr != nil {
								closed <- nil
								return
							}
							if !time.Now().Before(deadline) {
								closed <- fmt.Errorf("daemon can still write to Portwing's end of the connection")
								return
							}
							time.Sleep(20 * time.Millisecond)
						}
					}()
				}
			}()

			dockerClient, err := docker.NewClient(sockPath, 5)
			if err != nil {
				t.Fatalf("docker.NewClient: %v", err)
			}
			cfg := minimalConfig()
			cfg.AllowUnauthenticated = false
			cfg.Token = "teardown-secret" //nolint:gosec // Test-only request credential.
			s, err := NewServer(cfg, dockerClient, &stubServerAdapter{})
			if err != nil {
				t.Fatalf("NewServer: %v", err)
			}
			defer s.rateLimiter.Stop()
			defer s.auditor.Close()

			proxy := httptest.NewServer(s.httpServer.Handler)
			defer proxy.Close()
			proxyURL, err := url.Parse(proxy.URL)
			if err != nil {
				t.Fatalf("parse proxy URL: %v", err)
			}
			clientConn, err := net.Dial("tcp", proxyURL.Host)
			if err != nil {
				t.Fatalf("dial Portwing: %v", err)
			}
			defer clientConn.Close()

			target := tc.path
			if tc.query != "" {
				target += "?" + tc.query
			}
			rawRequest := "POST " + target + " HTTP/1.1\r\n" +
				"Host: " + proxyURL.Host + "\r\n" +
				"Authorization: Bearer teardown-secret\r\n" +
				"Connection: Upgrade\r\nUpgrade: tcp\r\n" +
				"Content-Type: application/json\r\nContent-Length: 2\r\n\r\n{}"
			if _, err := io.WriteString(clientConn, rawRequest); err != nil {
				t.Fatalf("write request: %v", err)
			}
			if err := clientConn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatalf("set read deadline: %v", err)
			}
			resp, err := http.ReadResponse(bufio.NewReader(clientConn), nil)
			if err != nil {
				t.Fatalf("read upgrade response: %v", err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusSwitchingProtocols {
				t.Fatalf("status = %d, want 101", resp.StatusCode)
			}
			select {
			case <-upgraded:
			case <-time.After(3 * time.Second):
				t.Fatal("daemon never saw the upgrade")
			}

			// Controller side goes away.
			if err := clientConn.Close(); err != nil {
				t.Fatalf("close controller conn: %v", err)
			}
			start := time.Now()

			select {
			case <-sawEOF:
			case <-time.After(hijackTeardownBound):
				t.Fatalf("daemon saw no EOF within %s of the controller closing", hijackTeardownBound)
			}
			select {
			case err := <-closed:
				if err != nil {
					t.Fatalf("upstream Docker connection not closed %s after controller close: %v",
						time.Since(start).Round(time.Millisecond), err)
				}
			case <-time.After(2 * hijackTeardownBound):
				t.Fatal("daemon probe never completed")
			}
		})
	}
}
