package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codeswhat/portwing/internal/audit"
)

// silenceSlog routes the package default logger to io.Discard for the duration
// of a benchmark and restores it afterward, so the rejection path's slog.Warn
// calls don't flood CI stderr with tens of thousands of lines.
func silenceSlog(b *testing.B) {
	b.Helper()
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	b.Cleanup(func() { slog.SetDefault(prev) })
}

// noopAuditor returns a disabled audit logger (writes nowhere), so the middleware
// benchmarks measure the auth path itself, not log I/O.
func noopAuditor(b *testing.B) *audit.Logger {
	b.Helper()
	l, cleanup, err := audit.New("", 0)
	if err != nil {
		b.Fatalf("audit.New: %v", err)
	}
	b.Cleanup(cleanup)
	return l
}

// BenchmarkAuthMiddleware measures the full per-request middleware cost —
// rate-limit lookup, token extraction, verification, and the statusRecorder
// wrap — for the authorized, rejected, and no-auth-configured paths. This is the
// tax every proxied request pays, so it's the most load-bearing benchmark here.
func BenchmarkAuthMiddleware(b *testing.B) {
	silenceSlog(b)
	auditor := noopAuditor(b)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	cases := []struct {
		name     string
		verifier tokenVerifier
		token    string
	}{
		{"authorized_raw", newRawTokenVerifier("secret"), "secret"},
		{"rejected_raw", newRawTokenVerifier("secret"), "wrong"},
		{"passthrough_no_auth", nil, ""},
	}

	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			rl := NewRateLimiter()
			h := rl.AuthMiddleware(c.verifier, auditor, nil, next)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/containers", nil)
			req.RemoteAddr = "192.0.2.10:40000"
			if c.token != "" {
				req.Header.Set("Authorization", "Bearer "+c.token)
			}
			b.ReportAllocs()
			for b.Loop() {
				h.ServeHTTP(httptest.NewRecorder(), req)
			}
		})
	}
}

// BenchmarkClientIP measures client-IP extraction, which runs on every request.
// The trusted-proxy case walks an X-Forwarded-For chain right-to-left, the most
// expensive shape.
func BenchmarkClientIP(b *testing.B) {
	direct := NewRateLimiter()

	proxied := NewRateLimiter()
	nets, err := ParseTrustedProxies([]string{"10.0.0.0/8", "192.0.2.0/24"})
	if err != nil {
		b.Fatalf("ParseTrustedProxies: %v", err)
	}
	proxied.SetTrustedProxies(nets)

	b.Run("direct_no_proxies", func(b *testing.B) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "203.0.113.5:51000"
		b.ReportAllocs()
		for b.Loop() {
			_ = direct.clientIP(req)
		}
	})

	b.Run("trusted_proxy_xff_chain", func(b *testing.B) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "192.0.2.1:51000"
		req.Header.Set("X-Forwarded-For", "203.0.113.7, 10.1.2.3, 192.0.2.9")
		b.ReportAllocs()
		for b.Loop() {
			_ = proxied.clientIP(req)
		}
	})

	b.Run("untrusted_peer", func(b *testing.B) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "203.0.113.5:51000"
		req.Header.Set("X-Forwarded-For", "8.8.8.8")
		b.ReportAllocs()
		for b.Loop() {
			_ = proxied.clientIP(req)
		}
	})
}

// BenchmarkParseTrustedProxies measures the startup parse of the TRUSTED_PROXIES
// CIDR list (also a fuzz target).
func BenchmarkParseTrustedProxies(b *testing.B) {
	cases := []struct {
		name    string
		entries []string
	}{
		{"cidrs", []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}},
		{"bare_ips", []string{"203.0.113.1", "203.0.113.2", "2001:db8::1"}},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := ParseTrustedProxies(c.entries); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkAgentToken measures token header extraction across the three accepted
// schemes, in the order the middleware probes them.
func BenchmarkAgentToken(b *testing.B) {
	cases := []struct {
		name   string
		header string
		value  string
	}{
		{"bearer", "Authorization", "Bearer secret-token-value"},
		{"portwing_header", headerPortwingToken, "secret-token-value"},
		{"drydock_secret", headerDrydockAgentSecret, "secret-token-value"},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set(c.header, c.value)
			b.ReportAllocs()
			for b.Loop() {
				_ = agentToken(req)
			}
		})
	}
}

// BenchmarkRateLimiter measures the two hot rate-limiter operations under both
// sequential and concurrent access, since every request takes the mutex once for
// the IsRateLimited check and rejected requests take it again to record.
func BenchmarkRateLimiter(b *testing.B) {
	b.Run("is_rate_limited", func(b *testing.B) {
		rl := NewRateLimiter()
		rl.RecordFailure("203.0.113.5")
		b.ReportAllocs()
		for b.Loop() {
			_ = rl.IsRateLimited("203.0.113.5")
		}
	})

	b.Run("record_failure", func(b *testing.B) {
		rl := NewRateLimiter()
		b.ReportAllocs()
		for b.Loop() {
			rl.RecordFailure("203.0.113.5")
		}
	})

	b.Run("is_rate_limited_parallel", func(b *testing.B) {
		rl := NewRateLimiter()
		rl.RecordFailure("203.0.113.5")
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_ = rl.IsRateLimited("203.0.113.5")
			}
		})
	})
}
