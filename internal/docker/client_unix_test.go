package docker

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// shortTempDir returns a short temp directory path suitable for unix sockets
// (macOS limits unix socket paths to 104 bytes).
func shortTempDir(t *testing.T) string {
	t.Helper()
	// Use os.MkdirTemp with /tmp as base to keep the path short.
	dir, err := os.MkdirTemp("", "lk")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// startUnixHTTPServer creates a unix socket in a short temp dir and serves
// handler on it. Returns the socket path; the server is shut down via t.Cleanup.
func startUnixHTTPServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	dir := shortTempDir(t)
	path := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	srv := &http.Server{Handler: handler}
	go srv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() { srv.Close() })
	return path
}

// ---- NewClient via real unix socket ----

func TestNewClient_Success(t *testing.T) {
	t.Parallel()

	socketPath := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"Version":"24.0.5","ApiVersion":"1.45"}`)
	}))

	c, err := NewClient(socketPath, 5)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c.apiVersion != "v1.45" {
		t.Fatalf("apiVersion = %q, want %q", c.apiVersion, "v1.45")
	}
	if c.socketPath != socketPath {
		t.Fatalf("socketPath = %q, want %q", c.socketPath, socketPath)
	}
}

// TestNewClient_StreamTransportDialContext exercises the streamTransport.DialContext
// closure by issuing a streaming request via the real unix socket client.
func TestNewClient_StreamTransportDialContext(t *testing.T) {
	t.Parallel()

	socketPath := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Serve both the version negotiation and a streaming events endpoint.
		if r.URL.Path == "/version" {
			fmt.Fprintf(w, `{"Version":"24.0.5","ApiVersion":"1.45"}`)
			return
		}
		// Events endpoint — just return 200 with empty body.
		w.WriteHeader(http.StatusOK)
	}))

	c, err := NewClient(socketPath, 5)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	// DoStream uses streamClient which exercises the streamTransport.DialContext closure.
	resp, err := c.DoStream(t.Context(), http.MethodGet, "/events?type=container", nil)
	if err != nil {
		t.Fatalf("DoStream: %v", err)
	}
	resp.Body.Close()
}

func TestNewClient_NegotiationFallback(t *testing.T) {
	t.Parallel()

	// Serve bad JSON so negotiation fails → fallback to v1.44
	socketPath := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "not-json")
	}))

	c, err := NewClient(socketPath, 5)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c.apiVersion != "v1.44" {
		t.Fatalf("apiVersion = %q, want fallback %q", c.apiVersion, "v1.44")
	}
}

// ---- StartExec via real unix socket ----

func TestStartExec_DialFailure(t *testing.T) {
	t.Parallel()

	c := &Client{
		socketPath: "/nonexistent/path/docker.sock",
		apiVersion: "v1.44",
	}
	_, err := c.StartExec(t.Context(), "exec-123", false)
	if err == nil {
		t.Fatal("expected error dialing nonexistent socket, got nil")
	}
	if !strings.Contains(err.Error(), "dial docker socket") {
		t.Fatalf("error = %q, expected 'dial docker socket'", err.Error())
	}
}

func TestStartExec_ReadResponseError(t *testing.T) {
	t.Parallel()

	dir := shortTempDir(t)
	sockPath := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Drain the request
		buf := make([]byte, 4096)
		conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond)) //nolint:errcheck
		conn.Read(buf)                                               //nolint:errcheck
		// Send garbage (not valid HTTP)
		conn.Write([]byte("GARBAGE\r\n\r\n")) //nolint:errcheck
	}()

	c := &Client{socketPath: sockPath, apiVersion: "v1.44"}
	_, err = c.StartExec(t.Context(), "exec-123", false)
	wg.Wait()
	if err == nil {
		t.Fatal("expected error reading garbage response, got nil")
	}
}

func TestStartExec_Non101Response(t *testing.T) {
	t.Parallel()

	dir := shortTempDir(t)
	sockPath := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))       //nolint:errcheck
		conn.Read(buf)                                                     //nolint:errcheck
		conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")) //nolint:errcheck
	}()

	c := &Client{socketPath: sockPath, apiVersion: "v1.44"}
	_, err = c.StartExec(t.Context(), "exec-123", false)
	wg.Wait()
	if err == nil {
		t.Fatal("expected error for non-101 response, got nil")
	}
	if !strings.Contains(err.Error(), "101") {
		t.Fatalf("error = %q, expected to contain '101'", err.Error())
	}
}

func TestStartExec_Non101ResponseWithMessage(t *testing.T) {
	t.Parallel()

	dir := shortTempDir(t)
	sockPath := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	body := `{"message":"exec denied: no commands are allowlisted"}`
	resp := fmt.Sprintf("HTTP/1.1 403 Forbidden\r\nContent-Length: %d\r\n\r\n%s", len(body), body)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond)) //nolint:errcheck
		conn.Read(buf)                                               //nolint:errcheck
		conn.Write([]byte(resp))                                     //nolint:errcheck
	}()

	c := &Client{socketPath: sockPath, apiVersion: "v1.44"}
	_, err = c.StartExec(t.Context(), "exec-123", false)
	wg.Wait()
	if err == nil {
		t.Fatal("expected error for non-101 response, got nil")
	}
	if !strings.Contains(err.Error(), "101") {
		t.Fatalf("error = %q, expected to contain '101'", err.Error())
	}
	if !strings.Contains(err.Error(), "exec denied: no commands are allowlisted") {
		t.Fatalf("error = %q, expected to contain the docker error message", err.Error())
	}
}

func TestStartExec_Success_NoBuffered(t *testing.T) {
	t.Parallel()

	dir := shortTempDir(t)
	sockPath := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		// Read the raw HTTP request
		buf := make([]byte, 4096)
		conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond)) //nolint:errcheck
		conn.Read(buf)                                               //nolint:errcheck
		// Send 101 Switching Protocols with NO extra body bytes
		conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: tcp\r\nConnection: Upgrade\r\n\r\n")) //nolint:errcheck
		// Keep the connection open briefly
		time.Sleep(100 * time.Millisecond)
		conn.Close()
	}()

	c := &Client{socketPath: sockPath, apiVersion: "v1.44"}
	conn, err := c.StartExec(t.Context(), "exec-123", false)
	if err != nil {
		t.Fatalf("StartExec: %v", err)
	}
	conn.Close()
	wg.Wait()

	// Should be a plain net.Conn, not bufferedConn (no extra bytes after headers)
	if _, ok := conn.(*bufferedConn); ok {
		t.Fatal("expected plain net.Conn (no buffered bytes), got bufferedConn")
	}
}

func TestStartExec_Success_WithBuffered(t *testing.T) {
	t.Parallel()

	dir := shortTempDir(t)
	sockPath := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		buf := make([]byte, 4096)
		conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond)) //nolint:errcheck
		conn.Read(buf)                                               //nolint:errcheck
		// Send 101 + immediate extra bytes (simulates stream data arriving immediately after upgrade)
		conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: tcp\r\nConnection: Upgrade\r\n\r\nextra-stream-data")) //nolint:errcheck
		time.Sleep(100 * time.Millisecond)
		conn.Close()
	}()

	c := &Client{socketPath: sockPath, apiVersion: "v1.44"}
	conn, err := c.StartExec(t.Context(), "exec-123", true)
	if err != nil {
		t.Fatalf("StartExec: %v", err)
	}
	defer conn.Close()
	wg.Wait()

	// Should be a bufferedConn because extra bytes were buffered after headers
	if _, ok := conn.(*bufferedConn); !ok {
		t.Fatal("expected bufferedConn (extra buffered bytes), got plain net.Conn")
	}
}

// TestStartExec_WedgedDaemon reproduces a dockerd that accepts the exec-start
// connection but never writes a response (a wedged daemon or a deadlocked
// exec subsystem). Before the fix, StartExec dialed with plain net.Dial and
// blocked on http.ReadResponse with no deadline, so this would hang forever
// and leak the ExecSession registration. StartExec must now honor ctx's
// deadline and return promptly. The outer timeout is generous so a
// regression shows up as a failure, not a hang.
func TestStartExec_WedgedDaemon(t *testing.T) {
	t.Parallel()

	dir := shortTempDir(t)
	sockPath := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	accepted := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		close(accepted)
		// Accept the connection and read whatever the client sends, but never
		// write a response — this is the wedged-daemon scenario StartExec
		// must bound itself against. The read unblocks (with EOF) once the
		// client gives up and closes its side, ending the goroutine.
		buf := make([]byte, 4096)
		conn.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
		conn.Read(buf)                                        //nolint:errcheck
	}()

	c := &Client{socketPath: sockPath, apiVersion: "v1.44", dialTimeout: 200 * time.Millisecond}

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := c.StartExec(ctx, "exec-123", false)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error from a wedged handshake, got nil")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("StartExec did not return within the outer timeout; handshake is not bounded")
	}

	<-accepted
	ln.Close()
	wg.Wait()
}

// ---- closeConn ----

// errCloseConn is a net.Conn whose Close always returns an error, so we can
// exercise the slog.Debug branch in closeConn.
type errCloseConn struct {
	net.Conn
	closeErr error
}

func (e *errCloseConn) Close() error { return e.closeErr }

func TestCloseConn_Error(t *testing.T) {
	t.Parallel()

	// closeConn with a conn that errors on Close should log but not panic.
	c := &errCloseConn{Conn: nil, closeErr: fmt.Errorf("close failed")}
	closeConn(c, "test context") // must not panic
}

func TestCloseConn_NoError(t *testing.T) {
	t.Parallel()

	// closeConn on a conn that closes cleanly should not log or panic.
	c1, c2 := net.Pipe()
	c2.Close()

	// closeConn calls c1.Close() which should succeed.
	closeConn(c1, "test context")
}
