// loadgen hammers a Portwing HTTP endpoint at a configurable concurrency for a
// fixed duration and prints a single-line JSON summary (p50/p90/p99/max
// latency, RPS, status/error counts). It keeps one http.Client per worker so we
// measure steady-state behavior, not per-request transport setup.
//
// Two modes:
//
//	-mode req  (default) — fire request, drain body, close, repeat.
//	-mode sse            — open the endpoint, hold it open for -sse-hold, then
//	                       cancel and close. This churns Portwing's SSE
//	                       subscriber registration/teardown path (one
//	                       broadcaster + event-stream goroutine per connection),
//	                       which is the most leak-prone path in a long-lived
//	                       agent.
//
// Output is one line of JSON on stdout so the soak orchestrator can parse it
// without scraping columnar text.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type result struct {
	Scenario         string           `json:"scenario"`
	Base             string           `json:"base"`
	Method           string           `json:"method"`
	Path             string           `json:"path"`
	Mode             string           `json:"mode"`
	Concurrency      int              `json:"concurrency"`
	DurationSeconds  float64          `json:"duration_seconds"`
	TotalRequests    int64            `json:"total_requests"`
	ErrorRequests    int64            `json:"error_requests"`
	ErrorCounts      map[string]int64 `json:"error_counts,omitempty"`
	StatusCodeCounts map[int]int64    `json:"status_code_counts"`
	RPS              float64          `json:"rps"`
	LatencyP50Micros int64            `json:"latency_p50_us"`
	LatencyP90Micros int64            `json:"latency_p90_us"`
	LatencyP99Micros int64            `json:"latency_p99_us"`
	LatencyMaxMicros int64            `json:"latency_max_us"`
}

type options struct {
	Base        string
	Method      string
	Path        string
	Auth        string
	Concurrency int
	Duration    time.Duration
	Scenario    string
	Mode        string
	SSEHold     time.Duration
}

func main() {
	var (
		base        = flag.String("base", "http://127.0.0.1:3000", "base URL of the Portwing server")
		method      = flag.String("method", "GET", "HTTP method")
		path        = flag.String("path", "/_portwing/health", "request path")
		auth        = flag.String("auth", "", "bearer token (sent as Authorization: Bearer …)")
		concurrency = flag.Int("concurrency", 20, "concurrent workers")
		duration    = flag.Duration("duration", 20*time.Second, "run duration")
		scenario    = flag.String("scenario", "custom", "label for this run")
		mode        = flag.String("mode", "req", "req | sse")
		sseHold     = flag.Duration("sse-hold", time.Second, "how long each sse connection is held before close")
	)
	flag.Parse()

	out := run(options{
		Base:        *base,
		Method:      *method,
		Path:        *path,
		Auth:        *auth,
		Concurrency: *concurrency,
		Duration:    *duration,
		Scenario:    *scenario,
		Mode:        *mode,
		SSEHold:     *sseHold,
	})

	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		log.Fatalf("encode: %v", err)
	}
	fmt.Fprintf(os.Stderr, "%-18s mode=%-3s conc=%-3d rps=%.0f p50=%dus p99=%dus max=%dus errs=%d\n",
		out.Scenario, out.Mode, out.Concurrency, out.RPS, out.LatencyP50Micros, out.LatencyP99Micros, out.LatencyMaxMicros, out.ErrorRequests)
}

func run(opts options) result {
	stop := make(chan struct{})
	timer := time.NewTimer(opts.Duration)
	defer timer.Stop()
	go func() { <-timer.C; close(stop) }()

	transport := &http.Transport{
		MaxIdleConns:        opts.Concurrency * 2,
		MaxIdleConnsPerHost: opts.Concurrency * 2,
		IdleConnTimeout:     90 * time.Second,
	}
	defer transport.CloseIdleConnections()

	var totalReqs, totalErrs atomic.Int64
	var errorMu, statusMu, latMu sync.Mutex
	errorCounts := make(map[string]int64)
	statusCounts := make(map[int]int64)
	latencies := make([]int64, 0, 1<<16)

	recordErr := func(err error) {
		totalReqs.Add(1)
		totalErrs.Add(1)
		errorMu.Lock()
		errorCounts[err.Error()]++
		errorMu.Unlock()
	}
	recordOK := func(status int, micros int64) {
		totalReqs.Add(1)
		statusMu.Lock()
		statusCounts[status]++
		statusMu.Unlock()
		latMu.Lock()
		latencies = append(latencies, micros)
		latMu.Unlock()
	}

	var wg sync.WaitGroup
	wg.Add(opts.Concurrency)
	started := time.Now()

	for i := 0; i < opts.Concurrency; i++ {
		go func() {
			defer wg.Done()
			client := &http.Client{Transport: transport}
			if opts.Mode != "sse" {
				client.Timeout = 10 * time.Second
			}
			for {
				select {
				case <-stop:
					return
				default:
				}
				if opts.Mode == "sse" {
					doSSE(client, opts, recordErr, recordOK)
				} else {
					doReq(client, opts, recordErr, recordOK)
				}
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(started)

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	pct := func(q float64) int64 {
		if len(latencies) == 0 {
			return 0
		}
		idx := int(q * float64(len(latencies)))
		if idx >= len(latencies) {
			idx = len(latencies) - 1
		}
		return latencies[idx]
	}
	var maxLat int64
	if len(latencies) > 0 {
		maxLat = latencies[len(latencies)-1]
	}

	return result{
		Scenario:         opts.Scenario,
		Base:             opts.Base,
		Method:           opts.Method,
		Path:             opts.Path,
		Mode:             opts.Mode,
		Concurrency:      opts.Concurrency,
		DurationSeconds:  elapsed.Seconds(),
		TotalRequests:    totalReqs.Load(),
		ErrorRequests:    totalErrs.Load(),
		ErrorCounts:      errorCounts,
		StatusCodeCounts: statusCounts,
		RPS:              float64(totalReqs.Load()) / elapsed.Seconds(),
		LatencyP50Micros: pct(0.50),
		LatencyP90Micros: pct(0.90),
		LatencyP99Micros: pct(0.99),
		LatencyMaxMicros: maxLat,
	}
}

func newRequest(ctx context.Context, opts options) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, opts.Method, opts.Base+opts.Path, nil)
	if err != nil {
		return nil, err
	}
	if opts.Auth != "" {
		req.Header.Set("Authorization", "Bearer "+opts.Auth)
	}
	return req, nil
}

func doReq(client *http.Client, opts options, recordErr func(error), recordOK func(int, int64)) {
	req, err := newRequest(context.Background(), opts)
	if err != nil {
		recordErr(err)
		return
	}
	t0 := time.Now()
	resp, err := client.Do(req)
	micros := time.Since(t0).Microseconds()
	if err != nil {
		recordErr(err)
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	recordOK(resp.StatusCode, micros)
}

// doSSE opens the endpoint, drains it until -sse-hold elapses, then cancels and
// closes — exercising connect → subscribe → teardown on every iteration.
func doSSE(client *http.Client, opts options, recordErr func(error), recordOK func(int, int64)) {
	ctx, cancel := context.WithTimeout(context.Background(), opts.SSEHold)
	defer cancel()
	req, err := newRequest(ctx, opts)
	if err != nil {
		recordErr(err)
		return
	}
	t0 := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		// A deadline-exceeded here means we never got headers; count it.
		recordErr(err)
		return
	}
	status := resp.StatusCode
	// Read until the context deadline fires (server holds the stream open),
	// then close. The copy returns with a context error, which is expected.
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	recordOK(status, time.Since(t0).Microseconds())
}
