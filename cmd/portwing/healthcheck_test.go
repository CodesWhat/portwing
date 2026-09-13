package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunHealthcheck(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tls    bool
		status int
		want   int
	}{
		{"http healthy", false, 200, 0}, {"https self signed", true, 200, 0},
		{"unavailable", false, 503, 1}, {"other success status", false, 204, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/health" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(tc.status)
			})
			srv := httptest.NewUnstartedServer(handler)
			if tc.tls {
				srv.StartTLS()
			} else {
				srv.Start()
			}
			defer srv.Close()
			_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
			t.Setenv("PORT", port)
			t.Setenv("TLS_CERT", "")
			if tc.tls {
				t.Setenv("TLS_CERT", "/not/read/by/probe")
			}
			t.Setenv("TOKEN", "conflict")
			t.Setenv("TOKEN_HASH", "conflict")
			t.Setenv("DOCKER_SOCKET", "/nonexistent/socket")
			t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
			t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
			var out, errOut bytes.Buffer
			if code := run([]string{"portwing", "healthcheck"}, strings.NewReader(""), &out, &errOut); code != tc.want {
				t.Fatalf("exit = %d, want %d: %s", code, tc.want, &errOut)
			}
			if requests.Load() != 1 {
				t.Fatalf("requests = %d", requests.Load())
			}
		})
	}
}

func TestRunHealthcheckRejectsRedirect(t *testing.T) {
	var followed atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed.Store(true) }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	t.Setenv("PORT", port)
	t.Setenv("TLS_CERT", "")
	var out bytes.Buffer
	if code := run([]string{"portwing", "healthcheck"}, strings.NewReader(""), &out, &out); code != 1 {
		t.Fatalf("exit = %d", code)
	}
	if followed.Load() {
		t.Fatal("followed health endpoint redirect")
	}
}

func TestRunHealthcheckTimeout(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); <-r.Context().Done() }))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	t.Setenv("PORT", port)
	t.Setenv("TLS_CERT", "")
	var out bytes.Buffer
	start := time.Now()
	code := run([]string{"portwing", "healthcheck"}, strings.NewReader(""), &out, &out)
	if code != 1 || requests.Load() != 1 {
		t.Fatalf("exit = %d, requests = %d: %s", code, requests.Load(), &out)
	}
	if elapsed := time.Since(start); elapsed > 4500*time.Millisecond {
		t.Fatalf("probe exceeded Docker healthcheck deadline: %v", elapsed)
	}
}

func TestHealthcheckURL(t *testing.T) {
	for _, tc := range []struct{ port, cert, want string }{
		{"", "", "http://localhost:3000/health"},
		{"4567", "", "http://localhost:4567/health"},
		{"4567", "cert.pem", "https://localhost:4567/health"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			t.Setenv("PORT", tc.port)
			t.Setenv("TLS_CERT", tc.cert)
			got, err := healthcheckURL()
			if err != nil || got != tc.want {
				t.Fatalf("URL = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	for _, port := range []string{"0", "65536", "-1", "http://example.com", "3000@evil.example", "3000/path"} {
		t.Run(port, func(t *testing.T) {
			t.Setenv("PORT", port)
			if _, err := healthcheckURL(); err == nil {
				t.Fatal("accepted invalid port")
			}
		})
	}
}
