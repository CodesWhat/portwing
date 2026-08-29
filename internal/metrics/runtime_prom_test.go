package metrics_test

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/docker"
	"github.com/codeswhat/portwing/internal/metrics"
)

type dockerMetricsFake struct {
	containers []docker.ContainerJSON
	listErr    error
	stats      map[string]*docker.ContainerStatsResponse
	statsErr   map[string]error
}

type blockingDockerMetricsFake struct {
	containers []docker.ContainerJSON
	entered    chan struct{}
	release    <-chan struct{}
	active     atomic.Int64
	maximum    atomic.Int64
}

func (f *blockingDockerMetricsFake) ListContainers(context.Context, bool) ([]docker.ContainerJSON, error) {
	return f.containers, nil
}

func (f *blockingDockerMetricsFake) ContainerStats(ctx context.Context, _ string) (*docker.ContainerStatsResponse, error) {
	active := f.active.Add(1)
	defer f.active.Add(-1)
	for {
		maximum := f.maximum.Load()
		if active <= maximum || f.maximum.CompareAndSwap(maximum, active) {
			break
		}
	}
	f.entered <- struct{}{}
	select {
	case <-f.release:
		return &docker.ContainerStatsResponse{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (f *dockerMetricsFake) ListContainers(context.Context, bool) ([]docker.ContainerJSON, error) {
	return f.containers, f.listErr
}

func (f *dockerMetricsFake) ContainerStats(
	_ context.Context,
	id string,
) (*docker.ContainerStatsResponse, error) {
	if err := f.statsErr[id]; err != nil {
		return nil, err
	}
	return f.stats[id], nil
}

func mustContainerStats(t *testing.T, raw string) *docker.ContainerStatsResponse {
	t.Helper()
	var stats docker.ContainerStatsResponse
	if err := json.Unmarshal([]byte(raw), &stats); err != nil {
		t.Fatalf("decode stats fixture: %v", err)
	}
	return &stats
}

func TestWriteHostPrometheus(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	metrics.WriteHostPrometheus(&b, nil)
	if b.Len() != 0 {
		t.Fatalf("nil collector wrote metrics: %q", b.String())
	}

	metrics.WriteHostPrometheus(&b, metrics.NewCollector(t.TempDir(), false))
	body := b.String()
	if !strings.Contains(body, "portwing_host_metrics_supported ") {
		t.Fatalf("missing support gauge in host metrics:\n%s", body)
	}
	if strings.Contains(body, "portwing_host_metrics_supported 0") {
		// No procfs on this platform. The resource series are deliberately
		// absent rather than zero-valued, so there is nothing further to
		// assert; the support gauge above is the whole contract here.
		return
	}
	for _, want := range []string{
		"portwing_host_cpu_usage_percent",
		"portwing_host_memory_total_bytes",
		"portwing_host_memory_used_bytes",
		"portwing_host_disk_metrics_available ",
		"portwing_host_network_receive_bytes_total",
		"portwing_host_network_transmit_bytes_total",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in host metrics:\n%s", want, body)
		}
	}
	// The disk byte series and the gauge must never disagree: exporting one
	// without the other is the zero-vs-unmeasured ambiguity this gauge exists
	// to remove.
	diskUp := strings.Contains(body, "portwing_host_disk_metrics_available 1")
	if diskUp != strings.Contains(body, "portwing_host_disk_total_bytes") {
		t.Errorf("disk availability gauge and disk series disagree:\n%s", body)
	}
}

func TestWriteContainerPrometheus(t *testing.T) {
	t.Parallel()

	stats := mustContainerStats(t, `{
		"cpu_stats":{"cpu_usage":{"total_usage":2500000000}},
		"memory_stats":{"usage":1024,"limit":4096},
		"networks":{
			"eth0":{"rx_bytes":10,"tx_bytes":20},
			"eth1":{"rx_bytes":30,"tx_bytes":40}
		}
	}`)
	noLimitStats := mustContainerStats(t, `{
		"cpu_stats":{"cpu_usage":{"total_usage":1000000000}},
		"memory_stats":{"usage":512,"limit":0},
		"networks":{}
	}`)
	client := &dockerMetricsFake{
		containers: []docker.ContainerJSON{
			{ID: `one"id`, Names: []string{"/api\nworker"}, Image: `repo\image:v1`},
			{ID: "two", Image: "repo/worker:v2"},
			{ID: "failed", Names: []string{"/ignored"}, Image: "repo/fail:v1"},
		},
		stats: map[string]*docker.ContainerStatsResponse{
			`one"id`: stats,
			"two":    noLimitStats,
		},
		statsErr: map[string]error{"failed": errors.New("stats unavailable")},
	}

	var b strings.Builder
	metrics.WriteContainerPrometheus(context.Background(), &b, client, metrics.EscapeLabelValue)
	body := b.String()
	for _, want := range []string{
		`container_cpu_usage_seconds_total{id="one\"id",name="api\nworker",image="repo\\image:v1"} 2.5`,
		`container_cpu_usage_seconds_total{id="two",name="two",image="repo/worker:v2"} 1`,
		`container_memory_usage_bytes{id="one\"id",name="api\nworker",image="repo\\image:v1"} 1024`,
		`container_spec_memory_limit_bytes{id="one\"id",name="api\nworker",image="repo\\image:v1"} 4096`,
		`container_network_receive_bytes_total{id="one\"id",name="api\nworker",image="repo\\image:v1"} 40`,
		`container_network_transmit_bytes_total{id="one\"id",name="api\nworker",image="repo\\image:v1"} 60`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in container metrics:\n%s", want, body)
		}
	}
	if strings.Contains(body, `id="failed"`) {
		t.Fatalf("failed stats collection produced metrics:\n%s", body)
	}
	if strings.Contains(body, `container_spec_memory_limit_bytes{id="two"`) {
		t.Fatalf("zero memory limit should be omitted:\n%s", body)
	}
}

func TestWriteContainerPrometheusNoData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		client metrics.DockerMetricsClient
	}{
		{name: "nil client"},
		{
			name: "list error",
			client: &dockerMetricsFake{
				listErr: errors.New("list unavailable"),
			},
		},
		{
			name:   "empty list",
			client: &dockerMetricsFake{},
		},
		{
			name: "all stats fail",
			client: &dockerMetricsFake{
				containers: []docker.ContainerJSON{{ID: "failed"}},
				statsErr:   map[string]error{"failed": errors.New("stats unavailable")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var b strings.Builder
			metrics.WriteContainerPrometheus(context.Background(), &b, tt.client, noEscape)
			if b.Len() != 0 {
				t.Fatalf("unexpected metrics without usable data:\n%s", b.String())
			}
		})
	}
}

func TestWriteContainerPrometheusUsesFixedWorkerPool(t *testing.T) {
	const containerCount = 512
	containers := make([]docker.ContainerJSON, containerCount)
	for i := range containers {
		containers[i].ID = "container-" + strconv.Itoa(i)
	}
	release := make(chan struct{})
	client := &blockingDockerMetricsFake{
		containers: containers,
		entered:    make(chan struct{}, containerCount),
		release:    release,
	}

	before := runtime.NumGoroutine()
	done := make(chan struct{})
	go func() {
		defer close(done)
		var b strings.Builder
		metrics.WriteContainerPrometheus(context.Background(), &b, client, noEscape)
	}()

	for i := 0; i < 8; i++ {
		select {
		case <-client.entered:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for stats workers")
		}
	}
	after := runtime.NumGoroutine()
	if delta := after - before; delta > 20 {
		close(release)
		<-done
		t.Fatalf("container scrape added %d goroutines, want at most 20", delta)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("container scrape did not finish")
	}
	if got := client.maximum.Load(); got > 8 {
		t.Fatalf("maximum concurrent stats calls = %d, want at most 8", got)
	}
}
