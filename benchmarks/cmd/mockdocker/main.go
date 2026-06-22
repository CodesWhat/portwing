// mockdocker is a minimal Docker-API-shaped HTTP server that listens on a unix
// socket. It exists so the Portwing soak benchmark has a stable Docker upstream
// whose behavior doesn't drift between runs and needs no real daemon.
//
// Portwing's docker client negotiates an API version and prefixes most paths
// with `/v1.NN`, but hits bare `/version` and `/_ping` during negotiation and
// health checks, so the handler strips an optional leading version segment
// before routing. It implements just the endpoints the agent touches:
//
//	GET /version              → daemon version (drives version negotiation)
//	GET /_ping                → 200 OK
//	GET /info                 → DockerRootDir
//	GET /containers/json      → JSON array of fake containers
//	GET /containers/{id}/json → container inspect
//	GET /containers/{id}/logs → an 8-byte-framed log chunk (multiplexed)
//	GET /containers/{id}/stats→ one-shot stats snapshot
//	GET /events               → long-lived stream emitting a container event
//	                            every 2s until the client disconnects
//
// Anything else returns 404. Logs are silenced unless -log is passed.
package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"
)

type mockContainer struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Image  string            `json:"Image"`
	State  string            `json:"State"`
	Status string            `json:"Status"`
	Labels map[string]string `json:"Labels"`
}

var fakeContainers = []mockContainer{
	{ID: "c0000000001", Names: []string{"/traefik"}, Image: "traefik:v3", State: "running", Status: "Up 3 days", Labels: map[string]string{"com.docker.compose.project": "infra"}},
	{ID: "c0000000002", Names: []string{"/grafana"}, Image: "grafana/grafana:10", State: "running", Status: "Up 3 days", Labels: map[string]string{"com.docker.compose.project": "infra"}},
	{ID: "c0000000003", Names: []string{"/prometheus"}, Image: "prom/prometheus:v2", State: "running", Status: "Up 2 days", Labels: map[string]string{"com.docker.compose.project": "infra"}},
	{ID: "c0000000004", Names: []string{"/postgres"}, Image: "postgres:17", State: "running", Status: "Up 5 hours", Labels: map[string]string{"com.docker.compose.project": "db"}},
	{ID: "c0000000005", Names: []string{"/redis"}, Image: "redis:8", State: "running", Status: "Up 5 hours", Labels: map[string]string{"com.docker.compose.project": "db"}},
}

// versionPrefix matches a leading Docker API version segment like "/v1.44".
var versionPrefix = regexp.MustCompile(`^/v[0-9]+\.[0-9]+`)

var verbose bool

func main() {
	socket := flag.String("socket", "/tmp/portwing-soak-mock.sock", "unix socket path")
	flag.BoolVar(&verbose, "log", false, "log every request")
	flag.Parse()

	_ = os.Remove(*socket)
	ln, err := net.Listen("unix", *socket)
	if err != nil {
		log.Fatalf("listen %s: %v", *socket, err)
	}
	// Owner-only: the soak runs portwing as the same user, so it can connect
	// without the world-writable bit gosec (G302) rightly objects to.
	if err := os.Chmod(*socket, 0o600); err != nil {
		log.Fatalf("chmod %s: %v", *socket, err)
	}

	containersPayload, err := json.Marshal(fakeContainers)
	if err != nil {
		log.Fatalf("marshal containers: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := versionPrefix.ReplaceAllString(r.URL.Path, "")
		if verbose {
			// #nosec G706 -- benchmark-only mock; %q quotes the request fields so
			// control chars can't forge log lines, and -log is opt-in for debugging.
			log.Printf("method=%q path=%q (raw=%q)", r.Method, path, r.URL.Path)
		}

		switch {
		case path == "/_ping":
			w.Header().Set("Api-Version", "1.44")
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, "OK")
		case path == "/version":
			writeJSON(w, map[string]string{"Version": "24.0.0-mock", "ApiVersion": "1.44"})
		case path == "/info":
			writeJSON(w, map[string]string{"DockerRootDir": "/var/lib/docker"})
		case path == "/containers/json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(containersPayload)
		case path == "/events":
			streamEvents(w, r)
		case strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/json"):
			writeInspect(w, containerID(path, "/json"))
		case strings.HasPrefix(path, "/containers/") && strings.Contains(path, "/logs"):
			writeLogs(w)
		case strings.HasPrefix(path, "/containers/") && strings.Contains(path, "/stats"):
			writeStats(w)
		default:
			http.NotFound(w, r)
		}
	})

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Fatalf("serve: %v", err)
		}
	}()

	log.Printf("mockdocker listening on %s", *socket)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	_ = srv.Close()
	<-done
	_ = os.Remove(*socket)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// containerID extracts the id from "/containers/<id><suffix>".
func containerID(path, suffix string) string {
	id := strings.TrimPrefix(path, "/containers/")
	id = strings.TrimSuffix(id, suffix)
	return id
}

func writeInspect(w http.ResponseWriter, id string) {
	writeJSON(w, map[string]any{
		"Id":      id,
		"Name":    "/" + id,
		"Image":   "nginx:latest",
		"Created": "2026-01-01T00:00:00Z",
		"State":   map[string]any{"Status": "running", "Running": true, "Pid": 4242},
		"Config":  map[string]any{"Image": "nginx:latest", "Env": []string{"A=1", "B=2"}, "Labels": map[string]string{"app": "web"}},
		"Mounts":  []any{},
	})
}

// writeLogs writes a single Docker-multiplexed stdout frame: an 8-byte header
// (stream byte + 3 pad + big-endian payload length) followed by the payload.
func writeLogs(w http.ResponseWriter) {
	const logPayload = "soak log line\n"
	const logPayloadLen = uint32(len(logPayload))
	payload := []byte(logPayload)
	header := make([]byte, 8)
	header[0] = 1 // stdout
	binary.BigEndian.PutUint32(header[4:8], logPayloadLen)
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(header)
	_, _ = w.Write(payload)
}

func writeStats(w http.ResponseWriter) {
	writeJSON(w, map[string]any{
		"cpu_stats":    map[string]any{"cpu_usage": map[string]any{"total_usage": 123456789}},
		"memory_stats": map[string]any{"usage": 33554432, "limit": 2147483648},
		"networks":     map[string]any{"eth0": map[string]any{"rx_bytes": 1024, "tx_bytes": 2048}},
	})
}

// streamEvents holds the connection open and emits a container event every 2s
// until the client disconnects, mirroring a quiet-but-alive Docker daemon.
func streamEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	enc := json.NewEncoder(w)
	for i := 0; ; i++ {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			c := fakeContainers[i%len(fakeContainers)]
			evt := map[string]any{
				"Type":   "container",
				"Action": "start",
				"Actor": map[string]any{
					"ID":         c.ID,
					"Attributes": map[string]string{"name": strings.TrimPrefix(c.Names[0], "/"), "image": c.Image},
				},
				"time": time.Now().Unix(),
			}
			if err := enc.Encode(evt); err != nil {
				return
			}
			_, _ = fmt.Fprint(w, "\n")
			flusher.Flush()
		}
	}
}
