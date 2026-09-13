package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
				t.Setenv("TLS_CERT", writeHealthCertificate(t, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})))
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

func writeHealthCertificate(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "health-cert.pem")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunHealthcheckCertificateVerification(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "agent.example"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: priv}}}
	srv.StartTLS()
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	t.Setenv("PORT", port)
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer other.Close()
	good := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	wrong := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: other.Certificate().Raw})
	for _, tc := range []struct {
		name string
		data []byte
		want int
	}{
		{"self signed without localhost SAN", good, 0},
		{"leaf first chain", append(append([]byte{}, good...), wrong...), 0},
		{"different leaf rejects", wrong, 1},
		{"matching cert later in chain rejects", append(append([]byte{}, wrong...), good...), 1},
		{"invalid PEM", []byte("invalid"), 1},
		{"wrong PEM type", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 1},
		{"invalid DER", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("invalid")}), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TLS_CERT", writeHealthCertificate(t, tc.data))
			before := requests.Load()
			var out bytes.Buffer
			code := run([]string{"portwing", "healthcheck"}, strings.NewReader(""), &out, &out)
			if code != tc.want {
				t.Fatalf("exit=%d want=%d: %s", code, tc.want, &out)
			}
			if tc.want != 0 && requests.Load() != before {
				t.Fatal("sent HTTP request without certificate verification")
			}
		})
	}
	t.Run("missing certificate", func(t *testing.T) {
		t.Setenv("TLS_CERT", filepath.Join(t.TempDir(), "missing.pem"))
		var out bytes.Buffer
		if code := run([]string{"portwing", "healthcheck"}, strings.NewReader(""), &out, &out); code != 1 {
			t.Fatalf("exit=%d", code)
		}
	})
}

func TestRunHealthcheckInvalidPort(t *testing.T) {
	t.Setenv("PORT", "invalid")
	var out bytes.Buffer
	if code := run([]string{"portwing", "healthcheck"}, strings.NewReader(""), &out, &out); code != 1 || !strings.Contains(out.String(), "invalid PORT") {
		t.Fatalf("exit=%d: %s", code, &out)
	}
}

func TestHealthcheckCertificateVerificationMissingPeer(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	path := writeHealthCertificate(t, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
	config, err := healthcheckCertificateVerification(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = config.VerifyConnection(tls.ConnectionState{}); err == nil {
		t.Fatal("accepted absent peer certificate")
	}
}
