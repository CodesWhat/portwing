package docker

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// errTransport always returns a network error, simulating connection failures.
type errTransport struct{ err error }

func (e *errTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, e.err
}

// newErrClient returns a Client whose both httpClient and streamClient always
// return a transport-level error (not a bad HTTP status).
func newErrClient() *Client {
	rt := &errTransport{err: fmt.Errorf("connection refused")}
	return &Client{
		socketPath:   "/var/run/docker.sock",
		apiVersion:   "v1.44",
		httpClient:   &http.Client{Transport: rt},
		streamClient: &http.Client{Transport: rt},
	}
}

// ---- Request-level errors (transport fails) ----

func TestGetVersion_RequestError(t *testing.T) {
	t.Parallel()
	c := newErrClient()
	_, err := c.GetVersion(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestPing_RequestError(t *testing.T) {
	t.Parallel()
	c := newErrClient()
	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestListContainers_RequestError(t *testing.T) {
	t.Parallel()
	c := newErrClient()
	_, err := c.ListContainers(context.Background(), true)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestInspectContainer_RequestError(t *testing.T) {
	t.Parallel()
	c := newErrClient()
	_, err := c.InspectContainer(context.Background(), "abc123")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestRemoveContainer_RequestError(t *testing.T) {
	t.Parallel()
	c := newErrClient()
	err := c.RemoveContainer(context.Background(), "abc123", false)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetContainerLogs_RequestError_NoFollow(t *testing.T) {
	t.Parallel()
	c := newErrClient()
	_, err := c.GetContainerLogs(context.Background(), "abc123", "", "", "", false, false)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetContainerLogs_RequestError_Follow(t *testing.T) {
	t.Parallel()
	c := newErrClient()
	_, err := c.GetContainerLogs(context.Background(), "abc123", "", "", "", true, false)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCreateExec_RequestError(t *testing.T) {
	t.Parallel()
	c := newErrClient()
	_, err := c.CreateExec(context.Background(), "abc123", []string{"sh"}, "", false)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestResizeExec_RequestError(t *testing.T) {
	t.Parallel()
	c := newErrClient()
	err := c.ResizeExec(context.Background(), "exec-id-123", 80, 24)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetEvents_RequestError(t *testing.T) {
	t.Parallel()
	c := newErrClient()
	_, err := c.GetEvents(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetDockerInfo_RequestError(t *testing.T) {
	t.Parallel()
	c := newErrClient()
	_, err := c.GetDockerInfo(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestContainerStats_RequestError(t *testing.T) {
	t.Parallel()
	c := newErrClient()
	_, err := c.ContainerStats(context.Background(), "abc123")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// ---- Do/DoStream request creation error (invalid method character) ----

func TestDo_InvalidMethod(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	// A null byte in the method name causes http.NewRequest to return an error.
	_, err := c.Do(context.Background(), "INVALID\x00METHOD", "/path", nil)
	if err == nil {
		t.Fatal("expected error for invalid method, got nil")
	}
}

func TestDoStream_InvalidMethod(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.DoStream(context.Background(), "INVALID\x00METHOD", "/path", nil)
	if err == nil {
		t.Fatal("expected error for invalid method, got nil")
	}
}

// ---- RemoveContainer returns 200 OK (treated as success) ----

func TestRemoveContainer_StatusOK(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	if err := c.RemoveContainer(context.Background(), "abc123", false); err != nil {
		t.Fatalf("RemoveContainer with 200: %v", err)
	}
}

// ---- ResizeExec body read error ----

// errBodyTransport serves a response where the body errors when read.
type errBodyTransport struct {
	status  int
	bodyErr error
}

type errBody struct{ err error }

func (b *errBody) Read(_ []byte) (int, error) { return 0, b.err }
func (b *errBody) Close() error               { return nil }

func (t *errBodyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: t.status,
		Body:       &errBody{err: t.bodyErr},
		Header:     make(http.Header),
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
	}, nil
}

func TestResizeExec_BodyReadError(t *testing.T) {
	t.Parallel()

	rt := &errBodyTransport{status: http.StatusNotFound, bodyErr: fmt.Errorf("body read failed")}
	c := &Client{
		socketPath:   "/var/run/docker.sock",
		apiVersion:   "v1.44",
		httpClient:   &http.Client{Transport: rt},
		streamClient: &http.Client{Transport: rt},
	}
	err := c.ResizeExec(context.Background(), "exec-id-123", 80, 24)
	if err == nil {
		t.Fatal("expected error on body read failure, got nil")
	}
	if !strings.Contains(err.Error(), "reading body") {
		t.Fatalf("error = %q, expected 'reading body'", err.Error())
	}
}

// ---- DoStream with nil body (no Content-Type) ----

func TestDoStream_NoContentTypeForNilBody(t *testing.T) {
	t.Parallel()

	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	resp, err := c.DoStream(context.Background(), http.MethodGet, "/stream/path", nil)
	if err != nil {
		t.Fatalf("DoStream: %v", err)
	}
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	resp.Body.Close()

	if gotContentType != "" {
		t.Fatalf("expected no Content-Type for nil body, got %q", gotContentType)
	}
}

// ---- negotiateAPIVersion request error ----

func TestNegotiateAPIVersion_RequestError(t *testing.T) {
	t.Parallel()

	c := newErrClient()
	// Should return an error (version request fails)
	err := c.negotiateAPIVersion(context.Background())
	if err == nil {
		t.Fatal("expected error from negotiateAPIVersion, got nil")
	}
}
