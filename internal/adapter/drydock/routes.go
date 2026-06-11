package drydock

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
)

// RegisterRoutes registers Drydock-specific HTTP routes.
func (a *Adapter) RegisterRoutes(mux *http.ServeMux, auth func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/events", auth(a.sse.ServeHTTP))
	mux.Handle("GET /api/containers", auth(a.handleContainers))
	mux.Handle("GET /api/containers/{id}/logs", auth(a.handleContainerLogs))
	mux.Handle("DELETE /api/containers/{id}", auth(a.handleContainerDelete))
	mux.Handle("GET /api/watchers", auth(a.handleWatchers))
	mux.Handle("GET /api/triggers", auth(a.handleTriggers))
	mux.Handle("POST /api/watchers/{type}/{name}", auth(a.handleWatcherPoll))
	mux.Handle("POST /api/watchers/{type}/{name}/container/{id}", auth(a.handleWatcherContainer))
	mux.Handle("POST /api/triggers/{type}/{name}", auth(a.handleTriggerExec))
	mux.Handle("POST /api/triggers/{type}/{name}/batch", auth(a.handleTriggerBatch))
}

func (a *Adapter) handleContainers(w http.ResponseWriter, r *http.Request) {
	containers := a.containers.GetContainers()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(containers)
}

func (a *Adapter) handleContainerLogs(w http.ResponseWriter, r *http.Request) {
	containerID := r.PathValue("id")
	tail := r.URL.Query().Get("tail")
	since := r.URL.Query().Get("since")
	until := r.URL.Query().Get("until")
	follow := r.URL.Query().Get("follow") == "1" || r.URL.Query().Get("follow") == "true"

	if tail != "" {
		n, err := strconv.Atoi(tail)
		if err != nil || n <= 0 {
			http.Error(w, "invalid tail: must be a positive integer", http.StatusBadRequest)
			return
		}
		tail = strconv.Itoa(n)
	}

	body, err := a.dockerClient.GetContainerLogs(r.Context(), containerID, tail, since, until, follow)
	if err != nil {
		http.Error(w, fmt.Sprintf("getting logs: %v", err), http.StatusInternalServerError)
		return
	}
	defer body.Close()

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if follow {
		w.Header().Set("Transfer-Encoding", "chunked")
	}

	// Docker log multiplexing: each frame has an 8-byte header
	// [stream_type(1), 0(3), size(4 big-endian)].
	// Strip the header and write only the payload.
	header := make([]byte, 8)
	flusher, canFlush := w.(http.Flusher)

	for {
		_, err := io.ReadFull(body, header)
		if err != nil {
			if err != io.EOF && err != io.ErrUnexpectedEOF {
				slog.Debug("log stream ended", "error", err)
			}
			return
		}

		frameSize := binary.BigEndian.Uint32(header[4:8])
		if frameSize == 0 {
			continue
		}

		_, err = io.CopyN(w, body, int64(frameSize))
		if err != nil {
			return
		}

		if canFlush {
			flusher.Flush()
		}
	}
}

func (a *Adapter) handleContainerDelete(w http.ResponseWriter, r *http.Request) {
	containerID := r.PathValue("id")

	if err := a.dockerClient.RemoveContainer(r.Context(), containerID, true); err != nil {
		http.Error(w, fmt.Sprintf("removing container: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (a *Adapter) handleWatchers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(GetWatcherComponents())
}

func (a *Adapter) handleTriggers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(GetTriggerComponents())
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
