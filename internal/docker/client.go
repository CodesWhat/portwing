package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

// Types for Docker API responses.

type ContainerJSON struct {
	ID              string            `json:"Id"`
	Names           []string          `json:"Names"`
	Image           string            `json:"Image"`
	ImageID         string            `json:"ImageID"`
	State           string            `json:"State"`
	Status          string            `json:"Status"`
	Labels          map[string]string `json:"Labels"`
	Created         int64             `json:"Created"`
	Ports           []PortBinding     `json:"Ports"`
	Mounts          []MountPoint      `json:"Mounts"`
	NetworkSettings *NetworkSettings  `json:"NetworkSettings,omitempty"`
}

type ContainerInspect struct {
	ID              string           `json:"Id"`
	Name            string           `json:"Name"`
	State           ContainerState   `json:"State"`
	Config          ContainerConfig  `json:"Config"`
	Image           string           `json:"Image"`
	Created         string           `json:"Created"`
	HostConfig      *HostConfig      `json:"HostConfig,omitempty"`
	NetworkSettings *NetworkSettings `json:"NetworkSettings,omitempty"`
	Mounts          []MountPoint     `json:"Mounts"`
	Platform        string           `json:"Platform,omitempty"`
}

type ContainerState struct {
	Status     string       `json:"Status"`
	Running    bool         `json:"Running"`
	Paused     bool         `json:"Paused"`
	Restarting bool         `json:"Restarting"`
	OOMKilled  bool         `json:"OOMKilled"`
	Dead       bool         `json:"Dead"`
	Pid        int          `json:"Pid"`
	ExitCode   int          `json:"ExitCode"`
	StartedAt  string       `json:"StartedAt"`
	FinishedAt string       `json:"FinishedAt"`
	Health     *HealthState `json:"Health,omitempty"`
}

type HealthState struct {
	Status string `json:"Status"`
}

type ContainerConfig struct {
	Image    string            `json:"Image"`
	Labels   map[string]string `json:"Labels"`
	Cmd      []string          `json:"Cmd"`
	Env      []string          `json:"Env"`
	Hostname string            `json:"Hostname"`
}

type HostConfig struct {
	RestartPolicy RestartPolicy `json:"RestartPolicy"`
}

type RestartPolicy struct {
	Name string `json:"Name"`
}

type PortBinding struct {
	IP          string `json:"IP,omitempty"`
	PrivatePort uint16 `json:"PrivatePort"`
	PublicPort  uint16 `json:"PublicPort,omitempty"`
	Type        string `json:"Type"`
}

type MountPoint struct {
	Type        string `json:"Type"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	RW          bool   `json:"RW"`
}

type NetworkSettings struct {
	Networks map[string]NetworkEndpoint `json:"Networks"`
}

type NetworkEndpoint struct {
	IPAddress string `json:"IPAddress"`
	Gateway   string `json:"Gateway"`
}

type DockerInfo struct {
	DockerRootDir string `json:"DockerRootDir"`
}

type VersionResponse struct {
	Version    string `json:"Version"`
	APIVersion string `json:"ApiVersion"`
}

// Client communicates with the Docker daemon over a Unix domain socket
// using raw HTTP (no Docker SDK).
type Client struct {
	socketPath   string
	apiVersion   string
	httpClient   *http.Client
	streamClient *http.Client
}

// NewClient creates a Docker API client that talks to the daemon via the
// given Unix socket. requestTimeout is in seconds; it applies to normal
// requests but not to streaming endpoints.
func NewClient(socketPath string, requestTimeout int) (*Client, error) {
	if socketPath == "" {
		return nil, fmt.Errorf("docker socket path is required")
	}

	timeout := time.Duration(requestTimeout) * time.Second

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return net.DialTimeout("unix", socketPath, timeout)
		},
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
	}

	streamTransport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return net.DialTimeout("unix", socketPath, timeout)
		},
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
	}

	c := &Client{
		socketPath: socketPath,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   timeout,
		},
		streamClient: &http.Client{
			Transport: streamTransport,
			// No timeout for streaming operations.
		},
	}

	// Negotiate API version with the daemon.
	if err := c.negotiateAPIVersion(context.Background()); err != nil {
		c.apiVersion = "v1.44" // fallback
	}

	return c, nil
}

// negotiateAPIVersion queries the Docker daemon for its API version and
// stores it for subsequent requests.
func (c *Client) negotiateAPIVersion(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/version", nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("version request: %w", err)
	}
	defer resp.Body.Close()

	var ver VersionResponse
	if err := json.NewDecoder(resp.Body).Decode(&ver); err != nil {
		return fmt.Errorf("decoding version: %w", err)
	}

	if ver.APIVersion != "" {
		c.apiVersion = "v" + ver.APIVersion
	} else {
		c.apiVersion = "v1.44"
	}

	return nil
}

// buildURL returns a full URL with the negotiated API version prefix.
// The host is irrelevant for Unix-socket transport.
func (c *Client) buildURL(path string) string {
	return "http://localhost/" + c.apiVersion + path
}

// Do performs a normal HTTP request against the Docker daemon.
func (c *Client) Do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.buildURL(path), body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.httpClient.Do(req)
}

// DoStream performs an HTTP request using the streaming client (no timeout).
func (c *Client) DoStream(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.buildURL(path), body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.streamClient.Do(req)
}

// DoRaw forwards an arbitrary *http.Request to the Docker daemon using the
// normal (timeout-bound) client. The caller must rewrite the URL before
// calling this if the request was received from an external source.
func (c *Client) DoRaw(req *http.Request) (*http.Response, error) {
	// #nosec G704 -- caller rewrites requests to the fixed local Docker endpoint before forwarding.
	return c.httpClient.Do(req)
}

// DoStreamRaw forwards an arbitrary *http.Request using the streaming client.
func (c *Client) DoStreamRaw(req *http.Request) (*http.Response, error) {
	// #nosec G704 -- caller rewrites requests to the fixed local Docker endpoint before forwarding.
	return c.streamClient.Do(req)
}

// GetVersion returns the Docker daemon version string.
func (c *Client) GetVersion(ctx context.Context) (string, error) {
	resp, err := c.Do(ctx, http.MethodGet, "/version", nil)
	if err != nil {
		return "", fmt.Errorf("docker version request: %w", err)
	}
	defer resp.Body.Close()

	var ver VersionResponse
	if err := json.NewDecoder(resp.Body).Decode(&ver); err != nil {
		return "", fmt.Errorf("decoding docker version: %w", err)
	}
	return ver.Version, nil
}

// Ping checks connectivity to the Docker daemon.
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/_ping", nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("docker ping: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("docker ping: unexpected status %d", resp.StatusCode)
	}
	return nil
}

// containerRefPattern matches valid Docker container IDs and names.
// Docker names allow [a-zA-Z0-9_.-] and must start with an alphanumeric.
var containerRefPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.\-]{0,127}$`)

// validateContainerRef returns an error if ref is not a valid Docker container
// name or ID (safe to interpolate into a URL path segment).
func validateContainerRef(ref string) error {
	if !containerRefPattern.MatchString(ref) {
		return fmt.Errorf("invalid container id/name: %q", ref)
	}
	return nil
}

// ListContainers returns all containers (or only running ones if all is false).
func (c *Client) ListContainers(ctx context.Context, all bool) ([]ContainerJSON, error) {
	path := "/containers/json"
	if all {
		path += "?all=1"
	}

	resp, err := c.Do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		slog.Warn("docker error", "method", "ListContainers", "status", resp.StatusCode, "body", string(body))
		return nil, fmt.Errorf("list containers: docker error (status %d)", resp.StatusCode)
	}

	var containers []ContainerJSON
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		return nil, fmt.Errorf("decoding containers: %w", err)
	}
	return containers, nil
}

// InspectContainer returns detailed information about a single container.
func (c *Client) InspectContainer(ctx context.Context, id string) (*ContainerInspect, error) {
	if err := validateContainerRef(id); err != nil {
		return nil, fmt.Errorf("inspect container: %w", err)
	}
	resp, err := c.Do(ctx, http.MethodGet, "/containers/"+id+"/json", nil)
	if err != nil {
		return nil, fmt.Errorf("inspect container: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		slog.Warn("docker error", "method", "InspectContainer", "path", "/containers/"+id+"/json", "status", resp.StatusCode, "body", string(body))
		return nil, fmt.Errorf("inspect container: docker error (status %d)", resp.StatusCode)
	}

	var info ContainerInspect
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decoding container inspect: %w", err)
	}
	return &info, nil
}

// RemoveContainer deletes a container. If force is true, the container is
// killed before removal.
func (c *Client) RemoveContainer(ctx context.Context, id string, force bool) error {
	if err := validateContainerRef(id); err != nil {
		return fmt.Errorf("remove container: %w", err)
	}
	path := "/containers/" + id
	if force {
		path += "?force=1"
	}

	resp, err := c.Do(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return fmt.Errorf("remove container: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		slog.Warn("docker error", "method", "RemoveContainer", "path", path, "status", resp.StatusCode, "body", string(body))
		return fmt.Errorf("remove container: docker error (status %d)", resp.StatusCode)
	}
	return nil
}

// GetContainerLogs returns a stream of container logs. The caller is
// responsible for closing the returned ReadCloser.
func (c *Client) GetContainerLogs(ctx context.Context, id, tail, since, until string, follow, timestamps bool) (io.ReadCloser, error) {
	if err := validateContainerRef(id); err != nil {
		return nil, fmt.Errorf("container logs: %w", err)
	}

	q := url.Values{}
	q.Set("stdout", "1")
	q.Set("stderr", "1")
	if tail != "" {
		q.Set("tail", tail)
	}
	if since != "" {
		q.Set("since", since)
	}
	if until != "" {
		q.Set("until", until)
	}
	if follow {
		q.Set("follow", "1")
	}
	if timestamps {
		q.Set("timestamps", "1")
	}

	path := "/containers/" + id + "/logs?" + q.Encode()

	var resp *http.Response
	var err error
	if follow {
		resp, err = c.DoStream(ctx, http.MethodGet, path, nil)
	} else {
		resp, err = c.Do(ctx, http.MethodGet, path, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("container logs: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body := readAndCloseBody(resp.Body)
		slog.Warn("docker error", "method", "GetContainerLogs", "path", "/containers/"+id+"/logs", "status", resp.StatusCode, "body", body)
		return nil, fmt.Errorf("container logs: docker error (status %d)", resp.StatusCode)
	}

	return resp.Body, nil
}

// CreateExec creates an exec instance in the given container and returns
// the exec ID.
func (c *Client) CreateExec(ctx context.Context, containerID string, cmd []string, user string, tty bool) (string, error) {
	if err := validateContainerRef(containerID); err != nil {
		return "", fmt.Errorf("create exec: %w", err)
	}

	type execConfig struct {
		AttachStdin  bool     `json:"AttachStdin"`
		AttachStdout bool     `json:"AttachStdout"`
		AttachStderr bool     `json:"AttachStderr"`
		Tty          bool     `json:"Tty"`
		Cmd          []string `json:"Cmd"`
		User         string   `json:"User,omitempty"`
	}

	cfg := execConfig{
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          tty,
		Cmd:          cmd,
		User:         user,
	}

	payload, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshaling exec config: %w", err)
	}

	resp, err := c.Do(ctx, http.MethodPost, "/containers/"+containerID+"/exec", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create exec: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		slog.Warn("docker error", "method", "CreateExec", "path", "/containers/"+containerID+"/exec", "status", resp.StatusCode, "body", string(body))
		return "", fmt.Errorf("create exec: docker error (status %d)", resp.StatusCode)
	}

	var result struct {
		ID string `json:"Id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding exec id: %w", err)
	}
	return result.ID, nil
}

// StartExec starts a previously created exec instance and returns the raw
// hijacked connection for bidirectional I/O. The Docker API responds with
// 101 Switching Protocols.
func (c *Client) StartExec(ctx context.Context, execID string, tty bool) (net.Conn, error) {
	body := fmt.Sprintf(`{"Detach":false,"Tty":%v}`, tty)

	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return nil, fmt.Errorf("dial docker socket: %w", err)
	}

	path := fmt.Sprintf("/%s/exec/%s/start", c.apiVersion, execID)
	raw := fmt.Sprintf(
		"POST %s HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nConnection: Upgrade\r\nUpgrade: tcp\r\nContent-Length: %d\r\n\r\n%s",
		path, len(body), body,
	)

	if _, err := conn.Write([]byte(raw)); err != nil {
		closeConn(conn, "exec start write failure")
		return nil, fmt.Errorf("writing exec start request: %w", err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		closeConn(conn, "exec start response failure")
		return nil, fmt.Errorf("reading exec start response: %w", err)
	}

	if resp.StatusCode != http.StatusSwitchingProtocols {
		closeConn(conn, "exec start unexpected status")
		return nil, fmt.Errorf("expected 101 Switching Protocols, got %d", resp.StatusCode)
	}

	// If the bufio reader has consumed bytes past the HTTP response, wrap
	// the connection so those bytes are not lost.
	if br.Buffered() > 0 {
		return &bufferedConn{Conn: conn, reader: br}, nil
	}

	return conn, nil
}

// bufferedConn wraps a net.Conn and prepends any bytes that were buffered
// by a bufio.Reader during the HTTP upgrade handshake.
type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (bc *bufferedConn) Read(p []byte) (int, error) {
	return bc.reader.Read(p)
}

// ResizeExec changes the TTY dimensions for a running exec instance.
func (c *Client) ResizeExec(ctx context.Context, execID string, cols, rows int) error {
	path := fmt.Sprintf("/exec/%s/resize?h=%d&w=%d", execID, rows, cols)
	resp, err := c.Do(ctx, http.MethodPost, path, nil)
	if err != nil {
		return fmt.Errorf("resize exec: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("resize exec: status %d: reading body: %w", resp.StatusCode, err)
		}
		return fmt.Errorf("resize exec: status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// GetEvents opens a streaming connection to the Docker events endpoint,
// filtered to container events. The caller must close the returned ReadCloser.
func (c *Client) GetEvents(ctx context.Context) (io.ReadCloser, error) {
	resp, err := c.DoStream(ctx, http.MethodGet, "/events?type=container", nil)
	if err != nil {
		return nil, fmt.Errorf("docker events: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body := readAndCloseBody(resp.Body)
		return nil, fmt.Errorf("docker events: status %d: %s", resp.StatusCode, string(body))
	}

	return resp.Body, nil
}

func readAndCloseBody(body io.ReadCloser) string {
	data, readErr := io.ReadAll(body)
	closeErr := body.Close()
	switch {
	case readErr != nil && closeErr != nil:
		return fmt.Sprintf("reading body: %v; closing body: %v", readErr, closeErr)
	case readErr != nil:
		return fmt.Sprintf("reading body: %v", readErr)
	case closeErr != nil:
		return fmt.Sprintf("%s; closing body: %v", string(data), closeErr)
	default:
		return string(data)
	}
}

func closeConn(conn net.Conn, context string) {
	if err := conn.Close(); err != nil {
		slog.Debug("closing docker connection", "context", context, "error", err)
	}
}

// GetDockerInfo returns system-wide Docker information (e.g. data root).
func (c *Client) GetDockerInfo(ctx context.Context) (*DockerInfo, error) {
	resp, err := c.Do(ctx, http.MethodGet, "/info", nil)
	if err != nil {
		return nil, fmt.Errorf("docker info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		slog.Warn("docker error", "method", "GetDockerInfo", "path", "/info", "status", resp.StatusCode, "body", string(body))
		return nil, fmt.Errorf("docker info: docker error (status %d)", resp.StatusCode)
	}

	var info DockerInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decoding docker info: %w", err)
	}
	return &info, nil
}

// GetAPIVersion returns the negotiated Docker API version string (e.g. "v1.45").
func (c *Client) GetAPIVersion() string {
	return c.apiVersion
}

// GetSocketPath returns the Unix socket path used by this client.
func (c *Client) GetSocketPath() string {
	return c.socketPath
}

// ContainerStatsResponse holds the subset of Docker stats we expose via Prometheus.
type ContainerStatsResponse struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
	} `json:"cpu_stats"`
	MemoryStats struct {
		Usage uint64 `json:"usage"`
		Limit uint64 `json:"limit"`
	} `json:"memory_stats"`
	Networks map[string]struct {
		RxBytes uint64 `json:"rx_bytes"`
		TxBytes uint64 `json:"tx_bytes"`
	} `json:"networks"`
}

// ContainerStats fetches a single-shot stats snapshot for the given container ID.
func (c *Client) ContainerStats(ctx context.Context, id string) (*ContainerStatsResponse, error) {
	if err := validateContainerRef(id); err != nil {
		return nil, fmt.Errorf("container stats: %w", err)
	}
	resp, err := c.Do(ctx, http.MethodGet, "/containers/"+id+"/stats?stream=false&one-shot=true", nil)
	if err != nil {
		return nil, fmt.Errorf("container stats: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		slog.Warn("docker error", "method", "ContainerStats", "path", "/containers/"+id+"/stats", "status", resp.StatusCode, "body", string(body))
		return nil, fmt.Errorf("container stats: docker error (status %d)", resp.StatusCode)
	}

	var stats ContainerStatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return nil, fmt.Errorf("decoding container stats: %w", err)
	}
	return &stats, nil
}
