package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/docker"
)

func TestShutdownDrainsFinalMutationAuditRecordsBeforeClosingSink(t *testing.T) {
	t.Parallel()

	auditPath := filepath.Join(t.TempDir(), "audit.log")
	auditor, closeAudit, err := audit.New(auditPath, 0)
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}
	defer closeAudit()

	rateLimiter := NewRateLimiter()
	started := make(chan struct{})
	release := make(chan struct{})
	handler := rateLimiter.AuthMiddlewareWithEd25519(
		newRawTokenVerifier("shutdown-secret"),
		Ed25519Config{},
		auditor,
		nil,
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(started)
			<-release
			auditor.ComposeOp("127.0.0.1", "up", "draining-stack", audit.OutcomeAllowed)
			w.WriteHeader(http.StatusNoContent)
		}),
	)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	httpServer := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	serveDone := make(chan error, 1)
	go func() { serveDone <- httpServer.Serve(listener) }()

	s := &Server{
		rateLimiter: rateLimiter,
		auditor:     auditor,
		httpServer:  httpServer,
		hupDone:     make(chan struct{}),
	}

	req, err := http.NewRequest(http.MethodPost, "http://"+listener.Addr().String()+"/mutate", nil)
	if err != nil {
		t.Fatalf("new mutation request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer shutdown-secret")
	type requestResult struct {
		status int
		err    error
	}
	requestDone := make(chan requestResult, 1)
	go func() {
		resp, requestErr := (&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
		result := requestResult{err: requestErr}
		if requestErr == nil {
			result.status = resp.StatusCode
			_ = resp.Body.Close()
		}
		requestDone <- result
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("mutation handler did not start")
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelShutdown()
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- s.Shutdown(shutdownCtx) }()

	select {
	case serveErr := <-serveDone:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			t.Fatalf("Serve: %v", serveErr)
		}
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not close the listener while the handler was blocked")
	}
	close(release)

	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown did not finish after the handler drained")
	}
	select {
	case result := <-requestDone:
		if result.err != nil {
			t.Fatalf("mutation request: %v", result.err)
		}
		if result.status != http.StatusNoContent {
			t.Fatalf("mutation status = %d, want %d", result.status, http.StatusNoContent)
		}
	case <-time.After(time.Second):
		t.Fatal("mutation request did not complete")
	}

	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	events := make(map[string]bool)
	for _, line := range splitNonEmptyLines(data) {
		var record struct {
			Event string `json:"event"`
			Stack string `json:"stack"`
			Path  string `json:"path"`
		}
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("decode audit record %q: %v", line, err)
		}
		if record.Event == audit.EventComposeOp && record.Stack == "draining-stack" {
			events[audit.EventComposeOp] = true
		}
		if record.Event == audit.EventAPIRequest && record.Path == "/mutate" {
			events[audit.EventAPIRequest] = true
		}
	}
	if !events[audit.EventComposeOp] || !events[audit.EventAPIRequest] {
		t.Fatalf("final mutation audit records = %v, want both %q and %q; file=%s", events, audit.EventComposeOp, audit.EventAPIRequest, data)
	}
}

func TestShutdownTimeoutKeepsAuditSinkOpenUntilHandlersDrain(t *testing.T) {
	t.Parallel()

	auditPath := filepath.Join(t.TempDir(), "audit.log")
	auditor, _, err := audit.New(auditPath, 0)
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}
	defer auditor.Close()

	rateLimiter := NewRateLimiter()
	started := make(chan struct{})
	release := make(chan struct{})
	handler := rateLimiter.AuthMiddlewareWithEd25519(
		newRawTokenVerifier("shutdown-secret"),
		Ed25519Config{},
		auditor,
		nil,
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(started)
			<-release
			auditor.ComposeOp("127.0.0.1", "up", "timeout-stack", audit.OutcomeAllowed)
			w.WriteHeader(http.StatusNoContent)
		}),
	)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	httpServer := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	serveDone := make(chan error, 1)
	go func() { serveDone <- httpServer.Serve(listener) }()
	s := &Server{
		rateLimiter: rateLimiter,
		auditor:     auditor,
		httpServer:  httpServer,
		hupDone:     make(chan struct{}),
	}

	req, err := http.NewRequest(http.MethodPost, "http://"+listener.Addr().String()+"/timeout", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer shutdown-secret")
	requestDone := make(chan error, 1)
	go func() {
		resp, requestErr := (&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
		if requestErr == nil {
			_ = resp.Body.Close()
		}
		requestDone <- requestErr
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not start")
	}

	timeoutCtx, cancelTimeout := context.WithTimeout(context.Background(), 10*time.Millisecond)
	err = s.Shutdown(timeoutCtx)
	cancelTimeout()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first Shutdown error = %v, want context deadline exceeded", err)
	}
	close(release)

	select {
	case err := <-requestDone:
		if err != nil {
			t.Fatalf("request after timed-out shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("request did not drain after release")
	}

	finalCtx, cancelFinal := context.WithTimeout(context.Background(), time.Second)
	defer cancelFinal()
	if err := s.Shutdown(finalCtx); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}

	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	if !containsAuditRecord(t, data, audit.EventComposeOp, "timeout-stack", "") {
		t.Fatalf("timed-out shutdown lost final compose record: %s", data)
	}
	if !containsAuditRecord(t, data, audit.EventAPIRequest, "", "/timeout") {
		t.Fatalf("timed-out shutdown lost final API record: %s", data)
	}

	select {
	case serveErr := <-serveDone:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			t.Fatalf("Serve: %v", serveErr)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not return after shutdown")
	}
}

func TestShutdownClosesHijackedAttachAfterFinalAuditRecord(t *testing.T) {
	t.Parallel()

	sockPath, cleanupSocket := shortSocketPath(t)
	defer cleanupSocket()
	dockerListener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen on Docker socket: %v", err)
	}
	defer dockerListener.Close()

	daemonDone := make(chan error, 1)
	go func() {
		versionConn, acceptErr := dockerListener.Accept()
		if acceptErr != nil {
			daemonDone <- fmt.Errorf("accept version request: %w", acceptErr)
			return
		}
		versionReq, readErr := http.ReadRequest(bufio.NewReader(versionConn))
		if readErr != nil {
			_ = versionConn.Close()
			daemonDone <- fmt.Errorf("read version request: %w", readErr)
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
			daemonDone <- fmt.Errorf("write version response: %w", writeErr)
			return
		}

		attachConn, acceptErr := dockerListener.Accept()
		if acceptErr != nil {
			daemonDone <- fmt.Errorf("accept attach request: %w", acceptErr)
			return
		}
		defer attachConn.Close()
		attachReq, readErr := http.ReadRequest(bufio.NewReader(attachConn))
		if readErr != nil {
			daemonDone <- fmt.Errorf("read attach request: %w", readErr)
			return
		}
		_ = attachReq.Body.Close()
		if _, writeErr := io.WriteString(attachConn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: tcp\r\nConnection: Upgrade\r\n\r\n"); writeErr != nil {
			daemonDone <- fmt.Errorf("write attach upgrade: %w", writeErr)
			return
		}
		if _, readErr := io.Copy(io.Discard, attachConn); readErr != nil {
			daemonDone <- fmt.Errorf("wait for attach shutdown: %w", readErr)
			return
		}
		daemonDone <- nil
	}()

	dockerClient, err := docker.NewClient(sockPath, 5)
	if err != nil {
		t.Fatalf("docker.NewClient: %v", err)
	}
	auditPath := filepath.Join(t.TempDir(), "audit.log")
	cfg := minimalConfig()
	cfg.AllowUnauthenticated = false
	cfg.Token = "shutdown-secret" //nolint:gosec // Test-only request credential.
	cfg.AuditLog = auditPath
	s, err := NewServer(cfg, dockerClient, &stubServerAdapter{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- s.httpServer.Serve(listener) }()

	clientConn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial server: %v", err)
	}
	defer clientConn.Close()
	request := "POST /v1.44/containers/abc123/attach?stdin=1&stdout=1 HTTP/1.1\r\n" +
		"Host: " + listener.Addr().String() + "\r\n" +
		"Authorization: Bearer shutdown-secret\r\n" +
		"Connection: Upgrade\r\n" +
		"Upgrade: tcp\r\n" +
		"Content-Length: 0\r\n\r\n"
	if _, err := io.WriteString(clientConn, request); err != nil {
		t.Fatalf("write attach request: %v", err)
	}
	clientReader := bufio.NewReader(clientConn)
	resp, err := http.ReadResponse(clientReader, nil)
	if err != nil {
		t.Fatalf("read attach response: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("attach status = %d, want %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelShutdown()
	if err := s.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set client read deadline: %v", err)
	}
	if _, err := clientReader.ReadByte(); !errors.Is(err, io.EOF) {
		t.Fatalf("client read after shutdown = %v, want EOF", err)
	}
	select {
	case err := <-daemonDone:
		if err != nil {
			t.Fatalf("Docker attach shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Docker attach connection remained open after shutdown")
	}

	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	if !containsAuditRecord(t, data, audit.EventAPIRequest, "", "/v1.44/containers/abc123/attach") {
		t.Fatalf("shutdown lost attach API audit record: %s", data)
	}
	if strings.Contains(string(data), `"event":"`+audit.EventExecStart+`"`) {
		t.Fatalf("attach emitted exec-only audit record: %s", data)
	}

	select {
	case serveErr := <-serveDone:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			t.Fatalf("Serve: %v", serveErr)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not return after shutdown")
	}
}

func TestShutdownIsSafeWhenCalledConcurrently(t *testing.T) {
	t.Parallel()

	dockerClient, stopDocker := newStubDockerClient(t)
	defer stopDocker()
	s, err := NewServer(minimalConfig(), dockerClient, &stubServerAdapter{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	const callers = 16
	start := make(chan struct{})
	errorsCh := make(chan error, callers)
	var callersWG sync.WaitGroup
	callersWG.Add(callers)
	for range callers {
		go func() {
			defer callersWG.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			errorsCh <- s.Shutdown(ctx)
		}()
	}
	close(start)
	callersWG.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Errorf("concurrent Shutdown: %v", err)
		}
	}
}

func containsAuditRecord(t *testing.T, data []byte, event, stack, path string) bool {
	t.Helper()
	for _, line := range splitNonEmptyLines(data) {
		var record struct {
			Event string `json:"event"`
			Stack string `json:"stack"`
			Path  string `json:"path"`
		}
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("decode audit record %q: %v", line, err)
		}
		if record.Event == event && (stack == "" || record.Stack == stack) && (path == "" || record.Path == path) {
			return true
		}
	}
	return false
}

func splitNonEmptyLines(data []byte) [][]byte {
	var lines [][]byte
	for len(data) > 0 {
		var line []byte
		for i, b := range data {
			if b == '\n' {
				line = data[:i]
				data = data[i+1:]
				break
			}
		}
		if line == nil {
			line = data
			data = nil
		}
		if len(line) > 0 {
			lines = append(lines, line)
		}
	}
	return lines
}
