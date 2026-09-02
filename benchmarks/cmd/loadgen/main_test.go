package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestResultFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		out         result
		max429Ratio float64
		want        string
	}{
		{
			name: "success",
			out: result{
				TotalRequests:    2,
				StatusCodeCounts: map[int]int64{http.StatusOK: 2},
			},
		},
		{name: "zero requests", out: result{}, want: "zero requests"},
		{
			name: "transport error",
			out: result{
				TotalRequests: 1,
				ErrorRequests: 1,
				ErrorCounts:   map[string]int64{"connection refused": 1},
			},
			want: "transport errors",
		},
		{
			name: "non 2xx",
			out: result{
				TotalRequests:    2,
				StatusCodeCounts: map[int]int64{http.StatusOK: 1, http.StatusUnauthorized: 1},
			},
			want: "non-2xx",
		},
		{
			// Rate limited bursts from the anti-brute-force limiter are
			// expected under soak concurrency; a small share must not fail
			// the run.
			name: "429 under the budget is not fatal",
			out: result{
				TotalRequests:    100,
				StatusCodeCounts: map[int]int64{http.StatusOK: 95, http.StatusTooManyRequests: 5},
			},
			max429Ratio: defaultMax429Ratio,
		},
		{
			// Exactly at the budget is still inside it.
			name: "429 at the budget is not fatal",
			out: result{
				TotalRequests:    10,
				StatusCodeCounts: map[int]int64{http.StatusOK: 9, http.StatusTooManyRequests: 1},
			},
			max429Ratio: defaultMax429Ratio,
		},
		{
			// An agent rejecting load wholesale must not pass the soak
			// vacuously on a tiny handled sample.
			name: "429 over the budget is fatal",
			out: result{
				TotalRequests:    100,
				StatusCodeCounts: map[int]int64{http.StatusOK: 50, http.StatusTooManyRequests: 50},
			},
			max429Ratio: defaultMax429Ratio,
			want:        "rate limited",
		},
		{
			// 429 alongside a genuine non-2xx must still fail on the
			// genuine failure, and report it as such rather than as a
			// budget overrun.
			name: "429 plus 500 is still fatal",
			out: result{
				TotalRequests:    3,
				StatusCodeCounts: map[int]int64{http.StatusOK: 1, http.StatusTooManyRequests: 1, http.StatusInternalServerError: 1},
			},
			max429Ratio: defaultMax429Ratio,
			want:        "non-2xx",
		},
		{
			name: "zero budget makes any 429 fatal",
			out: result{
				TotalRequests:    1000,
				StatusCodeCounts: map[int]int64{http.StatusOK: 999, http.StatusTooManyRequests: 1},
			},
			max429Ratio: 0,
			want:        "rate limited",
		},
		{
			name: "full budget never fails on 429",
			out: result{
				TotalRequests:    2,
				StatusCodeCounts: map[int]int64{http.StatusTooManyRequests: 2},
			},
			max429Ratio: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := resultFailure(tc.out, tc.max429Ratio)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("resultFailure() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("resultFailure() = %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func TestLoadgenProcessRejectsAllErrorRuns(t *testing.T) {
	t.Parallel()

	errorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(errorServer.Close)
	truncatedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("short"))
	}))
	t.Cleanup(truncatedServer.Close)
	earlySSEServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {}\n\n"))
	}))
	t.Cleanup(earlySSEServer.Close)
	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/final" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/final", http.StatusFound)
	}))
	t.Cleanup(redirectServer.Close)
	rateLimitedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	t.Cleanup(rateLimitedServer.Close)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "zero requests",
			args: []string{"-concurrency", "0", "-duration", "1ms"},
			want: "zero requests",
		},
		{
			name: "transport errors",
			args: []string{"-base", "://", "-concurrency", "1", "-duration", "20ms"},
			want: "transport errors",
		},
		{
			name: "non 2xx",
			args: []string{"-base", errorServer.URL, "-path", "/", "-concurrency", "1", "-duration", "20ms"},
			want: "non-2xx",
		},
		{
			name: "truncated response body",
			args: []string{"-base", truncatedServer.URL, "-path", "/", "-concurrency", "1", "-duration", "20ms"},
			want: "transport errors",
		},
		{
			name: "SSE stream closes before hold",
			args: []string{"-base", earlySSEServer.URL, "-path", "/", "-mode", "sse", "-sse-hold", "1s", "-concurrency", "1", "-duration", "20ms"},
			want: "transport errors",
		},
		{
			name: "redirect",
			args: []string{"-base", redirectServer.URL, "-path", "/", "-concurrency", "1", "-duration", "20ms"},
			want: "non-2xx",
		},
		{
			// Every response is 429, so the run is 100% rate limited and
			// must fail the default budget even though nothing else went
			// wrong.
			name: "rate limited wholesale",
			args: []string{"-base", rateLimitedServer.URL, "-path", "/", "-concurrency", "1", "-duration", "20ms"},
			want: "rate limited",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args, err := json.Marshal(tc.args)
			if err != nil {
				t.Fatalf("marshal child args: %v", err)
			}

			cmd := exec.Command(os.Args[0], "-test.run=^TestLoadgenProcessHelper$") //nolint:gosec // Executes this fixed test binary, not user input.
			cmd.Env = append(os.Environ(),
				"PORTWING_LOADGEN_HELPER=1",
				"PORTWING_LOADGEN_ARGS="+string(args),
			)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			err = cmd.Run()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
				t.Fatalf("loadgen exit = %v, want status 1\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want failure containing %q", stderr.String(), tc.want)
			}

			var out result
			if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
				t.Fatalf("decode summary %q: %v", stdout.String(), err)
			}
			if tc.name != "zero requests" && out.TotalRequests == 0 {
				t.Fatal("all-error fixture made zero requests")
			}
		})
	}
}

func TestExecuteRejectsOutOfRange429Ratio(t *testing.T) {
	t.Parallel()

	for _, ratio := range []string{"-0.1", "1.5"} {
		t.Run(ratio, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			// Fails at flag validation, before any request is made, so no
			// server is needed.
			if got := execute([]string{"-max-429-ratio", ratio}, &stdout, &stderr); got != 2 {
				t.Fatalf("execute() = %d, want 2\nstderr: %s", got, stderr.String())
			}
			if !strings.Contains(stderr.String(), "-max-429-ratio must be between 0 and 1") {
				t.Fatalf("stderr = %q, want range error", stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want no summary before validation passes", stdout.String())
			}
		})
	}
}

func TestLoadgenProcessHelper(t *testing.T) {
	if os.Getenv("PORTWING_LOADGEN_HELPER") != "1" {
		return
	}

	var args []string
	if err := json.Unmarshal([]byte(os.Getenv("PORTWING_LOADGEN_ARGS")), &args); err != nil {
		os.Exit(2)
	}
	os.Exit(execute(args, os.Stdout, os.Stderr))
}
