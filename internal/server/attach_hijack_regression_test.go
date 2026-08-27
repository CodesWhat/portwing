package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/docker"
)

func TestAuthenticatedAttachUpgradeRelaysBufferedInputBidirectionally(t *testing.T) {
	t.Parallel()

	sockPath, cleanupSocket := shortSocketPath(t)
	defer cleanupSocket()

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen on Docker socket: %v", err)
	}
	defer listener.Close()

	const (
		requestBody  = `{"DetachKeys":"ctrl-x"}`
		clientInput  = "buffered attach input"
		dockerOutput = "interactive attach output"
	)
	daemonResult := make(chan error, 1)
	go func() {
		versionConn, acceptErr := listener.Accept()
		if acceptErr != nil {
			daemonResult <- fmt.Errorf("accept version request: %w", acceptErr)
			return
		}
		versionReq, readErr := http.ReadRequest(bufio.NewReader(versionConn))
		if readErr != nil {
			_ = versionConn.Close()
			daemonResult <- fmt.Errorf("read version request: %w", readErr)
			return
		}
		_ = versionReq.Body.Close()
		versionBody := `{"Version":"26.0.0","ApiVersion":"1.44"}`
		_, writeErr := fmt.Fprintf(
			versionConn,
			"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
			len(versionBody),
			versionBody,
		)
		_ = versionConn.Close()
		if writeErr != nil {
			daemonResult <- fmt.Errorf("write version response: %w", writeErr)
			return
		}

		attachConn, acceptErr := listener.Accept()
		if acceptErr != nil {
			daemonResult <- fmt.Errorf("accept attach request: %w", acceptErr)
			return
		}
		defer attachConn.Close()

		attachReader := bufio.NewReader(attachConn)
		attachReq, readErr := http.ReadRequest(attachReader)
		if readErr != nil {
			daemonResult <- fmt.Errorf("read attach request: %w", readErr)
			return
		}
		gotBody, readErr := io.ReadAll(attachReq.Body)
		_ = attachReq.Body.Close()
		if readErr != nil {
			daemonResult <- fmt.Errorf("read attach body: %w", readErr)
			return
		}
		if attachReq.Method != http.MethodPost {
			daemonResult <- fmt.Errorf("attach method = %q", attachReq.Method)
			return
		}
		if attachReq.URL.Path != "/v1.44/containers/abc123/attach" {
			daemonResult <- fmt.Errorf("attach path = %q", attachReq.URL.Path)
			return
		}
		if attachReq.URL.RawQuery != "stream=1&stdin=1&stdout=1" {
			daemonResult <- fmt.Errorf("attach query = %q", attachReq.URL.RawQuery)
			return
		}
		if string(gotBody) != requestBody {
			daemonResult <- fmt.Errorf("attach body = %q, want %q", gotBody, requestBody)
			return
		}
		if got := attachReq.Header.Get("Content-Type"); got != "application/json" {
			daemonResult <- fmt.Errorf("attach content type = %q", got)
			return
		}
		if got := attachReq.Header.Get("X-Docker-End-To-End"); got != "preserved" {
			daemonResult <- fmt.Errorf("end-to-end header = %q", got)
			return
		}
		for _, name := range append(portwingAuthHeaders, "X-Connection-Only") {
			if value := attachReq.Header.Get(name); value != "" {
				daemonResult <- fmt.Errorf("hop-by-hop or Portwing header %s leaked with value %q", name, value)
				return
			}
		}
		if got := attachReq.Header.Get("Connection"); !strings.EqualFold(got, "Upgrade") {
			daemonResult <- fmt.Errorf("attach connection header = %q", got)
			return
		}
		if got := attachReq.Header.Get("Upgrade"); !strings.EqualFold(got, "tcp") {
			daemonResult <- fmt.Errorf("attach upgrade header = %q", got)
			return
		}
		if _, writeErr = io.WriteString(attachConn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: tcp\r\nConnection: Upgrade\r\n\r\n"); writeErr != nil {
			daemonResult <- fmt.Errorf("write attach upgrade: %w", writeErr)
			return
		}

		if deadlineErr := attachConn.SetReadDeadline(time.Now().Add(time.Second)); deadlineErr != nil {
			daemonResult <- fmt.Errorf("set attach read deadline: %w", deadlineErr)
			return
		}
		gotInput := make([]byte, len(clientInput))
		_, readErr = io.ReadFull(attachReader, gotInput)
		if _, writeErr = io.WriteString(attachConn, dockerOutput); writeErr != nil {
			daemonResult <- fmt.Errorf("write Docker output: %w", writeErr)
			return
		}
		if readErr != nil {
			daemonResult <- fmt.Errorf("read buffered client input: %w", readErr)
			return
		}
		if string(gotInput) != clientInput {
			daemonResult <- fmt.Errorf("relayed client input = %q, want %q", gotInput, clientInput)
			return
		}
		daemonResult <- nil
	}()

	dockerClient, err := docker.NewClient(sockPath, 5)
	if err != nil {
		t.Fatalf("docker.NewClient: %v", err)
	}
	cfg := minimalConfig()
	cfg.AllowUnauthenticated = false
	cfg.Token = "attach-secret" //nolint:gosec // Test-only request credential.

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

	rawRequest := "POST /v1.44/containers/abc123/attach?stream=1&stdin=1&stdout=1 HTTP/1.1\r\n" +
		"Host: " + proxyURL.Host + "\r\n" +
		"Authorization: Bearer attach-secret\r\n" +
		"X-Portwing-Token: must-not-leak\r\n" +
		"X-Dd-Agent-Secret: must-not-leak\r\n" +
		"X-Portwing-Signature: must-not-leak\r\n" +
		"Connection: Upgrade, X-Connection-Only\r\n" +
		"X-Connection-Only: must-not-leak\r\n" +
		"Upgrade: tcp\r\n" +
		"Content-Type: application/json\r\n" +
		"X-Docker-End-To-End: preserved\r\n" +
		fmt.Sprintf("Content-Length: %d\r\n\r\n", len(requestBody)) +
		requestBody +
		clientInput
	if _, err := io.WriteString(clientConn, rawRequest); err != nil {
		t.Fatalf("write attach request and buffered input: %v", err)
	}
	if err := clientConn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set client read deadline: %v", err)
	}
	clientReader := bufio.NewReader(clientConn)
	resp, err := http.ReadResponse(clientReader, nil)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("read attach upgrade response: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("attach status = %d, want %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}
	if err := <-daemonResult; err != nil {
		t.Errorf("Docker did not receive the buffered attach input: %v", err)
	}
	gotOutput := make([]byte, len(dockerOutput))
	if _, err := io.ReadFull(clientReader, gotOutput); err != nil {
		t.Errorf("read relayed Docker output: %v", err)
	}
	if string(gotOutput) != dockerOutput {
		t.Errorf("relayed Docker output = %q, want %q", gotOutput, dockerOutput)
	}

	deadline := time.Now().Add(time.Second)
	for {
		records := s.auditor.Records(0)
		if len(records) > 0 {
			seenRequest := false
			for _, record := range records {
				if record.Event == audit.EventExecStart {
					t.Fatalf("attach emitted exec-only audit record: %+v", record)
				}
				if record.Event == audit.EventAPIRequest && strings.HasSuffix(record.Path, "/attach") {
					seenRequest = true
				}
			}
			if !seenRequest {
				t.Fatalf("attach API audit record missing: %+v", records)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("attach handler did not finish and emit its API audit record")
		}
		time.Sleep(time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = proxy.Config.Shutdown(ctx)
}

func TestDockerHijackPathMatchesOnlyDockerExecAndAttachRoutes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want bool
	}{
		{path: "/exec/abc123/start", want: true},
		{path: "/v1.44/exec/abc123/start", want: true},
		{path: "/containers/abc123/attach", want: true},
		{path: "/v1.44/containers/abc123/attach", want: true},
		{path: "/proxy/exec/abc123/start", want: false},
		{path: "/v1.44/proxy/exec/abc123/start", want: false},
		{path: "/proxy/containers/abc123/attach", want: false},
		{path: "/v1/containers/abc123/attach", want: false},
		{path: "/v.44/containers/abc123/attach", want: false},
		{path: "/v1.x/containers/abc123/attach", want: false},
		{path: "/v1.44/containers//attach", want: false},
		{path: "/v1.44/containers/abc123/attach/", want: false},
		{path: "/v1.44/containers/abc123/attach/extra", want: false},
		{path: "/v1.44/exec/abc123/start/extra", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()
			if got := isDockerHijackPath(tt.path); got != tt.want {
				t.Fatalf("isDockerHijackPath(%q) = %t, want %t", tt.path, got, tt.want)
			}
		})
	}
}

func TestAttachBodyReadFailureIsRejectedBeforeHijack(t *testing.T) {
	t.Parallel()

	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/v1.44/containers/abc123/attach", nil)
	req.Body = io.NopCloser(failingReader{err: errors.New("read failed")})
	rec := httptest.NewRecorder()

	s.handleDockerHijack(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("body read failure status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "reading upgrade request body: read failed") {
		t.Fatalf("body read failure response = %q", rec.Body.String())
	}
}

func TestDockerHijackRejectsConnectionAfterShutdownStarts(t *testing.T) {
	t.Parallel()

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()

	s := &Server{shuttingDown: true}
	w := &hijackableResponseWriter{
		conn: serverConn,
		buf:  bufio.NewReadWriter(bufio.NewReader(serverConn), bufio.NewWriter(serverConn)),
		hdr:  make(http.Header),
	}
	req := httptest.NewRequest(http.MethodPost, "/v1.44/containers/abc123/attach", nil)
	if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set client read deadline: %v", err)
	}

	s.handleDockerHijack(w, req)

	if _, err := clientConn.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("client read after rejected hijack = %v, want EOF", err)
	}
}

func TestOversizedAttachBodyIsRejectedBeforeHijack(t *testing.T) {
	t.Parallel()

	s := &Server{}
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1.44/containers/abc123/attach",
		io.LimitReader(zeroReader{}, maxHijackBodyBytes+1),
	)
	rec := httptest.NewRecorder()
	s.handleDockerHijack(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized attach status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestAttachUpgradeReturnsBadGatewayBeforeProtocolSwitchWhenDockerIsUnavailable(t *testing.T) {
	t.Parallel()

	sockPath, cleanupSocket := shortSocketPath(t)
	defer cleanupSocket()
	dockerClient, err := docker.NewClient(sockPath, 5)
	if err != nil {
		t.Fatalf("docker.NewClient: %v", err)
	}
	auditor, _, err := audit.New("", 0)
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}
	rateLimiter := NewRateLimiter()
	defer rateLimiter.Stop()
	s := &Server{dockerClient: dockerClient, auditor: auditor, rateLimiter: rateLimiter}

	proxy := httptest.NewServer(http.HandlerFunc(s.handleDockerProxy))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	conn, err := net.Dial("tcp", proxyURL.Host)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	request := "POST /v1.44/containers/abc123/attach?stdin=1 HTTP/1.1\r\n" +
		"Host: " + proxyURL.Host + "\r\n" +
		"Connection: Upgrade\r\n" +
		"Upgrade: tcp\r\n" +
		"Content-Length: 0\r\n\r\n"
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatalf("write attach request: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read pre-upgrade error response: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("unavailable Docker attach status = %d, want %d", resp.StatusCode, http.StatusBadGateway)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

type failingReader struct {
	err error
}

func (r failingReader) Read([]byte) (int, error) {
	return 0, r.err
}
