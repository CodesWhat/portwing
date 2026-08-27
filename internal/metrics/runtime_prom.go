package metrics

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/codeswhat/portwing/internal/docker"
)

// DockerMetricsClient is the Docker API subset required for Prometheus
// per-container metrics.
type DockerMetricsClient interface {
	ListContainers(ctx context.Context, all bool) ([]docker.ContainerJSON, error)
	ContainerStats(ctx context.Context, id string) (*docker.ContainerStatsResponse, error)
}

// WriteHostPrometheus appends host resource metrics when collection succeeds.
func WriteHostPrometheus(b *strings.Builder, collector *Collector) {
	if collector == nil {
		return
	}
	host, err := collector.Collect()
	if err != nil || host == nil {
		return
	}

	fmt.Fprintf(b, "# HELP portwing_host_cpu_usage_percent Host CPU usage percentage.\n")
	fmt.Fprintf(b, "# TYPE portwing_host_cpu_usage_percent gauge\n")
	fmt.Fprintf(b, "portwing_host_cpu_usage_percent %g\n", host.CPUUsage)
	fmt.Fprintf(b, "# HELP portwing_host_memory_total_bytes Host total memory in bytes.\n")
	fmt.Fprintf(b, "# TYPE portwing_host_memory_total_bytes gauge\n")
	fmt.Fprintf(b, "portwing_host_memory_total_bytes %d\n", host.MemoryTotal)
	fmt.Fprintf(b, "# HELP portwing_host_memory_used_bytes Host used memory in bytes.\n")
	fmt.Fprintf(b, "# TYPE portwing_host_memory_used_bytes gauge\n")
	fmt.Fprintf(b, "portwing_host_memory_used_bytes %d\n", host.MemoryUsed)
	fmt.Fprintf(b, "# HELP portwing_host_disk_total_bytes Host total disk space in bytes.\n")
	fmt.Fprintf(b, "# TYPE portwing_host_disk_total_bytes gauge\n")
	fmt.Fprintf(b, "portwing_host_disk_total_bytes %d\n", host.DiskTotal)
	fmt.Fprintf(b, "# HELP portwing_host_disk_used_bytes Host used disk space in bytes.\n")
	fmt.Fprintf(b, "# TYPE portwing_host_disk_used_bytes gauge\n")
	fmt.Fprintf(b, "portwing_host_disk_used_bytes %d\n", host.DiskUsed)
	fmt.Fprintf(b, "# HELP portwing_host_network_receive_bytes_total Host network bytes received (all non-lo interfaces).\n")
	fmt.Fprintf(b, "# TYPE portwing_host_network_receive_bytes_total counter\n")
	fmt.Fprintf(b, "portwing_host_network_receive_bytes_total %d\n", host.NetworkRxBytes)
	fmt.Fprintf(b, "# HELP portwing_host_network_transmit_bytes_total Host network bytes transmitted (all non-lo interfaces).\n")
	fmt.Fprintf(b, "# TYPE portwing_host_network_transmit_bytes_total counter\n")
	fmt.Fprintf(b, "portwing_host_network_transmit_bytes_total %d\n", host.NetworkTxBytes)
}

// WriteContainerPrometheus appends per-container Docker metrics. Collection is
// bounded to eight concurrent stats requests, each with a ten-second timeout.
func WriteContainerPrometheus(
	ctx context.Context,
	b *strings.Builder,
	client DockerMetricsClient,
	escapeLabel func(string) string,
) {
	if client == nil {
		return
	}
	containers, err := client.ListContainers(ctx, false)
	if err != nil || len(containers) == 0 {
		return
	}

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
	workerCount := min(maxWorkers, len(containers))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range workerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				container := containers[idx]
				name := container.ID
				if len(container.Names) > 0 {
					name = strings.TrimPrefix(container.Names[0], "/")
				}
				statsCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				stats, err := client.ContainerStats(statsCtx, container.ID)
				cancel()
				if err != nil {
					continue
				}

				var rxBytes, txBytes uint64
				for _, network := range stats.Networks {
					rxBytes += network.RxBytes
					txBytes += network.TxBytes
				}
				results[idx] = containerResult{
					id:    container.ID,
					name:  name,
					image: container.Image,
					cpu:   float64(stats.CPUStats.CPUUsage.TotalUsage) / 1e9,
					memU:  stats.MemoryStats.Usage,
					memL:  stats.MemoryStats.Limit,
					rxB:   rxBytes,
					txB:   txBytes,
				}
			}
		}()
	}
	for i := range containers {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	valid := make([]containerResult, 0, len(results))
	for _, result := range results {
		if result.id != "" {
			valid = append(valid, result)
		}
	}
	if len(valid) == 0 {
		return
	}

	fmt.Fprintf(b, "# HELP container_cpu_usage_seconds_total Cumulative CPU time consumed by the container in seconds.\n")
	fmt.Fprintf(b, "# TYPE container_cpu_usage_seconds_total counter\n")
	for _, result := range valid {
		fmt.Fprintf(b, "container_cpu_usage_seconds_total{id=\"%s\",name=\"%s\",image=\"%s\"} %g\n",
			escapeLabel(result.id), escapeLabel(result.name), escapeLabel(result.image), result.cpu)
	}
	fmt.Fprintf(b, "# HELP container_memory_usage_bytes Current memory usage of the container in bytes.\n")
	fmt.Fprintf(b, "# TYPE container_memory_usage_bytes gauge\n")
	for _, result := range valid {
		fmt.Fprintf(b, "container_memory_usage_bytes{id=\"%s\",name=\"%s\",image=\"%s\"} %d\n",
			escapeLabel(result.id), escapeLabel(result.name), escapeLabel(result.image), result.memU)
	}
	fmt.Fprintf(b, "# HELP container_spec_memory_limit_bytes Memory limit configured for the container in bytes.\n")
	fmt.Fprintf(b, "# TYPE container_spec_memory_limit_bytes gauge\n")
	for _, result := range valid {
		if result.memL == 0 {
			continue
		}
		fmt.Fprintf(b, "container_spec_memory_limit_bytes{id=\"%s\",name=\"%s\",image=\"%s\"} %d\n",
			escapeLabel(result.id), escapeLabel(result.name), escapeLabel(result.image), result.memL)
	}
	fmt.Fprintf(b, "# HELP container_network_receive_bytes_total Cumulative bytes received by the container across all network interfaces.\n")
	fmt.Fprintf(b, "# TYPE container_network_receive_bytes_total counter\n")
	for _, result := range valid {
		fmt.Fprintf(b, "container_network_receive_bytes_total{id=\"%s\",name=\"%s\",image=\"%s\"} %d\n",
			escapeLabel(result.id), escapeLabel(result.name), escapeLabel(result.image), result.rxB)
	}
	fmt.Fprintf(b, "# HELP container_network_transmit_bytes_total Cumulative bytes transmitted by the container across all network interfaces.\n")
	fmt.Fprintf(b, "# TYPE container_network_transmit_bytes_total counter\n")
	for _, result := range valid {
		fmt.Fprintf(b, "container_network_transmit_bytes_total{id=\"%s\",name=\"%s\",image=\"%s\"} %d\n",
			escapeLabel(result.id), escapeLabel(result.name), escapeLabel(result.image), result.txB)
	}
}
