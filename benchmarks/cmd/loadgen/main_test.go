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
		name string
		out  result
		want string
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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := resultFailure(tc.out)
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
	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/final" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/final", http.StatusFound)
	}))
	t.Cleanup(redirectServer.Close)

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
			name: "redirect",
			args: []string{"-base", redirectServer.URL, "-path", "/", "-concurrency", "1", "-duration", "20ms"},
			want: "non-2xx",
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
