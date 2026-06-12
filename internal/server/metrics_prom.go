package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/codeswhat/lookout/internal/protocol"
)

// handleMetrics emits host and per-container metrics in Prometheus text
// exposition format (version 0.0.4). It is registered at both
// GET /_lookout/metrics and GET /metrics.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var b strings.Builder

	// --- build info ---
	fmt.Fprintf(&b, "# HELP lookout_build_info Lookout agent build metadata.\n")
	fmt.Fprintf(&b, "# TYPE lookout_build_info gauge\n")
	fmt.Fprintf(&b, "lookout_build_info{version=\"%s\"} 1\n", escapeLabelValue(protocol.AgentVersion))

	// --- uptime ---
	uptime := time.Since(s.startTime).Seconds()
	fmt.Fprintf(&b, "# HELP lookout_uptime_seconds Seconds since the agent started.\n")
	fmt.Fprintf(&b, "# TYPE lookout_uptime_seconds gauge\n")
	fmt.Fprintf(&b, "lookout_uptime_seconds %g\n", uptime)

	// --- host metrics ---
	host, err := s.collector.Collect()
	if err == nil && host != nil {
		fmt.Fprintf(&b, "# HELP lookout_host_cpu_usage_percent Host CPU usage percentage.\n")
		fmt.Fprintf(&b, "# TYPE lookout_host_cpu_usage_percent gauge\n")
		fmt.Fprintf(&b, "lookout_host_cpu_usage_percent %g\n", host.CPUUsage)

		fmt.Fprintf(&b, "# HELP lookout_host_memory_total_bytes Host total memory in bytes.\n")
		fmt.Fprintf(&b, "# TYPE lookout_host_memory_total_bytes gauge\n")
		fmt.Fprintf(&b, "lookout_host_memory_total_bytes %d\n", host.MemoryTotal)

		fmt.Fprintf(&b, "# HELP lookout_host_memory_used_bytes Host used memory in bytes.\n")
		fmt.Fprintf(&b, "# TYPE lookout_host_memory_used_bytes gauge\n")
		fmt.Fprintf(&b, "lookout_host_memory_used_bytes %d\n", host.MemoryUsed)

		fmt.Fprintf(&b, "# HELP lookout_host_disk_total_bytes Host total disk space in bytes.\n")
		fmt.Fprintf(&b, "# TYPE lookout_host_disk_total_bytes gauge\n")
		fmt.Fprintf(&b, "lookout_host_disk_total_bytes %d\n", host.DiskTotal)

		fmt.Fprintf(&b, "# HELP lookout_host_disk_used_bytes Host used disk space in bytes.\n")
		fmt.Fprintf(&b, "# TYPE lookout_host_disk_used_bytes gauge\n")
		fmt.Fprintf(&b, "lookout_host_disk_used_bytes %d\n", host.DiskUsed)

		fmt.Fprintf(&b, "# HELP lookout_host_network_receive_bytes_total Host network bytes received (all non-lo interfaces).\n")
		fmt.Fprintf(&b, "# TYPE lookout_host_network_receive_bytes_total counter\n")
		fmt.Fprintf(&b, "lookout_host_network_receive_bytes_total %d\n", host.NetworkRxBytes)

		fmt.Fprintf(&b, "# HELP lookout_host_network_transmit_bytes_total Host network bytes transmitted (all non-lo interfaces).\n")
		fmt.Fprintf(&b, "# TYPE lookout_host_network_transmit_bytes_total counter\n")
		fmt.Fprintf(&b, "lookout_host_network_transmit_bytes_total %d\n", host.NetworkTxBytes)
	}

	// --- per-container metrics ---
	containers, err := s.dockerClient.ListContainers(ctx, false)
	if err == nil && len(containers) > 0 {
		type containerResult struct {
			id    string
			name  string
			image string
			cpu   float64
			memU  uint64
			memL  uint64
			rxB   uint64
			txB   uint64
		}

		const maxWorkers = 8
		results := make([]containerResult, len(containers))
		sem := make(chan struct{}, maxWorkers)
		var wg sync.WaitGroup

		for i, c := range containers {
			wg.Add(1)
			go func(idx int, id, image string, names []string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				name := id
				if len(names) > 0 {
					name = strings.TrimPrefix(names[0], "/")
				}

				// Per-container timeout derived from request context.
				cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				defer cancel()

				stats, err := s.dockerClient.ContainerStats(cctx, id)
				if err != nil {
					// Failing container is skipped.
					results[idx] = containerResult{id: ""}
					return
				}

				var rxB, txB uint64
				for _, iface := range stats.Networks {
					rxB += iface.RxBytes
					txB += iface.TxBytes
				}

				results[idx] = containerResult{
					id:    id,
					name:  name,
					image: image,
					cpu:   float64(stats.CPUStats.CPUUsage.TotalUsage) / 1e9,
					memU:  stats.MemoryStats.Usage,
					memL:  stats.MemoryStats.Limit,
					rxB:   rxB,
					txB:   txB,
				}
			}(i, c.ID, c.Image, c.Names)
		}
		wg.Wait()

		// Collect non-empty results.
		var valid []containerResult
		for _, r := range results {
			if r.id != "" {
				valid = append(valid, r)
			}
		}

		if len(valid) > 0 {
			fmt.Fprintf(&b, "# HELP container_cpu_usage_seconds_total Cumulative CPU time consumed by the container in seconds.\n")
			fmt.Fprintf(&b, "# TYPE container_cpu_usage_seconds_total counter\n")
			for _, r := range valid {
				fmt.Fprintf(&b, "container_cpu_usage_seconds_total{id=\"%s\",name=\"%s\",image=\"%s\"} %g\n",
					escapeLabelValue(r.id), escapeLabelValue(r.name), escapeLabelValue(r.image), r.cpu)
			}

			fmt.Fprintf(&b, "# HELP container_memory_usage_bytes Current memory usage of the container in bytes.\n")
			fmt.Fprintf(&b, "# TYPE container_memory_usage_bytes gauge\n")
			for _, r := range valid {
				fmt.Fprintf(&b, "container_memory_usage_bytes{id=\"%s\",name=\"%s\",image=\"%s\"} %d\n",
					escapeLabelValue(r.id), escapeLabelValue(r.name), escapeLabelValue(r.image), r.memU)
			}

			fmt.Fprintf(&b, "# HELP container_spec_memory_limit_bytes Memory limit configured for the container in bytes.\n")
			fmt.Fprintf(&b, "# TYPE container_spec_memory_limit_bytes gauge\n")
			for _, r := range valid {
				if r.memL == 0 {
					continue
				}
				fmt.Fprintf(&b, "container_spec_memory_limit_bytes{id=\"%s\",name=\"%s\",image=\"%s\"} %d\n",
					escapeLabelValue(r.id), escapeLabelValue(r.name), escapeLabelValue(r.image), r.memL)
			}

			fmt.Fprintf(&b, "# HELP container_network_receive_bytes_total Cumulative bytes received by the container across all network interfaces.\n")
			fmt.Fprintf(&b, "# TYPE container_network_receive_bytes_total counter\n")
			for _, r := range valid {
				fmt.Fprintf(&b, "container_network_receive_bytes_total{id=\"%s\",name=\"%s\",image=\"%s\"} %d\n",
					escapeLabelValue(r.id), escapeLabelValue(r.name), escapeLabelValue(r.image), r.rxB)
			}

			fmt.Fprintf(&b, "# HELP container_network_transmit_bytes_total Cumulative bytes transmitted by the container across all network interfaces.\n")
			fmt.Fprintf(&b, "# TYPE container_network_transmit_bytes_total counter\n")
			for _, r := range valid {
				fmt.Fprintf(&b, "container_network_transmit_bytes_total{id=\"%s\",name=\"%s\",image=\"%s\"} %d\n",
					escapeLabelValue(r.id), escapeLabelValue(r.name), escapeLabelValue(r.image), r.txB)
			}
		}
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, b.String())
}

// escapeLabelValue escapes a Prometheus label value per the exposition format
// spec: backslash -> \\, double-quote -> \", newline -> \n.
func escapeLabelValue(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}
