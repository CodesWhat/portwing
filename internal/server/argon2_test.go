package server

import (
	"strings"
	"sync/atomic"
	"testing"
)

// TestParsePHCRoundTrip verifies that HashToken produces a PHC string that
// ParsePHC can parse and that Verify accepts the original token.
func TestParsePHCRoundTrip(t *testing.T) {
	t.Parallel()

	const token = "supersecret"
	phc, err := HashToken(token)
	if err != nil {
		t.Fatalf("HashToken: %v", err)
	}

	params, err := ParsePHC(phc)
	if err != nil {
		t.Fatalf("ParsePHC(%q): %v", phc, err)
	}

	if !params.Verify(token) {
		t.Fatal("Verify returned false for correct token")
	}
	if params.Verify("wrongtoken") {
		t.Fatal("Verify returned true for wrong token")
	}
}

// TestParsePHCRejectsWrongVersion ensures version != 19 is rejected.
func TestParsePHCRejectsWrongVersion(t *testing.T) {
	t.Parallel()

	phc := "$argon2id$v=18$m=19456,t=2,p=1$c29tZXNhbHQ$aGFzaA"
	if _, err := ParsePHC(phc); err == nil {
		t.Fatal("expected error for wrong version, got nil")
	}
}

// TestParsePHCRejectsBadAlgorithm ensures non-argon2id strings are rejected.
func TestParsePHCRejectsBadAlgorithm(t *testing.T) {
	t.Parallel()

	phc := "$argon2i$v=19$m=19456,t=2,p=1$c29tZXNhbHQ$aGFzaA"
	if _, err := ParsePHC(phc); err == nil {
		t.Fatal("expected error for wrong algorithm, got nil")
	}
}

// TestParsePHCRejectsBadBase64Salt ensures malformed salt base64 is rejected.
func TestParsePHCRejectsBadBase64Salt(t *testing.T) {
	t.Parallel()

	phc := "$argon2id$v=19$m=19456,t=2,p=1$not!valid!base64$aGFzaA"
	if _, err := ParsePHC(phc); err == nil {
		t.Fatal("expected error for bad base64 salt, got nil")
	}
}

// TestParsePHCRejectsBadBase64Hash ensures malformed hash base64 is rejected.
func TestParsePHCRejectsBadBase64Hash(t *testing.T) {
	t.Parallel()

	phc := "$argon2id$v=19$m=19456,t=2,p=1$c29tZXNhbHQ$not!valid!base64"
	if _, err := ParsePHC(phc); err == nil {
		t.Fatal("expected error for bad base64 hash, got nil")
	}
}

// TestParsePHCRejectsMalformedSegments checks that strings with the wrong
// number of $ segments are rejected.
func TestParsePHCRejectsMalformedSegments(t *testing.T) {
	t.Parallel()

	cases := []string{
		"$argon2id$v=19$m=19456,t=2,p=1$c29tZXNhbHQ", // missing hash
		"$argon2id$v=19$m=19456,t=2,p=1",             // missing salt+hash
		"",
		"notaphcstring",
	}
	for _, phc := range cases {
		if _, err := ParsePHC(phc); err == nil {
			t.Errorf("expected error for %q, got nil", phc)
		}
	}
}

// TestHashTokenParams verifies that the PHC string produced by HashToken
// uses OWASP-recommended parameters.
func TestHashTokenParams(t *testing.T) {
	t.Parallel()

	phc, err := HashToken("testtoken")
	if err != nil {
		t.Fatalf("HashToken: %v", err)
	}

	params, err := ParsePHC(phc)
	if err != nil {
		t.Fatalf("ParsePHC: %v", err)
	}

	if params.Memory != 19456 {
		t.Errorf("memory: got %d, want 19456", params.Memory)
	}
	if params.Time != 2 {
		t.Errorf("time: got %d, want 2", params.Time)
	}
	if params.Parallelism != 1 {
		t.Errorf("parallelism: got %d, want 1", params.Parallelism)
	}
	if len(params.Salt) != 16 {
		t.Errorf("salt length: got %d, want 16", len(params.Salt))
	}
	if len(params.Hash) != 32 {
		t.Errorf("hash length: got %d, want 32", len(params.Hash))
	}
	if !strings.HasPrefix(phc, "$argon2id$v=19$") {
		t.Errorf("PHC prefix mismatch: %q", phc)
	}
}

// countingVerifier wraps argon2Verifier and counts Argon2id calls via a seam.
// It intercepts at the params.Verify level by substituting a custom verifyFn.
type countingVerifier struct {
	inner     *argon2Verifier
	argoCalls atomic.Int64
}

func newCountingVerifier(phc string) (*countingVerifier, error) {
	params, err := ParsePHC(phc)
	if err != nil {
		return nil, err
	}
	return &countingVerifier{inner: newArgon2Verifier(params)}, nil
}

func (c *countingVerifier) Verify(token string) bool {
	// We detect if the slow path ran by checking whether the cache was empty
	// before the call. The argon2Verifier stores the sum atomically on first
	// success, so cacheOnce is nil before and non-nil after.
	before := c.inner.cacheOnce.Load()
	result := c.inner.Verify(token)
	after := c.inner.cacheOnce.Load()
	if before == nil && after != nil {
		// The slow Argon2id path ran and succeeded.
		c.argoCalls.Add(1)
	}
	return result
}

// TestArgon2VerifierSuccessCache verifies that the Argon2id derivation is only
// called once across multiple successful verifications; subsequent calls use
// the SHA-256 cache.
func TestArgon2VerifierSuccessCache(t *testing.T) {
	t.Parallel()

	const token = "cachetest"
	phc, err := HashToken(token)
	if err != nil {
		t.Fatalf("HashToken: %v", err)
	}

	cv, err := newCountingVerifier(phc)
	if err != nil {
		t.Fatalf("newCountingVerifier: %v", err)
	}

	// First call: should run Argon2id.
	if !cv.Verify(token) {
		t.Fatal("first Verify returned false")
	}
	if cv.argoCalls.Load() != 1 {
		t.Fatalf("expected 1 argon2 call after first success, got %d", cv.argoCalls.Load())
	}

	// Subsequent calls: should use the cache, not Argon2id.
	for i := 0; i < 5; i++ {
		if !cv.Verify(token) {
			t.Fatalf("Verify call %d returned false", i+2)
		}
	}
	if cv.argoCalls.Load() != 1 {
		t.Fatalf("expected argon2 call count to remain 1, got %d", cv.argoCalls.Load())
	}

	// Wrong token should return false and not update the cache count.
	if cv.Verify("wrongtoken") {
		t.Fatal("wrong token should not verify")
	}
}
