// SPDX-License-Identifier: MIT

package edge

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/docker"
	"github.com/codeswhat/portwing/internal/protocol"
)

// upstreamCloseBound is how long the fake Docker daemon waits to see Portwing
// drop the hijacked exec connection after the controller side ends.
const upstreamCloseBound = 2 * time.Second

// fakeHijackDaemon is a Docker Unix socket that answers exec create and exec
// start (101 upgrade) and records when the hijacked connection closes.
type fakeHijackDaemon struct {
	sock   string
	ready  chan struct{} // closed once the exec start upgrade is written
	closed chan struct{} // closed when the hijacked connection reads EOF or errors
}

func newFakeHijackDaemon(t *testing.T) *fakeHijackDaemon {
	t.Helper()
	dir, err := os.MkdirTemp("", "lk")
	if err != nil {
		t.Fatalf("mkdirtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	d := &fakeHijackDaemon{
		sock:   filepath.Join(dir, "d.sock"),
		ready:  make(chan struct{}),
		closed: make(chan struct{}),
	}
	ln, err := net.Listen("unix", d.sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go d.serve(conn)
		}
	}()
	return d
}

func (d *fakeHijackDaemon) serve(conn net.Conn) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, req.Body)
	_ = req.Body.Close()
	switch {
	case strings.HasSuffix(req.URL.Path, "/exec") && req.Method == http.MethodPost:
		body := `{"Id":"fake-exec"}`
		_, _ = fmt.Fprintf(conn, "HTTP/1.1 201 Created\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
	case strings.HasSuffix(req.URL.Path, "/start"):
		_, _ = io.WriteString(conn, "HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
		close(d.ready)
		// Block until Portwing closes its end; EOF or any error is the close.
		_, _ = io.Copy(io.Discard, br)
		close(d.closed)
	default:
		_, _ = io.WriteString(conn, "HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
	}
}

func (d *fakeHijackDaemon) waitReady(t *testing.T) {
	t.Helper()
	select {
	case <-d.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("fake Docker never saw the exec start upgrade")
	}
}

func (d *fakeHijackDaemon) assertUpstreamCloses(t *testing.T, what string) {
	t.Helper()
	select {
	case <-d.closed:
	case <-time.After(upstreamCloseBound):
		t.Fatalf("upstream Docker exec connection still open %s after %s", upstreamCloseBound, what)
	}
}

// TestControllerCloseTearsDownDockerExecSession covers both controller-side
// ends of an Edge exec session: an explicit exec_end and a dropped tunnel.
// Either must close the hijacked connection to the Docker daemon.
func TestControllerCloseTearsDownDockerExecSession(t *testing.T) {
	for _, tc := range []struct {
		name string
		end  func(c *Client, execID string)
	}{
		{"exec_end", func(c *Client, id string) { c.EndExec(protocol.ExecEndMessage{ExecID: id}) }},
		{"tunnel drop", func(c *Client, _ string) { c.closeAllExecSessions() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			daemon := newFakeHijackDaemon(t)
			dc, err := docker.NewClient(daemon.sock, 5)
			if err != nil {
				t.Fatalf("docker.NewClient: %v", err)
			}
			c, ctrl := newTestClient(t)
			c.dockerClient = dc

			c.StartExec(context.Background(), protocol.ExecStartMessage{
				ExecID:      "teardown-1",
				ContainerID: "c1",
				Cmd:         []string{"sh"},
			})
			daemon.waitReady(t)
			var ready protocol.ExecReadyMessage
			decodeData(t, expectType(t, ctrl, protocol.TypeExecReady), &ready)

			select {
			case <-daemon.closed:
				t.Fatal("upstream closed before the controller ended the session")
			case <-time.After(50 * time.Millisecond):
			}

			tc.end(c, "teardown-1")
			daemon.assertUpstreamCloses(t, tc.name)
		})
	}
}
