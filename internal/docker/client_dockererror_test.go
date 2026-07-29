package docker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---- extractDockerErrorMessage ----

func TestExtractDockerErrorMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body []byte
		want string
	}{
		{
			name: "docker JSON message is extracted",
			body: []byte(`{"message":"exec denied: no commands are allowlisted"}`),
			want: "exec denied: no commands are allowlisted",
		},
		{
			name: "raw non-JSON body is trimmed and returned as-is",
			body: []byte("  no such container\n"),
			want: "no such container",
		},
		{
			name: "empty body yields empty message",
			body: []byte(""),
			want: "",
		},
		{
			name: "whitespace-only body yields empty message",
			body: []byte("   \n\t  "),
			want: "",
		},
		{
			name: "valid JSON with empty message falls back to raw trimmed body",
			body: []byte(`  {"message":""}  `),
			want: `{"message":""}`,
		},
		{
			name: "sockguard verbose denial: message and reason are combined",
			body: []byte(`{"message":"request denied by sockguard policy","method":"POST","path":"/containers/create","reason":"not allowed by portwing preset"}`),
			want: "request denied by sockguard policy: not allowed by portwing preset",
		},
		{
			name: "reason-only body yields the reason",
			body: []byte(`{"reason":"exec denied: privileged exec is not allowed"}`),
			want: "exec denied: privileged exec is not allowed",
		},
		{
			name: "identical message and reason are not duplicated",
			body: []byte(`{"message":"same text","reason":"same text"}`),
			want: "same text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extractDockerErrorMessage(tt.body)
			if got != tt.want {
				t.Errorf("extractDockerErrorMessage(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

// ---- dockerError ----

func TestDockerError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		action string
		status int
		body   []byte
		want   string
	}{
		{
			name:   "message present",
			action: "create exec",
			status: http.StatusForbidden,
			body:   []byte(`{"message":"exec denied: no commands are allowlisted"}`),
			want:   "create exec: docker error (status 403): exec denied: no commands are allowlisted",
		},
		{
			name:   "empty body yields bare status error",
			action: "resize exec",
			status: http.StatusNotFound,
			body:   nil,
			want:   "resize exec: docker error (status 404)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := dockerError(tt.action, tt.status, tt.body)
			if err == nil {
				t.Fatal("expected non-nil error")
			}
			if err.Error() != tt.want {
				t.Errorf("dockerError(...) = %q, want %q", err.Error(), tt.want)
			}
		})
	}
}

// ---- Call-site propagation: the extracted body message reaches the caller ----

func TestCreateExec_DockerError_MessageBody(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"exec denied: no commands are allowlisted"}`)) //nolint:errcheck
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.CreateExec(context.Background(), "abc123", []string{"sh"}, "", false)
	if err == nil {
		t.Fatal("expected error on 403, got nil")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error = %q, expected to contain status 403", err.Error())
	}
	if !strings.Contains(err.Error(), "exec denied: no commands are allowlisted") {
		t.Errorf("error = %q, expected to contain the docker error message", err.Error())
	}
}

func TestResizeExec_DockerError_MessageBody(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"exec denied: no commands are allowlisted"}`)) //nolint:errcheck
	}))
	defer srv.Close()

	c := newTestClient(srv)
	err := c.ResizeExec(context.Background(), "exec-id-123", 80, 24)
	if err == nil {
		t.Fatal("expected error on 403, got nil")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error = %q, expected to contain status 403", err.Error())
	}
	if !strings.Contains(err.Error(), "exec denied: no commands are allowlisted") {
		t.Errorf("error = %q, expected to contain the docker error message", err.Error())
	}
}

func TestGetContainerLogs_DockerError_MessageBody(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"exec denied: no commands are allowlisted"}`)) //nolint:errcheck
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.GetContainerLogs(context.Background(), "abc123", "", "", "", false, false)
	if err == nil {
		t.Fatal("expected error on 403, got nil")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error = %q, expected to contain status 403", err.Error())
	}
	if !strings.Contains(err.Error(), "exec denied: no commands are allowlisted") {
		t.Errorf("error = %q, expected to contain the docker error message", err.Error())
	}
}

func TestCreateExec_DockerError_MessageAndReasonBody(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"request denied by sockguard policy","method":"POST","path":"/containers/abc123/exec","reason":"exec denied: privileged exec is not allowed"}`)) //nolint:errcheck
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.CreateExec(context.Background(), "abc123", []string{"sh"}, "", false)
	if err == nil {
		t.Fatal("expected error on 403, got nil")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error = %q, expected to contain status 403", err.Error())
	}
	if !strings.Contains(err.Error(), "request denied by sockguard policy: exec denied: privileged exec is not allowed") {
		t.Errorf("error = %q, expected to contain the combined message and reason", err.Error())
	}
}

// ---- Truncation: the error body is bounded to maxDockerErrorBodyBytes ----

func TestCreateExec_DockerError_BodyTruncated(t *testing.T) {
	t.Parallel()

	const tailMarker = "TAIL_MARKER_SHOULD_NOT_APPEAR"
	oversized := strings.Repeat("a", maxDockerErrorBodyBytes+1024) + tailMarker

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(oversized)) //nolint:errcheck
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.CreateExec(context.Background(), "abc123", []string{"sh"}, "", false)
	if err == nil {
		t.Fatal("expected error on 403, got nil")
	}
	if strings.Contains(err.Error(), tailMarker) {
		t.Errorf("error = %q, expected the body to be truncated before the tail marker", err.Error())
	}
	// The message portion of the error can carry at most maxDockerErrorBodyBytes
	// of body content; the rest of the error text (action/status wrapper) is
	// fixed overhead, so bound the whole error generously above that.
	if len(err.Error()) > maxDockerErrorBodyBytes+128 {
		t.Errorf("error length = %d, expected roughly bounded by maxDockerErrorBodyBytes (%d)", len(err.Error()), maxDockerErrorBodyBytes)
	}
}
