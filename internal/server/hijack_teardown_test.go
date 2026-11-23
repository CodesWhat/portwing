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

// setHijackHalfCloseIdle shortens the idle drain window for the test and its
// subtests. Call it from a non-parallel test body: the restore runs after the
// parallel subtests finish, and no other parallel test runs meanwhile.
func setHijackHalfCloseIdle(t *testing.T, d time.Duration) {
	t.Helper()
	old := hijackHalfCloseIdle
	hijackHalfCloseIdle = d
	t.Cleanup(func() { hijackHalfCloseIdle = old })
}

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
// The idle cases rely on the bounded idle drain: a controller FIN reads as a
// stdin half-close, so Portwing CloseWrites Docker and keeps relaying, then
// closes both ends once Docker has sent nothing for hijackHalfCloseIdle.
func TestControllerCloseTearsDownStandardHijackSession(t *testing.T) {
	setHijackHalfCloseIdle(t, 150*time.Millisecond)
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

// TestHijackHalfCloseKeepsRelayingWhileDockerOutputFlows half-closes the
// controller's sending side and checks Docker output arriving inside the idle
// window keeps the session open past more than one window, then that silence
// finally closes it.
func TestHijackHalfCloseKeepsRelayingWhileDockerOutputFlows(t *testing.T) {
	const window = 400 * time.Millisecond
	setHijackHalfCloseIdle(t, window)

	const (
		path    = "/v1.44/containers/abc123/attach"
		chunks  = 8
		spacing = 100 * time.Millisecond
	)
	sockPath, cleanupSocket := shortSocketPath(t)
	defer cleanupSocket()
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen on Docker socket: %v", err)
	}
	defer listener.Close()

	daemonQuit := make(chan struct{})
	defer close(daemonQuit)
	go func() {
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
				if req.URL.Path != path {
					body := `{"Version":"26.0.0","ApiVersion":"1.44"}`
					_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
					return
				}
				_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: tcp\r\nConnection: Upgrade\r\n\r\n")
				// Output keeps arriving after the controller half-closes, for
				// longer than two windows, then Docker goes quiet.
				for range chunks {
					time.Sleep(spacing)
					if _, writeErr := conn.Write([]byte{'x'}); writeErr != nil {
						return
					}
				}
				// Then stay silent and open: only Portwing's idle drain ends it.
				<-daemonQuit
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
	conn, err := net.Dial("tcp", proxyURL.Host)
	if err != nil {
		t.Fatalf("dial Portwing: %v", err)
	}
	defer conn.Close()
	rawRequest := "POST " + path + "?stream=1&stdin=1&stdout=1 HTTP/1.1\r\n" +
		"Host: " + proxyURL.Host + "\r\n" +
		"Authorization: Bearer teardown-secret\r\n" +
		"Connection: Upgrade\r\nUpgrade: tcp\r\n" +
		"Content-Type: application/json\r\nContent-Length: 2\r\n\r\n{}"
	if _, err := io.WriteString(conn, rawRequest); err != nil {
		t.Fatalf("write request: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read upgrade response: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}

	// Controller half-closes stdin but keeps reading output.
	if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatalf("controller half-close: %v", err)
	}
	start := time.Now()
	got, err := io.ReadAll(br)
	if err != nil {
		t.Fatalf("read relayed output (session never closed after Docker went quiet?): %v", err)
	}
	if len(got) != chunks {
		t.Fatalf("relayed %d bytes, want %d: session closed while Docker output was still flowing", len(got), chunks)
	}
	if elapsed := time.Since(start); elapsed < 2*window {
		t.Fatalf("session lasted %s, want past two idle windows (%s)", elapsed, 2*window)
	}
}

// noCloseWriteConn hides the embedded conn's CloseWrite, so the proxy sees a
// Docker connection that cannot half-close.
type noCloseWriteConn struct{ net.Conn }

// failingCloseWriteConn half-closes with an error.
type failingCloseWriteConn struct{ net.Conn }

func (failingCloseWriteConn) CloseWrite() error { return fmt.Errorf("close write failed") }

// TestHijackClosesImmediatelyWhenDockerCannotHalfClose covers a Docker
// connection that cannot be half-closed: the controller's FIN then closes both
// ends at once instead of starting an idle drain.
func TestHijackClosesImmediatelyWhenDockerCannotHalfClose(t *testing.T) {
	for name, wrap := range map[string]func(net.Conn) net.Conn{
		"no CloseWrite":      func(c net.Conn) net.Conn { return noCloseWriteConn{c} },
		"CloseWrite failing": func(c net.Conn) net.Conn { return failingCloseWriteConn{c} },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			const path = "/v1.44/containers/abc123/attach"
			sockPath, cleanupSocket := shortSocketPath(t)
			defer cleanupSocket()
			listener, err := net.Listen("unix", sockPath)
			if err != nil {
				t.Fatalf("listen on Docker socket: %v", err)
			}
			defer listener.Close()
			quit := make(chan struct{})
			defer close(quit)
			go func() {
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
						if req.URL.Path != path {
							body := `{"Version":"26.0.0","ApiVersion":"1.44"}`
							_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
							return
						}
						_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: tcp\r\nConnection: Upgrade\r\n\r\n")
						<-quit
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
			s.dockerDialer = func(network, addr string) (net.Conn, error) {
				c, dialErr := net.Dial(network, addr)
				if dialErr != nil {
					return nil, dialErr
				}
				return wrap(c), nil
			}

			proxy := httptest.NewServer(s.httpServer.Handler)
			defer proxy.Close()
			proxyURL, err := url.Parse(proxy.URL)
			if err != nil {
				t.Fatalf("parse proxy URL: %v", err)
			}
			conn, err := net.Dial("tcp", proxyURL.Host)
			if err != nil {
				t.Fatalf("dial Portwing: %v", err)
			}
			defer conn.Close()
			rawRequest := "POST " + path + "?stream=1&stdin=1 HTTP/1.1\r\n" +
				"Host: " + proxyURL.Host + "\r\n" +
				"Authorization: Bearer teardown-secret\r\n" +
				"Connection: Upgrade\r\nUpgrade: tcp\r\n" +
				"Content-Type: application/json\r\nContent-Length: 2\r\n\r\n{}"
			if _, err := io.WriteString(conn, rawRequest); err != nil {
				t.Fatalf("write request: %v", err)
			}
			if err := conn.SetReadDeadline(time.Now().Add(hijackTeardownBound)); err != nil {
				t.Fatalf("set read deadline: %v", err)
			}
			br := bufio.NewReader(conn)
			resp, err := http.ReadResponse(br, nil)
			if err != nil {
				t.Fatalf("read upgrade response: %v", err)
			}
			_ = resp.Body.Close()
			if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
				t.Fatalf("controller half-close: %v", err)
			}
			if _, err := io.ReadAll(br); err != nil {
				t.Fatalf("session stayed open after controller FIN: %v", err)
			}
		})
	}
}
