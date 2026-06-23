package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient builds a Client whose httpClient and streamClient both route
// through the given httptest.Server. The apiVersion is set to "v1.44" directly
// so no negotiation request is made during construction.
func newTestClient(srv *httptest.Server) *Client {
	// Route every request to the test server by rewriting the scheme+host.
	rt := &rewriteHostTransport{
		base:    srv.Client().Transport,
		baseURL: srv.URL,
	}

	return &Client{
		socketPath:   "/var/run/docker.sock",
		apiVersion:   "v1.44",
		httpClient:   &http.Client{Transport: rt},
		streamClient: &http.Client{Transport: rt},
	}
}

// rewriteHostTransport replaces the scheme+host of every outbound request so
// it lands on the given httptest.Server instead of the real Docker daemon.
type rewriteHostTransport struct {
	base    http.RoundTripper
	baseURL string // e.g. "http://127.0.0.1:PORT"
}

func (r *rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	host := strings.TrimPrefix(r.baseURL, "http://")
	cloned.URL.Scheme = "http"
	cloned.URL.Host = host
	return r.base.RoundTrip(cloned)
}

// ---- GetAPIVersion / GetSocketPath ----

func TestGetAPIVersion(t *testing.T) {
	t.Parallel()

	c := &Client{apiVersion: "v1.45"}
	if got := c.GetAPIVersion(); got != "v1.45" {
		t.Fatalf("GetAPIVersion() = %q, want %q", got, "v1.45")
	}
}

func TestGetSocketPath(t *testing.T) {
	t.Parallel()

	c := &Client{socketPath: "/tmp/docker.sock"}
	if got := c.GetSocketPath(); got != "/tmp/docker.sock" {
		t.Fatalf("GetSocketPath() = %q, want %q", got, "/tmp/docker.sock")
	}
}

// ---- validateContainerRef ----

func TestValidateContainerRef(t *testing.T) {
	t.Parallel()

	valid := []string{
		"abc123",
		"my-container",
		"my.container",
		"my_container",
		"a",
		strings.Repeat("a", 128),
	}
	for _, id := range valid {
		if err := validateContainerRef(id); err != nil {
			t.Errorf("validateContainerRef(%q) = %v, want nil", id, err)
		}
	}

	invalid := []string{
		"",
		"-starts-with-dash",
		".starts-with-dot",
		strings.Repeat("a", 129),
		"has/slash",
		"has space",
	}
	for _, id := range invalid {
		if err := validateContainerRef(id); err == nil {
			t.Errorf("validateContainerRef(%q): expected error, got nil", id)
		}
	}
}

// ---- buildURL ----

func TestBuildURL(t *testing.T) {
	t.Parallel()

	c := &Client{apiVersion: "v1.44"}
	got := c.buildURL("/containers/json")
	want := "http://localhost/v1.44/containers/json"
	if got != want {
		t.Fatalf("buildURL = %q, want %q", got, want)
	}
}

// ---- NewClient: empty socket path ----

func TestNewClientEmptySocketPath(t *testing.T) {
	t.Parallel()

	_, err := NewClient("", 10)
	if err == nil {
		t.Fatal("NewClient with empty socket path: expected error, got nil")
	}
}

// ---- GetVersion ----

func TestGetVersion_Success(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.44/version" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(VersionResponse{Version: "24.0.5", APIVersion: "1.44"}) //nolint:errcheck
	}))
	defer srv.Close()

	c := newTestClient(srv)
	ver, err := c.GetVersion(context.Background())
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if ver != "24.0.5" {
		t.Fatalf("GetVersion = %q, want %q", ver, "24.0.5")
	}
}

func TestGetVersion_BadJSON(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not-json")) //nolint:errcheck
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.GetVersion(context.Background())
	if err == nil {
		t.Fatal("expected error on bad JSON, got nil")
	}
}

// ---- Ping ----

func TestPing_Success(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_ping" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK")) //nolint:errcheck
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestPing_NonOK(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("expected error on non-200, got nil")
	}
}

// ---- ListContainers ----

func TestListContainers_Success(t *testing.T) {
	t.Parallel()

	containers := []ContainerJSON{
		{ID: "abc123", Names: []string{"/myapp"}, Image: "nginx:latest", State: "running"},
		{ID: "def456", Names: []string{"/mydb"}, Image: "postgres:15", State: "exited"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(containers) //nolint:errcheck
	}))
	defer srv.Close()

	c := newTestClient(srv)
	got, err := c.ListContainers(context.Background(), true)
	if err != nil {
		t.Fatalf("ListContainers: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListContainers: got %d containers, want 2", len(got))
	}
	if got[0].ID != "abc123" {
		t.Fatalf("ListContainers[0].ID = %q, want %q", got[0].ID, "abc123")
	}
}

func TestListContainers_AllFalseOmitsParam(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("expected no query params when all=false, got %q", r.URL.RawQuery)
		}
		json.NewEncoder(w).Encode([]ContainerJSON{}) //nolint:errcheck
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.ListContainers(context.Background(), false)
	if err != nil {
		t.Fatalf("ListContainers: %v", err)
	}
}

func TestListContainers_DockerError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "daemon error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.ListContainers(context.Background(), true)
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
}

func TestListContainers_BadJSON(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not-json")) //nolint:errcheck
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.ListContainers(context.Background(), true)
	if err == nil {
		t.Fatal("expected error on bad JSON, got nil")
	}
}

// ---- InspectContainer ----

func TestInspectContainer_Success(t *testing.T) {
	t.Parallel()

	inspect := ContainerInspect{
		ID:   "abc123",
		Name: "/myapp",
		State: ContainerState{
			Status:  "running",
			Running: true,
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(inspect) //nolint:errcheck
	}))
	defer srv.Close()

	c := newTestClient(srv)
	got, err := c.InspectContainer(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("InspectContainer: %v", err)
	}
	if got.ID != "abc123" {
		t.Fatalf("InspectContainer.ID = %q, want %q", got.ID, "abc123")
	}
	if !got.State.Running {
		t.Fatal("InspectContainer.State.Running should be true")
	}
}

func TestInspectContainer_InvalidRef(t *testing.T) {
	t.Parallel()

	c := &Client{apiVersion: "v1.44"}
	_, err := c.InspectContainer(context.Background(), "../evil")
	if err == nil {
		t.Fatal("expected error for invalid container ref, got nil")
	}
}

func TestInspectContainer_DockerError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no such container", http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.InspectContainer(context.Background(), "abc123")
	if err == nil {
		t.Fatal("expected error on 404, got nil")
	}
}

func TestInspectContainer_BadJSON(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{broken")) //nolint:errcheck
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.InspectContainer(context.Background(), "abc123")
	if err == nil {
		t.Fatal("expected error on bad JSON, got nil")
	}
}

// ---- RemoveContainer ----

func TestRemoveContainer_Success(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	if err := c.RemoveContainer(context.Background(), "abc123", false); err != nil {
		t.Fatalf("RemoveContainer: %v", err)
	}
}

func TestRemoveContainer_Force(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "force=1") {
			t.Errorf("expected force=1 query param, got %q", r.URL.RawQuery)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	if err := c.RemoveContainer(context.Background(), "abc123", true); err != nil {
		t.Fatalf("RemoveContainer(force): %v", err)
	}
}

func TestRemoveContainer_InvalidRef(t *testing.T) {
	t.Parallel()

	c := &Client{apiVersion: "v1.44"}
	err := c.RemoveContainer(context.Background(), "../evil", false)
	if err == nil {
		t.Fatal("expected error for invalid container ref, got nil")
	}
}

func TestRemoveContainer_DockerError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "container in use", http.StatusConflict)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	if err := c.RemoveContainer(context.Background(), "abc123", false); err == nil {
		t.Fatal("expected error on 409, got nil")
	}
}

// ---- GetContainerLogs ----

func TestGetContainerLogs_Success(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("log line 1\nlog line 2\n")) //nolint:errcheck
	}))
	defer srv.Close()

	c := newTestClient(srv)
	rc, err := c.GetContainerLogs(context.Background(), "abc123", "100", "", "", false, false)
	if err != nil {
		t.Fatalf("GetContainerLogs: %v", err)
	}
	defer rc.Close()

	data, _ := io.ReadAll(rc)
	if !strings.Contains(string(data), "log line 1") {
		t.Fatalf("GetContainerLogs: unexpected body %q", string(data))
	}
}

func TestGetContainerLogs_InvalidRef(t *testing.T) {
	t.Parallel()

	c := &Client{apiVersion: "v1.44"}
	_, err := c.GetContainerLogs(context.Background(), "../evil", "", "", "", false, false)
	if err == nil {
		t.Fatal("expected error for invalid ref, got nil")
	}
}

func TestGetContainerLogs_DockerError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no such container", http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.GetContainerLogs(context.Background(), "abc123", "", "", "", false, false)
	if err == nil {
		t.Fatal("expected error on 404, got nil")
	}
}

func TestGetContainerLogs_WithTimestampsAndSince(t *testing.T) {
	t.Parallel()

	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	rc, err := c.GetContainerLogs(context.Background(), "abc123", "50", "2024-01-01", "2024-12-31", false, true)
	if err != nil {
		t.Fatalf("GetContainerLogs: %v", err)
	}
	if rc != nil {
		rc.Close()
	}

	if !strings.Contains(gotQuery, "timestamps=1") {
		t.Errorf("expected timestamps=1 in query, got %q", gotQuery)
	}
	if !strings.Contains(gotQuery, "since=2024-01-01") {
		t.Errorf("expected since param in query, got %q", gotQuery)
	}
	if !strings.Contains(gotQuery, "until=2024-12-31") {
		t.Errorf("expected until param in query, got %q", gotQuery)
	}
	if !strings.Contains(gotQuery, "tail=50") {
		t.Errorf("expected tail=50 in query, got %q", gotQuery)
	}
}

func TestGetContainerLogs_Follow(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "follow=1") {
			t.Errorf("expected follow=1 in query, got %q", r.URL.RawQuery)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("streaming...\n")) //nolint:errcheck
	}))
	defer srv.Close()

	c := newTestClient(srv)
	rc, err := c.GetContainerLogs(context.Background(), "abc123", "", "", "", true, false)
	if err != nil {
		t.Fatalf("GetContainerLogs(follow): %v", err)
	}
	if rc != nil {
		rc.Close()
	}
}

// ---- CreateExec ----

func TestCreateExec_Success(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(struct { //nolint:errcheck
			ID string `json:"Id"`
		}{ID: "exec-id-123"})
	}))
	defer srv.Close()

	c := newTestClient(srv)
	id, err := c.CreateExec(context.Background(), "abc123", []string{"sh", "-c", "echo hi"}, "", false)
	if err != nil {
		t.Fatalf("CreateExec: %v", err)
	}
	if id != "exec-id-123" {
		t.Fatalf("CreateExec ID = %q, want %q", id, "exec-id-123")
	}
}

func TestCreateExec_WithUser(t *testing.T) {
	t.Parallel()

	var gotBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody) //nolint:errcheck
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(struct { //nolint:errcheck
			ID string `json:"Id"`
		}{ID: "exec-id-456"})
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.CreateExec(context.Background(), "abc123", []string{"id"}, "root", true)
	if err != nil {
		t.Fatalf("CreateExec with user: %v", err)
	}
	if gotBody["User"] != "root" {
		t.Fatalf("exec config User = %v, want %q", gotBody["User"], "root")
	}
	if gotBody["Tty"] != true {
		t.Fatalf("exec config Tty = %v, want true", gotBody["Tty"])
	}
}

func TestCreateExec_InvalidRef(t *testing.T) {
	t.Parallel()

	c := &Client{apiVersion: "v1.44"}
	_, err := c.CreateExec(context.Background(), "../evil", []string{"sh"}, "", false)
	if err == nil {
		t.Fatal("expected error for invalid ref, got nil")
	}
}

func TestCreateExec_DockerError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no such container", http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.CreateExec(context.Background(), "abc123", []string{"sh"}, "", false)
	if err == nil {
		t.Fatal("expected error on 404, got nil")
	}
}

func TestCreateExec_BadJSON(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("{broken")) //nolint:errcheck
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.CreateExec(context.Background(), "abc123", []string{"sh"}, "", false)
	if err == nil {
		t.Fatal("expected error on bad JSON, got nil")
	}
}

// ---- ResizeExec ----

func TestResizeExec_Success(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "h=") || !strings.Contains(r.URL.RawQuery, "w=") {
			t.Errorf("expected h= and w= params, got %q", r.URL.RawQuery)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	if err := c.ResizeExec(context.Background(), "exec-id-123", 80, 24); err != nil {
		t.Fatalf("ResizeExec: %v", err)
	}
}

func TestResizeExec_Created(t *testing.T) {
	t.Parallel()

	// Some Docker versions return 201 for resize.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	if err := c.ResizeExec(context.Background(), "exec-id-123", 80, 24); err != nil {
		t.Fatalf("ResizeExec (201): %v", err)
	}
}

func TestResizeExec_DockerError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("exec not found")) //nolint:errcheck
	}))
	defer srv.Close()

	c := newTestClient(srv)
	if err := c.ResizeExec(context.Background(), "exec-id-123", 80, 24); err == nil {
		t.Fatal("expected error on 404, got nil")
	}
}

// ---- GetEvents ----

func TestGetEvents_Success(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(DockerEvent{ //nolint:errcheck
			ID:     "abc123",
			Action: "start",
			Type:   "container",
		})
	}))
	defer srv.Close()

	c := newTestClient(srv)
	rc, err := c.GetEvents(context.Background())
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	defer rc.Close()

	data, _ := io.ReadAll(rc)
	if len(data) == 0 {
		t.Fatal("GetEvents: got empty body")
	}
}

func TestGetEvents_DockerError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.GetEvents(context.Background())
	if err == nil {
		t.Fatal("expected error on 403, got nil")
	}
}

// ---- GetDockerInfo ----

func TestGetDockerInfo_Success(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(DockerInfo{DockerRootDir: "/var/lib/docker"}) //nolint:errcheck
	}))
	defer srv.Close()

	c := newTestClient(srv)
	info, err := c.GetDockerInfo(context.Background())
	if err != nil {
		t.Fatalf("GetDockerInfo: %v", err)
	}
	if info.DockerRootDir != "/var/lib/docker" {
		t.Fatalf("GetDockerInfo.DockerRootDir = %q, want %q", info.DockerRootDir, "/var/lib/docker")
	}
}

func TestGetDockerInfo_DockerError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.GetDockerInfo(context.Background())
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
}

func TestGetDockerInfo_BadJSON(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{broken")) //nolint:errcheck
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.GetDockerInfo(context.Background())
	if err == nil {
		t.Fatal("expected error on bad JSON, got nil")
	}
}

// ---- ContainerStats ----

func TestContainerStats_Success(t *testing.T) {
	t.Parallel()

	stats := ContainerStatsResponse{}
	stats.CPUStats.CPUUsage.TotalUsage = 123456789
	stats.MemoryStats.Usage = 512 * 1024 * 1024
	stats.MemoryStats.Limit = 2 * 1024 * 1024 * 1024

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(stats) //nolint:errcheck
	}))
	defer srv.Close()

	c := newTestClient(srv)
	got, err := c.ContainerStats(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("ContainerStats: %v", err)
	}
	if got.CPUStats.CPUUsage.TotalUsage != 123456789 {
		t.Fatalf("CPUStats.TotalUsage = %d, want %d", got.CPUStats.CPUUsage.TotalUsage, 123456789)
	}
}

func TestContainerStats_InvalidRef(t *testing.T) {
	t.Parallel()

	c := &Client{apiVersion: "v1.44"}
	_, err := c.ContainerStats(context.Background(), "../evil")
	if err == nil {
		t.Fatal("expected error for invalid ref, got nil")
	}
}

func TestContainerStats_DockerError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no such container", http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.ContainerStats(context.Background(), "abc123")
	if err == nil {
		t.Fatal("expected error on 404, got nil")
	}
}

func TestContainerStats_BadJSON(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{broken")) //nolint:errcheck
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.ContainerStats(context.Background(), "abc123")
	if err == nil {
		t.Fatal("expected error on bad JSON, got nil")
	}
}

// ---- Do / DoStream: Content-Type header ----

func TestDo_SetsContentTypeForBodyRequests(t *testing.T) {
	t.Parallel()

	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	body := strings.NewReader(`{"foo":"bar"}`)
	resp, err := c.Do(context.Background(), http.MethodPost, "/some/path", body)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	if gotContentType != "application/json" {
		t.Fatalf("Content-Type = %q, want %q", gotContentType, "application/json")
	}
}

func TestDo_NoContentTypeForNilBody(t *testing.T) {
	t.Parallel()

	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	resp, err := c.Do(context.Background(), http.MethodGet, "/some/path", nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	if gotContentType != "" {
		t.Fatalf("expected no Content-Type for nil body, got %q", gotContentType)
	}
}

func TestDoStream_SetsContentTypeForBodyRequests(t *testing.T) {
	t.Parallel()

	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	body := strings.NewReader(`{"foo":"bar"}`)
	resp, err := c.DoStream(context.Background(), http.MethodPost, "/stream/path", body)
	if err != nil {
		t.Fatalf("DoStream: %v", err)
	}
	resp.Body.Close()

	if gotContentType != "application/json" {
		t.Fatalf("Content-Type = %q, want %q", gotContentType, "application/json")
	}
}

// ---- negotiateAPIVersion ----

func TestNegotiateAPIVersion_SetsVersion(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(VersionResponse{Version: "24.0.5", APIVersion: "1.45"}) //nolint:errcheck
	}))
	defer srv.Close()

	c := newTestClient(srv)
	c.apiVersion = ""

	if err := c.negotiateAPIVersion(context.Background()); err != nil {
		t.Fatalf("negotiateAPIVersion: %v", err)
	}
	if c.apiVersion != "v1.45" {
		t.Fatalf("apiVersion = %q, want %q", c.apiVersion, "v1.45")
	}
}

func TestNegotiateAPIVersion_EmptyAPIVersionFallback(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(VersionResponse{Version: "24.0.5", APIVersion: ""}) //nolint:errcheck
	}))
	defer srv.Close()

	c := newTestClient(srv)
	c.apiVersion = ""

	if err := c.negotiateAPIVersion(context.Background()); err != nil {
		t.Fatalf("negotiateAPIVersion: %v", err)
	}
	if c.apiVersion != "v1.44" {
		t.Fatalf("apiVersion = %q, want fallback %q", c.apiVersion, "v1.44")
	}
}

func TestNegotiateAPIVersion_BadJSON(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not-json")) //nolint:errcheck
	}))
	defer srv.Close()

	c := newTestClient(srv)
	if err := c.negotiateAPIVersion(context.Background()); err == nil {
		t.Fatal("expected error on bad JSON, got nil")
	}
}

// ---- DoRaw / DoStreamRaw ----

func TestDoRaw_ForwardsRequest(t *testing.T) {
	t.Parallel()

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(srv)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/some/raw/path", nil)
	resp, err := c.DoRaw(req)
	if err != nil {
		t.Fatalf("DoRaw: %v", err)
	}
	resp.Body.Close()

	if gotPath != "/some/raw/path" {
		t.Fatalf("DoRaw path = %q, want %q", gotPath, "/some/raw/path")
	}
}

func TestDoStreamRaw_ForwardsRequest(t *testing.T) {
	t.Parallel()

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(srv)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/stream/raw/path", nil)
	resp, err := c.DoStreamRaw(req)
	if err != nil {
		t.Fatalf("DoStreamRaw: %v", err)
	}
	resp.Body.Close()

	if gotPath != "/stream/raw/path" {
		t.Fatalf("DoStreamRaw path = %q, want %q", gotPath, "/stream/raw/path")
	}
}

// ---- bufferedConn ----

func TestBufferedConn_Read(t *testing.T) {
	t.Parallel()

	// bufferedConn wraps a net.Conn with a bufio.Reader. We test the Read
	// path by constructing one directly with a pipe as the underlying conn.
	pr, pw := io.Pipe()
	go func() {
		pw.Write([]byte("hello buffered world")) //nolint:errcheck
		_ = pw.Close()
	}()

	br := bufio.NewReader(pr)
	// Read one byte to put something in the buffer.
	_, _ = br.ReadByte()
	_ = br.UnreadByte()

	bc := &bufferedConn{Conn: nil, reader: br}
	buf := make([]byte, 5)
	n, err := bc.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("bufferedConn.Read: %v", err)
	}
	if n == 0 {
		t.Fatal("bufferedConn.Read: got 0 bytes")
	}
	if string(buf[:n]) != "hello" {
		t.Fatalf("bufferedConn.Read: got %q, want %q", string(buf[:n]), "hello")
	}
}

// ---- readAndCloseBody ----

func TestReadAndCloseBody(t *testing.T) {
	t.Parallel()

	rc := io.NopCloser(strings.NewReader("hello"))
	got := readAndCloseBody(rc)
	if got != "hello" {
		t.Fatalf("readAndCloseBody = %q, want %q", got, "hello")
	}
}

// errCloser is a ReadCloser whose Close method always returns an error.
type errCloser struct {
	r        io.Reader
	closeErr error
}

func (e *errCloser) Read(p []byte) (int, error) { return e.r.Read(p) }
func (e *errCloser) Close() error               { return e.closeErr }

// errReaderCloser combines a read error with a Close error.
type errReaderCloser struct {
	readErr  error
	closeErr error
}

func (e *errReaderCloser) Read(_ []byte) (int, error) { return 0, e.readErr }
func (e *errReaderCloser) Close() error               { return e.closeErr }

func TestReadAndCloseBody_CloseError(t *testing.T) {
	t.Parallel()

	closeErr := fmt.Errorf("close failed")
	rc := &errCloser{r: strings.NewReader("data"), closeErr: closeErr}
	got := readAndCloseBody(rc)
	// Should contain the data and the close error.
	if !strings.Contains(got, "data") {
		t.Errorf("readAndCloseBody: expected data in result, got %q", got)
	}
	if !strings.Contains(got, "close failed") {
		t.Errorf("readAndCloseBody: expected close error in result, got %q", got)
	}
}

func TestReadAndCloseBody_ReadError(t *testing.T) {
	t.Parallel()

	readErr := fmt.Errorf("read failed")
	rc := &errReaderCloser{readErr: readErr, closeErr: nil}
	got := readAndCloseBody(rc)
	if !strings.Contains(got, "read failed") {
		t.Errorf("readAndCloseBody: expected read error in result, got %q", got)
	}
}

func TestReadAndCloseBody_BothErrors(t *testing.T) {
	t.Parallel()

	readErr := fmt.Errorf("read failed")
	closeErr := fmt.Errorf("close failed")
	rc := &errReaderCloser{readErr: readErr, closeErr: closeErr}
	got := readAndCloseBody(rc)
	if !strings.Contains(got, "read failed") {
		t.Errorf("readAndCloseBody: expected read error in result, got %q", got)
	}
	if !strings.Contains(got, "close failed") {
		t.Errorf("readAndCloseBody: expected close error in result, got %q", got)
	}
}
