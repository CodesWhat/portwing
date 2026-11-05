// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codeswhat/portwing/internal/audit"
)

func TestEnrollmentRequiresSecureTransport(t *testing.T) {
	for _, tc := range []struct {
		name, remote string
		trusted      []string
		proto        []string
		tls          bool
		insecure     bool
		want         int
	}{
		{name: "plaintext", remote: "192.0.2.1:1234", want: 403},
		{name: "explicit plaintext opt-in", remote: "192.0.2.1:1234", insecure: true, want: 200},
		{name: "native TLS", remote: "192.0.2.1:1234", tls: true, want: 200},
		{name: "spoofed HTTPS", remote: "192.0.2.1:1234", proto: []string{"https"}, want: 403},
		{name: "untrusted peer", remote: "192.0.2.1:1234", trusted: []string{"10.0.0.0/8"}, proto: []string{"https"}, want: 403},
		{name: "trusted HTTPS", remote: "10.0.0.1:1234", trusted: []string{"10.0.0.0/8"}, proto: []string{"https"}, want: 200},
		{name: "trusted IPv6 HTTPS", remote: "[2001:db8::1]:1234", trusted: []string{"2001:db8::/32"}, proto: []string{"https"}, want: 200},
		{name: "trusted HTTP", remote: "10.0.0.1:1234", trusted: []string{"10.0.0.0/8"}, proto: []string{"http"}, want: 403},
		{name: "missing protocol", remote: "10.0.0.1:1234", trusted: []string{"10.0.0.0/8"}, want: 403},
		{name: "protocol list", remote: "10.0.0.1:1234", trusted: []string{"10.0.0.0/8"}, proto: []string{"https, http"}, want: 403},
		{name: "duplicate protocol", remote: "10.0.0.1:1234", trusted: []string{"10.0.0.0/8"}, proto: []string{"https", "http"}, want: 403},
		{name: "malformed peer", remote: "not-an-IP", trusted: []string{"10.0.0.0/8"}, proto: []string{"https"}, want: 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, stop := newStubDockerClient(t)
			t.Cleanup(stop)
			cfg := minimalConfig()
			cfg.AuthorizedKeysFile = filepath.Join(t.TempDir(), "authorized_keys")
			if err := os.WriteFile(cfg.AuthorizedKeysFile, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			cfg.EnrollmentToken = "bootstrap-secret"
			cfg.AllowInsecureEnrollment = tc.insecure
			cfg.TrustedProxies = tc.trusted
			s, err := NewServer(cfg, client, &stubServerAdapter{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
			pub, _, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			body := fmt.Sprintf(`{"enrollment_token":"bootstrap-secret","public_key":%q}`, base64.StdEncoding.EncodeToString(pub))
			req := httptest.NewRequest(http.MethodPost, "/api/portwing/enroll", strings.NewReader(body))
			req.RemoteAddr = tc.remote
			if tc.tls {
				req.TLS = &tls.ConnectionState{}
			}
			for _, v := range tc.proto {
				req.Header.Add("X-Forwarded-Proto", v)
			}
			rec := httptest.NewRecorder()
			s.httpServer.Handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == http.StatusForbidden {
				records := s.auditor.Records(0)
				if len(records) != 1 || records[0].Event != audit.EventEnrollment || records[0].Outcome != "denied" {
					t.Fatalf("transport refusal audit = %+v", records)
				}
				if !s.enroller.Armed() {
					t.Fatal("transport refusal consumed enrollment credential")
				}
				data, err := os.ReadFile(cfg.AuthorizedKeysFile)
				if err != nil || len(data) != 0 {
					t.Fatalf("transport refusal changed enrollment state: %q, %v", data, err)
				}
				// Refusal must leave the credential usable over a secure connection.
				retry := httptest.NewRequest(http.MethodPost, "/api/portwing/enroll", strings.NewReader(body))
				retry.TLS = &tls.ConnectionState{}
				retry.RemoteAddr = tc.remote
				rr := httptest.NewRecorder()
				s.httpServer.Handler.ServeHTTP(rr, retry)
				if rr.Code != http.StatusOK {
					t.Fatalf("secure retry status = %d: %s", rr.Code, rr.Body.String())
				}
			}
		})
	}
}
