package drydock

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/codeswhat/portwing/internal/adapter"
	"github.com/codeswhat/portwing/internal/docker"
)

// streamLimitRejectionMessage is the body returned when a long-lived adapter
// stream is rejected for want of a free concurrency slot. Matches the
// Docker-proxy stream rejection in internal/server/http.go so a client (or a
// test) can't tell the two rejection paths apart.
const streamLimitRejectionMessage = "agent busy: too many concurrent streams"

// RegisterRoutes registers Drydock-specific HTTP routes. admitStream gates the
// SSE route and follow-mode container log streaming against the server's
// shared stream concurrency limit (SPEC 7.3); see adapter.StreamAdmitter.
func (a *Adapter) RegisterRoutes(mux *http.ServeMux, auth func(http.HandlerFunc) http.Handler, admitStream adapter.StreamAdmitter) {
	a.admit = admitStream
	mux.Handle("GET /api/events", auth(a.serveEvents))
	mux.Handle("GET /api/containers", auth(a.handleContainers))
	mux.Handle("GET /api/containers/{id}/logs", auth(a.handleContainerLogs))
	mux.Handle("DELETE /api/containers/{id}", auth(a.handleContainerDelete))
	mux.Handle("GET /api/watchers", auth(a.handleWatchers))
	mux.Handle("GET /api/watchers/{type}/{name}", auth(a.handleWatcherGet))
	mux.Handle("GET /api/triggers", auth(a.handleTriggers))
	mux.Handle("GET /api/log/entries", auth(a.handleLogEntries))
	mux.Handle("POST /api/watchers/{type}/{name}", auth(a.handleWatcherPoll))
	mux.Handle("POST /api/watchers/{type}/{name}/container/{id}", auth(a.handleWatcherContainer))
	mux.Handle("POST /api/triggers/{type}/{name}", auth(a.handleTriggerExec))
	mux.Handle("POST /api/triggers/{type}/{name}/batch", auth(a.handleTriggerBatch))
}

// serveEvents wraps the SSE broadcaster with the shared stream-concurrency
// gate. The admission check happens before the broadcaster registers the
// client, so a rejected connection never appears in b.clients.
func (a *Adapter) serveEvents(w http.ResponseWriter, r *http.Request) {
	release, ok := a.admit.Admit()
	if !ok {
		slog.Warn("concurrent stream limit reached, rejecting SSE client")
		http.Error(w, streamLimitRejectionMessage, http.StatusServiceUnavailable)
		return
	}
	defer release()
	a.sse.ServeHTTP(w, r)
}

func (a *Adapter) handleContainers(w http.ResponseWriter, r *http.Request) {
	containers := a.containers.GetContainers()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(toDrydockContainers(containers))
}

func (a *Adapter) handleContainerLogs(w http.ResponseWriter, r *http.Request) {
	containerID := r.PathValue("id")
	tail := r.URL.Query().Get("tail")
	since := r.URL.Query().Get("since")
	until := r.URL.Query().Get("until")
	follow := r.URL.Query().Get("follow") == "1" || r.URL.Query().Get("follow") == "true"
	timestamps := r.URL.Query().Get("timestamps") == "1" || r.URL.Query().Get("timestamps") == "true"

	if tail != "" {
		n, err := strconv.Atoi(tail)
		if err != nil || n <= 0 {
			http.Error(w, "invalid tail: must be a positive integer", http.StatusBadRequest)
			return
		}
		tail = strconv.Itoa(n)
	}

	// Bound concurrent follow-mode log streams against the shared stream
	// limit (SPEC 7.3), before the daemon call so a rejected follow request
	// costs nothing. Non-follow requests are a single bounded read and are
	// never gated.
	var release func()
	if follow {
		var ok bool
		release, ok = a.admit.Admit()
		if !ok {
			slog.Warn("concurrent stream limit reached, rejecting log follow", "containerId", containerID)
			http.Error(w, streamLimitRejectionMessage, http.StatusServiceUnavailable)
			return
		}
		defer release()
	}

	body, err := a.dockerClient.GetContainerLogs(r.Context(), containerID, tail, since, until, follow, timestamps)
	if err != nil {
		http.Error(w, fmt.Sprintf("getting logs: %v", err), docker.StatusCodeForError(err))
		return
	}
	defer body.Close()

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if follow {
		w.Header().Set("Transfer-Encoding", "chunked")
	}

	flusher, canFlush := w.(http.Flusher)
	err = docker.DecodeContainerLogStream(body, func(_ docker.ContainerLogStream, payload []byte) error {
		if _, writeErr := w.Write(payload); writeErr != nil {
			return writeErr
		}
		if canFlush {
			flusher.Flush()
		}
		return nil
	})
	if err != nil {
		slog.Debug("log stream ended", "error", err)
	}
}

func (a *Adapter) handleContainerDelete(w http.ResponseWriter, r *http.Request) {
	containerID := r.PathValue("id")

	if err := a.dockerClient.RemoveContainer(r.Context(), containerID, true); err != nil {
		http.Error(w, fmt.Sprintf("removing container: %v", err), docker.StatusCodeForError(err))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (a *Adapter) handleWatchers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(GetWatcherComponents())
}

// handleWatcherGet returns a single watcher descriptor by type and name.
// Called by Drydock's AgentClient.getWatcher() (AgentClient.ts:1552).
func (a *Adapter) handleWatcherGet(w http.ResponseWriter, r *http.Request) {
	watcherType := r.PathValue("type")
	watcherName := r.PathValue("name")

	for _, watcher := range GetWatcherComponents() {
		if strings.EqualFold(watcher.Type, watcherType) && strings.EqualFold(watcher.Name, watcherName) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(watcher)
			return
		}
	}

	http.Error(w, fmt.Sprintf("watcher %s/%s not found", watcherType, watcherName), http.StatusNotFound)
}

func (a *Adapter) handleTriggers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(GetTriggerComponents())
}

// handleLogEntries returns an empty log entry array.
// Drydock calls GET /api/log/entries (AgentClient.ts:1503) to populate the
// agent log viewer. Portwing has no in-memory log buffer; returning [] is safe
// and prevents 404 errors in Drydock's log panel.
func (a *Adapter) handleLogEntries(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode([]struct{}{})
}

func (a *Adapter) handleWatcherPoll(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":   "not implemented in v1.0",
		"message": "registry checking is performed by the Drydock controller",
	})
}

func (a *Adapter) handleWatcherContainer(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":   "not implemented in v1.0",
		"message": "registry checking is performed by the Drydock controller",
	})
}

func (a *Adapter) handleTriggerExec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":   "not implemented in v1.0",
		"message": "registry checking is performed by the Drydock controller",
	})
}

func (a *Adapter) handleTriggerBatch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":   "not implemented in v1.0",
		"message": "registry checking is performed by the Drydock controller",
	})
}
