// SPDX-License-Identifier: MIT

package docker

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestParseTimeoutHeader(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		ms   int64
		want bool
	}{
		{"60000", 60000, true},
		{"0", 0, true},
		{"900000", 900000, true},
		{"9999999", 9999999, true},
		{"", 0, false},
		{"10000000", 0, false},
		{"-5", 0, false},
		{"+5", 0, false},
		{"1.5", 0, false},
		{"1e3", 0, false},
		{" 5", 0, false},
		{"abc", 0, false},
		{"１２", 0, false},
	}
	for _, tc := range cases {
		ms, ok := ParseTimeoutHeader(tc.in)
		if ok != tc.want || ms != tc.ms {
			t.Errorf("ParseTimeoutHeader(%q) = %d, %v; want %d, %v", tc.in, ms, ok, tc.ms, tc.want)
		}
	}
}

func TestRequestDeadlineDisabledDefault(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/containers/a/stop?t=60", "/containers/a/wait", "/info"} {
		if d, explicit := RequestDeadline("POST", path, 0, false, 0); d != 0 || explicit {
			t.Errorf("%s: RequestDeadline = %v, %v; want unbounded", path, d, explicit)
		}
	}
	if d, _ := RequestDeadline("POST", "/images/load", 5000, true, 0); d != 5*time.Second {
		t.Errorf("hint with disabled default = %v, want 5s", d)
	}
}

func TestDoWithDeadline_ZeroIsUnbounded(t *testing.T) {
	t.Parallel()
	sock := startUnixHTTPServer(t, slowDockerd(300*time.Millisecond))
	c, err := NewClient(sock, 0)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.DoWithDeadline(context.Background(), http.MethodGet, "/info", nil, nil, c.DefaultTimeout())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if b, _ := io.ReadAll(resp.Body); string(b) != "done" {
		t.Fatalf("body = %q", b)
	}
}

func TestRequestDeadline(t *testing.T) {
	t.Parallel()
	def := 30 * time.Second
	cases := []struct {
		name     string
		method   string
		path     string
		hint     int64
		have     bool
		want     time.Duration
		explicit bool
	}{
		{"no hint plain", "GET", "/containers/json", 0, false, def, false},
		{"hint below min clamps up", "GET", "/info", 1, true, time.Second, true},
		{"hint exactly min", "GET", "/info", 1000, true, time.Second, true},
		{"hint mid", "POST", "/images/load", 60000, true, time.Minute, true},
		{"hint exactly max", "GET", "/info", 900000, true, 15 * time.Minute, true},
		{"hint above max clamps down", "GET", "/info", 9999999, true, 15 * time.Minute, true},
		{"hint huge", "GET", "/info", 1 << 62, true, 15 * time.Minute, true},
		{"hint zero is no hint", "GET", "/info", 0, true, def, false},
		{"hint negative is no hint", "GET", "/info", -5, true, def, false},
		{"hint ignored when not present", "GET", "/info", 5000, false, def, false},
		{"stop t=60", "POST", "/containers/abc/stop?t=60", 0, false, 75 * time.Second, true},
		{"stop t=5 keeps default", "POST", "/containers/abc/stop?t=5", 0, false, def, true},
		{"stop t absent", "POST", "/containers/abc/stop", 0, false, def, false},
		{"stop t garbage", "POST", "/containers/abc/stop?t=x", 0, false, def, false},
		{"stop t=-1 is forever", "POST", "/containers/abc/stop?t=-1", 0, false, 15 * time.Minute, true},
		{"stop t huge is capped", "POST", "/containers/abc/stop?t=100000", 0, false, 15 * time.Minute, true},
		{"stop t=890 caps at max", "POST", "/containers/abc/stop?t=890", 0, false, 15 * time.Minute, true},
		{"restart t=60 versioned", "POST", "/v1.45/containers/abc/restart?t=60", 0, false, 75 * time.Second, true},
		{"stop GET not a stop", "GET", "/containers/abc/stop?t=60", 0, false, def, false},
		{"wait", "POST", "/containers/abc/wait?condition=not-running", 0, false, 15 * time.Minute, true},
		{"wait encoded path", "POST", "/containers/abc%2Fx/wait", 0, false, def, false},
		{"hint beats stop rule", "POST", "/containers/abc/stop?t=60", 2000, true, 2 * time.Second, true},
		{"hint beats wait rule", "POST", "/containers/abc/wait", 5000, true, 5 * time.Second, true},
	}
	for _, tc := range cases {
		got, explicit := RequestDeadline(tc.method, tc.path, tc.hint, tc.have, def)
		if got != tc.want || explicit != tc.explicit {
			t.Errorf("%s: RequestDeadline = %v, %v; want %v, %v", tc.name, got, explicit, tc.want, tc.explicit)
		}
	}
}

// slowDockerd answers every request after delay.
func slowDockerd(delay time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			_, _ = io.WriteString(w, `{"Version":"24.0.5","ApiVersion":"1.45"}`)
			return
		}
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "done")
	})
}

func TestDoWithDeadline_OutlivesDefaultTimeout(t *testing.T) {
	t.Parallel()
	sock := startUnixHTTPServer(t, slowDockerd(1500*time.Millisecond))
	c, err := NewClient(sock, 1)
	if err != nil {
		t.Fatal(err)
	}

	// Default path still dies at REQUEST_TIMEOUT.
	if resp, err := c.DoWithHeaders(context.Background(), http.MethodPost, "/containers/a/stop", nil, nil); err == nil {
		resp.Body.Close()
		t.Fatal("default client should have timed out")
	}

	resp, err := c.DoWithDeadline(context.Background(), http.MethodPost, "/containers/a/stop", nil, nil, 5*time.Second)
	if err != nil {
		t.Fatalf("hinted call failed: %v", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil || string(b) != "done" {
		t.Fatalf("body = %q, %v", b, err)
	}
}

func TestDoWithDeadline_TimeoutIsCoded(t *testing.T) {
	t.Parallel()
	sock := startUnixHTTPServer(t, slowDockerd(5*time.Second))
	c, err := NewClient(sock, 30)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = c.DoWithDeadline(context.Background(), http.MethodGet, "/info", nil, nil, 300*time.Millisecond) //nolint:bodyclose // always an error here, no response
	if _, ok := AsTimeout(err); !ok {
		t.Fatalf("err = %v, want TimeoutError", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("deadline not enforced")
	}
}

func TestDoWithDeadline_ParentCancelIsNotTimeout(t *testing.T) {
	t.Parallel()
	sock := startUnixHTTPServer(t, slowDockerd(5*time.Second))
	c, err := NewClient(sock, 30)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	_, err = c.DoWithDeadline(ctx, http.MethodGet, "/info", nil, nil, 10*time.Second) //nolint:bodyclose // always an error here, no response
	if err == nil {
		t.Fatal("want error")
	}
	if _, ok := AsTimeout(err); ok {
		t.Fatalf("parent cancel misreported as timeout: %v", err)
	}
}

func TestDoWithDeadline_SlowBodyIsCovered(t *testing.T) {
	t.Parallel()
	sock := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			_, _ = io.WriteString(w, `{"Version":"24.0.5","ApiVersion":"1.45"}`)
			return
		}
		w.WriteHeader(200)
		_, _ = io.WriteString(w, "part")
		w.(http.Flusher).Flush()
		select {
		case <-time.After(5 * time.Second):
		case <-r.Context().Done():
		}
	}))
	c, err := NewClient(sock, 30)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.DoWithDeadline(context.Background(), http.MethodGet, "/info", nil, nil, 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, err = io.ReadAll(resp.Body)
	if _, ok := AsTimeout(err); !ok {
		t.Fatalf("body read err = %v, want TimeoutError", err)
	}
}

func TestDoRawWithDeadline(t *testing.T) {
	t.Parallel()
	sock := startUnixHTTPServer(t, slowDockerd(1500*time.Millisecond))
	c, err := NewClient(sock, 1)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, "http://localhost/containers/a/wait", nil)
	resp, err := c.DoRawWithDeadline(req, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if b, _ := io.ReadAll(resp.Body); string(b) != "done" {
		t.Fatalf("body = %q", b)
	}

	req, _ = http.NewRequest(http.MethodPost, "http://localhost/containers/a/wait", nil)
	_, err = c.DoRawWithDeadline(req, 200*time.Millisecond) //nolint:bodyclose // always an error here, no response
	if _, ok := AsTimeout(err); !ok {
		t.Fatalf("err = %v, want TimeoutError", err)
	}
}

func TestTimeoutErrorMessageAndUnwrap(t *testing.T) {
	t.Parallel()
	inner := io.ErrUnexpectedEOF
	err := &TimeoutError{After: 90 * time.Second, Err: inner}
	if got := err.Error(); got != "docker request exceeded 1m30s: unexpected EOF" {
		t.Fatalf("Error() = %q", got)
	}
	if err.Unwrap() != inner {
		t.Fatal("Unwrap did not return the wrapped error")
	}
}

func TestDefaultTimeoutWithoutHTTPClient(t *testing.T) {
	t.Parallel()
	if got := (&Client{}).DefaultTimeout(); got != defaultDialTimeout {
		t.Fatalf("DefaultTimeout() = %v, want %v", got, defaultDialTimeout)
	}
}
