package server

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/docker"
)

func TestHandleExecHijackReturnsWhenDockerOutputCloses(t *testing.T) {
	t.Parallel()

	sockPath, cleanupSocket := shortSocketPath(t)
	defer cleanupSocket()

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	daemonErr := make(chan error, 1)
	go func() {
		versionConn, acceptErr := listener.Accept()
		if acceptErr != nil {
			daemonErr <- fmt.Errorf("accept version request: %w", acceptErr)
			return
		}
		versionReq, readErr := http.ReadRequest(bufio.NewReader(versionConn))
		if readErr != nil {
			_ = versionConn.Close()
			daemonErr <- fmt.Errorf("read version request: %w", readErr)
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
			daemonErr <- fmt.Errorf("write version response: %w", writeErr)
			return
		}

		execConn, acceptErr := listener.Accept()
		if acceptErr != nil {
			daemonErr <- fmt.Errorf("accept exec request: %w", acceptErr)
			return
		}
		execReq, readErr := http.ReadRequest(bufio.NewReader(execConn))
		if readErr != nil {
			_ = execConn.Close()
			daemonErr <- fmt.Errorf("read exec request: %w", readErr)
			return
		}
		_ = execReq.Body.Close()
		_, writeErr = io.WriteString(execConn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: tcp\r\nConnection: Upgrade\r\n\r\n")
		_ = execConn.Close()
		if writeErr != nil {
			daemonErr <- fmt.Errorf("write exec response: %w", writeErr)
			return
		}
		daemonErr <- nil
	}()

	client, err := docker.NewClient(sockPath, 5)
	if err != nil {
		t.Fatalf("docker.NewClient: %v", err)
	}
	auditor, closeAudit, err := audit.New("", 0)
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}
	defer closeAudit()

	s := &Server{
		dockerClient: client,
		rateLimiter:  NewRateLimiter(),
		auditor:      auditor,
	}
	defer s.rateLimiter.Stop()

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	hrw := &hijackableResponseWriter{
		conn: serverConn,
		buf:  bufio.NewReadWriter(bufio.NewReader(serverConn), bufio.NewWriter(serverConn)),
		hdr:  make(http.Header),
	}
	handlerDone := make(chan struct{})
	go func() {
		defer close(handlerDone)
		req := httptest.NewRequest(http.MethodPost, "/exec/abc123/start", strings.NewReader(`{}`))
		s.handleExecHijack(hrw, req)
	}()

	if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set client deadline: %v", err)
	}
	clientReader := bufio.NewReader(clientConn)
	resp, err := http.ReadResponse(clientReader, nil)
	if err != nil {
		t.Fatalf("read upgrade response: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade status = %d, want %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}

	if err := <-daemonErr; err != nil {
		t.Fatal(err)
	}

	clientReadDone := make(chan error, 1)
	go func() {
		_, readErr := clientReader.ReadByte()
		clientReadDone <- readErr
	}()

	handlerReturned := false
	clientSawEOF := false
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for !handlerReturned || !clientSawEOF {
		select {
		case <-handlerDone:
			handlerReturned = true
			handlerDone = nil
		case readErr := <-clientReadDone:
			if !errors.Is(readErr, io.EOF) {
				t.Errorf("client read after Docker output closed: %v, want EOF", readErr)
			}
			clientSawEOF = true
			clientReadDone = nil
		case <-deadline.C:
			if !handlerReturned {
				t.Error("handleExecHijack remained blocked after Docker closed output while client input stayed open")
			}
			if !clientSawEOF {
				t.Error("client did not observe EOF after Docker closed output")
			}
			_ = clientConn.Close()
			select {
			case <-handlerDone:
			case <-time.After(time.Second):
				t.Fatal("handleExecHijack did not stop after test cleanup")
			}
			return
		}
	}
}

func TestHandleExecHijackPreservesBufferedInputOnClientHalfClose(t *testing.T) {
	t.Parallel()

	sockPath, cleanupSocket := shortSocketPath(t)
	defer cleanupSocket()

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	const clientInput = "buffered client input"
	const dockerOutput = "docker output after client EOF"
	daemonErr := make(chan error, 1)
	go func() {
		versionConn, acceptErr := listener.Accept()
		if acceptErr != nil {
			daemonErr <- fmt.Errorf("accept version request: %w", acceptErr)
			return
		}
		versionReq, readErr := http.ReadRequest(bufio.NewReader(versionConn))
		if readErr != nil {
			_ = versionConn.Close()
			daemonErr <- fmt.Errorf("read version request: %w", readErr)
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
			daemonErr <- fmt.Errorf("write version response: %w", writeErr)
			return
		}

		execConn, acceptErr := listener.Accept()
		if acceptErr != nil {
			daemonErr <- fmt.Errorf("accept exec request: %w", acceptErr)
			return
		}
		defer execConn.Close()
		execReq, readErr := http.ReadRequest(bufio.NewReader(execConn))
		if readErr != nil {
			daemonErr <- fmt.Errorf("read exec request: %w", readErr)
			return
		}
		_ = execReq.Body.Close()
		if _, writeErr = io.WriteString(execConn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: tcp\r\nConnection: Upgrade\r\n\r\n"); writeErr != nil {
			daemonErr <- fmt.Errorf("write exec response: %w", writeErr)
			return
		}
		if deadlineErr := execConn.SetReadDeadline(time.Now().Add(2 * time.Second)); deadlineErr != nil {
			daemonErr <- fmt.Errorf("set exec read deadline: %w", deadlineErr)
			return
		}
		gotInput, readErr := io.ReadAll(execConn)
		if readErr != nil {
			daemonErr <- fmt.Errorf("read relayed input: %w", readErr)
			return
		}
		if string(gotInput) != clientInput {
			daemonErr <- fmt.Errorf("relayed input = %q, want %q", gotInput, clientInput)
			return
		}
		if _, writeErr = io.WriteString(execConn, dockerOutput); writeErr != nil {
			daemonErr <- fmt.Errorf("write relayed output: %w", writeErr)
			return
		}
		daemonErr <- nil
	}()

	client, err := docker.NewClient(sockPath, 5)
	if err != nil {
		t.Fatalf("docker.NewClient: %v", err)
	}
	auditor, closeAudit, err := audit.New("", 0)
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}
	defer closeAudit()

	s := &Server{
		dockerClient: client,
		rateLimiter:  NewRateLimiter(),
		auditor:      auditor,
	}
	defer s.rateLimiter.Stop()

	clientListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for client: %v", err)
	}
	defer clientListener.Close()

	clientConnRaw, err := net.Dial("tcp", clientListener.Addr().String())
	if err != nil {
		t.Fatalf("dial client: %v", err)
	}
	clientConn := clientConnRaw.(*net.TCPConn)
	defer func() { _ = clientConn.Close() }()

	serverConnRaw, err := clientListener.Accept()
	if err != nil {
		t.Fatalf("accept client: %v", err)
	}
	serverConn := serverConnRaw.(*net.TCPConn)
	defer func() { _ = serverConn.Close() }()

	serverBuf := bufio.NewReadWriter(bufio.NewReader(serverConn), bufio.NewWriter(serverConn))
	if _, err := io.WriteString(clientConn, clientInput); err != nil {
		t.Fatalf("write client input: %v", err)
	}
	if err := clientConn.CloseWrite(); err != nil {
		t.Fatalf("half-close client input: %v", err)
	}
	if _, err := serverBuf.Peek(len(clientInput)); err != nil {
		t.Fatalf("buffer client input before hijack: %v", err)
	}

	hrw := &hijackableResponseWriter{
		conn: serverConn,
		buf:  serverBuf,
		hdr:  make(http.Header),
	}
	handlerDone := make(chan struct{})
	go func() {
		defer close(handlerDone)
		req := httptest.NewRequest(http.MethodPost, "/exec/abc123/start", strings.NewReader(`{}`))
		s.handleExecHijack(hrw, req)
	}()

	if err := clientConn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set client deadline: %v", err)
	}
	clientReader := bufio.NewReader(clientConn)
	resp, err := http.ReadResponse(clientReader, nil)
	if err != nil {
		t.Fatalf("read upgrade response: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade status = %d, want %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}
	gotOutput, err := io.ReadAll(clientReader)
	if err != nil {
		t.Fatalf("read Docker output after client half-close: %v", err)
	}
	if string(gotOutput) != dockerOutput {
		t.Errorf("relayed output = %q, want %q", gotOutput, dockerOutput)
	}
	if err := <-daemonErr; err != nil {
		t.Fatal(err)
	}
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("handleExecHijack did not return after half-closed input and completed Docker output")
	}
}
