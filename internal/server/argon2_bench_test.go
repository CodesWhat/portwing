package server

import "testing"

// benchPHC is a real OWASP-parameter Argon2id PHC string (m=19456,t=2,p=1),
// generated once so the parse/verify benchmarks below run against the genuine
// production hash shape rather than a hand-rolled constant.
var benchPHC, benchPHCErr = HashToken("correct-horse-battery-staple")

func mustBenchPHC(b *testing.B) string {
	b.Helper()
	if benchPHCErr != nil {
		b.Fatalf("HashToken: %v", benchPHCErr)
	}
	return benchPHC
}

// BenchmarkParsePHC measures the startup-path cost of decoding a PHC string into
// Argon2id parameters. Cheap, but it runs once per process boot and is a fuzz
// target, so we track it for regressions.
func BenchmarkParsePHC(b *testing.B) {
	valid := mustBenchPHC(b)
	cases := []struct {
		name string
		phc  string
	}{
		{"valid", valid},
		{"wrong_prefix", "$argon2i$v=19$m=19456,t=2,p=1$c2FsdHNhbHQ$aGFzaGhhc2g"},
		{"malformed", "$argon2id$v=19$m=19456,t=2$short"},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, _ = ParsePHC(c.phc)
			}
		})
	}
}

// BenchmarkArgon2idParamsVerify measures the full Argon2id key derivation — the
// cold, deliberately-expensive path taken on the first request (and on every
// failed attempt). This is the dominant auth cost when no token has been cached
// yet, so a regression here directly raises tail latency under credential churn.
func BenchmarkArgon2idParamsVerify(b *testing.B) {
	params, err := ParsePHC(mustBenchPHC(b))
	if err != nil {
		b.Fatalf("ParsePHC: %v", err)
	}
	cases := []struct {
		name     string
		password string
	}{
		{"correct", "correct-horse-battery-staple"},
		{"wrong", "wrong-horse-battery-staple"},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = params.Verify(c.password)
			}
		})
	}
}

// BenchmarkArgon2VerifierVerify measures the per-request verifier as the server
// actually uses it: a warmed verifier compares only the SHA-256 of the token
// (the flat-cost steady state), while a rejected token always falls through to
// the full Argon2id derivation since wrong tokens never populate the cache.
func BenchmarkArgon2VerifierVerify(b *testing.B) {
	params, err := ParsePHC(mustBenchPHC(b))
	if err != nil {
		b.Fatalf("ParsePHC: %v", err)
	}

	b.Run("warm_cache_hit", func(b *testing.B) {
		v := newArgon2Verifier(params)
		// Prime the SHA-256 success cache with one real verification.
		if !v.Verify("correct-horse-battery-staple") {
			b.Fatal("priming verification failed")
		}
		b.ReportAllocs()
		for b.Loop() {
			_ = v.Verify("correct-horse-battery-staple")
		}
	})

	b.Run("reject", func(b *testing.B) {
		v := newArgon2Verifier(params)
		b.ReportAllocs()
		for b.Loop() {
			_ = v.Verify("wrong-horse-battery-staple")
		}
	})
}

// BenchmarkRawTokenVerifierVerify measures the plain-text constant-time compare
// used when TOKEN (not TOKEN_HASH) is configured — the cheapest auth path.
func BenchmarkRawTokenVerifierVerify(b *testing.B) {
	v := &rawTokenVerifier{token: "correct-horse-battery-staple"}
	cases := []struct {
		name  string
		token string
	}{
		{"match", "correct-horse-battery-staple"},
		{"mismatch", "wrong-horse-battery-staple"},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = v.Verify(c.token)
			}
		})
	}
}
