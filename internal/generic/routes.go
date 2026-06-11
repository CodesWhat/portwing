package generic

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
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

	// Docker log multiplexing: strip the 8-byte frame header before writing.
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
