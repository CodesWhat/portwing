// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/config"
)

const clientPolicyCredential = "client-policy-test-credential"

type clientTestAuthority struct {
	certificate *x509.Certificate
	key         ed25519.PrivateKey
	pem         []byte
}

func newClientTestAuthority(t *testing.T) clientTestAuthority {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Portwing test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	encoded, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return clientTestAuthority{certificate, private, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: encoded})}
}

func (a clientTestAuthority) client(t *testing.T, usage x509.ExtKeyUsage, expires time.Time) tls.Certificate {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "test controller"}, NotBefore: time.Now().Add(-2 * time.Hour), NotAfter: expires, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
	encoded, err := x509.CreateCertificate(rand.Reader, template, a.certificate, public, a.key)
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: encoded}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}))
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}

func clientPolicyConfig(t *testing.T, caFile string) *config.Config {
	t.Helper()
	for name, value := range map[string]string{"TLS_CLIENT_CA": caFile, "TLS_CERT": "server.crt", "TLS_KEY": "server.key", "DRYDOCK_URL": "", "TOKEN": clientPolicyCredential, "TOKEN_HASH": "", "AUTHORIZED_KEYS_FILE": "", "ENROLLMENT_TOKEN": ""} {
		t.Setenv(name, value)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.AuditLog = ""
	return cfg
}

func startClientPolicyServer(t *testing.T, caFile string) *httptest.Server {
	t.Helper()
	cfg := clientPolicyConfig(t, caFile)
	dockerClient, stop := newStubDockerClient(t)
	t.Cleanup(stop)
	agent, err := NewServer(cfg, dockerClient, &stubServerAdapter{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = agent.Shutdown(ctx)
	})
	server := httptest.NewUnstartedServer(agent.httpServer.Handler)
	server.TLS = agent.httpServer.TLSConfig.Clone()
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func policyRequest(t *testing.T, server *httptest.Server, certificate *tls.Certificate, path string, authenticated bool) (int, error) {
	t.Helper()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	policy := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	if certificate != nil {
		policy.Certificates = []tls.Certificate{*certificate}
	}
	transport := &http.Transport{TLSClientConfig: policy}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if authenticated {
		req.Header.Set("Authorization", "Bearer "+clientPolicyCredential)
	}
	response, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	return response.StatusCode, nil
}

func TestClientCertificateTLSHandshakeAndRequestAuth(t *testing.T) {
	authority := newClientTestAuthority(t)
	other := newClientTestAuthority(t)
	file := filepath.Join(t.TempDir(), "client-ca.pem")
	if err := os.WriteFile(file, authority.pem, 0600); err != nil {
		t.Fatal(err)
	}
	server := startClientPolicyServer(t, file)
	valid := authority.client(t, x509.ExtKeyUsageClientAuth, time.Now().Add(30*time.Minute))
	foreign := other.client(t, x509.ExtKeyUsageClientAuth, time.Now().Add(30*time.Minute))
	expired := authority.client(t, x509.ExtKeyUsageClientAuth, time.Now().Add(-time.Minute))
	wrongUsage := authority.client(t, x509.ExtKeyUsageServerAuth, time.Now().Add(30*time.Minute))
	for _, tc := range []struct {
		name        string
		certificate *tls.Certificate
	}{{"missing", nil}, {"untrusted", &foreign}, {"expired", &expired}, {"wrong usage", &wrongUsage}} {
		t.Run(tc.name, func(t *testing.T) {
			if status, err := policyRequest(t, server, tc.certificate, "/health", true); err == nil {
				t.Fatalf("HTTP status=%d, want TLS rejection", status)
			}
		})
	}
	for _, authenticated := range []bool{false, true} {
		status, err := policyRequest(t, server, &valid, "/_portwing/metrics", authenticated)
		want := http.StatusUnauthorized
		if authenticated {
			want = http.StatusOK
		}
		if err != nil || status != want {
			t.Errorf("credential=%v: status=%d error=%v, want%d", authenticated, status, err, want)
		}
	}
}

func TestClientCertificateDisabledKeepsOrdinaryTLS(t *testing.T) {
	server := startClientPolicyServer(t, "")
	if status, err := policyRequest(t, server, nil, "/health", false); err != nil || status != http.StatusOK {
		t.Fatalf("status=%d error=%v, want200", status, err)
	}
}

func TestClientCertificateTrustChangesOnRestart(t *testing.T) {
	first := newClientTestAuthority(t)
	second := newClientTestAuthority(t)
	file := filepath.Join(t.TempDir(), "client-ca.pem")
	if err := os.WriteFile(file, first.pem, 0600); err != nil {
		t.Fatal(err)
	}
	original := startClientPolicyServer(t, file)
	a := first.client(t, x509.ExtKeyUsageClientAuth, time.Now().Add(30*time.Minute))
	b := second.client(t, x509.ExtKeyUsageClientAuth, time.Now().Add(30*time.Minute))
	if err := os.WriteFile(file, second.pem, 0600); err != nil {
		t.Fatal(err)
	}
	restarted := startClientPolicyServer(t, file)
	for _, tc := range []struct {
		name        string
		server      *httptest.Server
		certificate *tls.Certificate
		allowed     bool
	}{{"old accepts first", original, &a, true}, {"old rejects second", original, &b, false}, {"restart accepts second", restarted, &b, true}, {"restart rejects first", restarted, &a, false}} {
		t.Run(tc.name, func(t *testing.T) {
			status, err := policyRequest(t, tc.server, tc.certificate, "/health", false)
			if tc.allowed {
				if err != nil || status != http.StatusOK {
					t.Fatalf("status=%d error=%v, want200", status, err)
				}
			} else if err == nil {
				t.Fatalf("status=%d, want TLS rejection", status)
			}
		})
	}
}

func TestNewServerRejectsInvalidClientTrust(t *testing.T) {
	for _, contents := range []string{"", "not a certificate"} {
		t.Run(contents, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "client-ca.pem")
			if err := os.WriteFile(file, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			cfg := clientPolicyConfig(t, file)
			dockerClient, stop := newStubDockerClient(t)
			defer stop()
			if _, err := NewServer(cfg, dockerClient, &stubServerAdapter{}); err == nil {
				t.Fatal("invalid trust file must fail startup")
			}
		})
	}
	t.Run("missing file", func(t *testing.T) {
		cfg := clientPolicyConfig(t, filepath.Join(t.TempDir(), "missing.pem"))
		dockerClient, stop := newStubDockerClient(t)
		defer stop()
		if _, err := NewServer(cfg, dockerClient, &stubServerAdapter{}); err == nil {
			t.Fatal("missing trust file must fail startup")
		}
	})
}

func TestNewServerClientCARequiresNativeStandardTLS(t *testing.T) {
	authority := newClientTestAuthority(t)
	file := filepath.Join(t.TempDir(), "client-ca.pem")
	if err := os.WriteFile(file, authority.pem, 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"edge", "missing certificate", "missing key"} {
		t.Run(name, func(t *testing.T) {
			cfg := clientPolicyConfig(t, file)
			switch name {
			case "edge":
				cfg.DrydockURL = "https://controller.example.com"
			case "missing certificate":
				cfg.TLSCert = ""
			case "missing key":
				cfg.TLSKey = ""
			}
			client, stop := newStubDockerClient(t)
			defer stop()
			if _, err := NewServer(cfg, client, &stubServerAdapter{}); err == nil {
				t.Fatal("client CA on unsupported listener must fail startup")
			}
		})
	}
}
