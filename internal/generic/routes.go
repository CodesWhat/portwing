package generic

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/codeswhat/portwing/internal/docker"
)

func (a *Adapter) handleContainers(w http.ResponseWriter, _ *http.Request) {
	containers := a.containers.GetContainers()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(containers); err != nil {
		slog.Error("failed to encode containers response", "error", err)
	}
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

	body, err := a.dockerClient.GetContainerLogs(r.Context(), containerID, tail, since, until, follow, false)
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
