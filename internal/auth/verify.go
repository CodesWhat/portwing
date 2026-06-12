package auth

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// Header names used in Ed25519 request authentication.
const (
	HeaderKeyID     = "X-Lookout-Key-ID"
	HeaderTimestamp = "X-Lookout-Timestamp"
	HeaderNonce     = "X-Lookout-Nonce"
	HeaderSignature = "X-Lookout-Signature"
	HeaderReason    = "X-Lookout-Reason"
)

// Sentinel errors returned by VerifyRequest. Callers can use errors.Is.
var (
	ErrMissingHeaders = errors.New("ed25519: missing required signature headers")
	ErrUnknownKey     = errors.New("ed25519: unknown key ID")
	ErrTimestampSkew  = errors.New("ed25519: timestamp outside allowed window")
	ErrNonceReplay    = errors.New("ed25519: nonce already seen (replay)")
	ErrBadSignature   = errors.New("ed25519: signature verification failed")
	ErrInvalidNonce   = errors.New("ed25519: nonce must be 32 hex characters")
	ErrInvalidSig     = errors.New("ed25519: signature is not valid base64url")
)

// ReasonFor maps a sentinel error to the value for the X-Lookout-Reason
// header returned with 401 responses.
func ReasonFor(err error) string {
	switch {
	case errors.Is(err, ErrTimestampSkew):
		return "timestamp-skew"
	case errors.Is(err, ErrNonceReplay):
		return "replay"
	case errors.Is(err, ErrUnknownKey):
		return "unknown-key"
	default:
		return "invalid-signature"
	}
}

// CanonicalMessage constructs the canonical byte string that is signed and
// verified for every authenticated request. The format (Appendix A) is:
//
//	METHOD\nPATH\nbody-sha256-hex\nunix-timestamp\nnonce
//
// For an empty body, bodyHashHex must be the full SHA-256 of the empty string:
//
//	e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
func CanonicalMessage(method, path, bodyHashHex string, timestampUnix int64, nonce string) []byte {
	return []byte(fmt.Sprintf("%s\n%s\n%s\n%d\n%s",
		method, path, bodyHashHex, timestampUnix, nonce))
}

// emptyBodyHash is the SHA-256 hex digest of the empty string, used when the
// request body is absent or has zero length.
const emptyBodyHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// BodyHashHex computes the hex-encoded SHA-256 of b.
// If b is nil or zero-length, it returns the canonical empty-body hash.
func BodyHashHex(b []byte) string {
	if len(b) == 0 {
		return emptyBodyHash
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// VerifyRequest verifies an incoming HTTP request's Ed25519 signature.
//
// It reads the four Ed25519 headers (X-Lookout-Key-ID, X-Lookout-Timestamp,
// X-Lookout-Nonce, X-Lookout-Signature) and:
//  1. Looks up the key in registry.
//  2. Checks the timestamp against maxSkew.
//  3. Checks the nonce LRU for replay.
//  4. Verifies the Ed25519 signature over the canonical message.
//  5. Records the nonce on success.
//
// body is the raw request body (already read by the caller). The caller is
// responsible for replacing r.Body with a new reader if needed.
//
// Returns the key ID string and nil on success. Returns ("", err) on failure.
func VerifyRequest(
	r *http.Request,
	body []byte,
	registry *KeyRegistry,
	lru *NonceLRU,
	maxSkewSeconds int,
) (keyID string, err error) {
	sigHeader := r.Header.Get(HeaderSignature)
	if sigHeader == "" {
		return "", ErrMissingHeaders
	}

	kidHeader := r.Header.Get(HeaderKeyID)
	tsHeader := r.Header.Get(HeaderTimestamp)
	nonceHeader := r.Header.Get(HeaderNonce)

	if kidHeader == "" || tsHeader == "" || nonceHeader == "" {
		return "", ErrMissingHeaders
	}

	// Validate nonce format: must be exactly 32 hex characters.
	if len(nonceHeader) != 32 {
		return "", ErrInvalidNonce
	}

	// Look up key.
	key, ok := registry.LookupByID(kidHeader)
	if !ok {
		return "", ErrUnknownKey
	}

	// Parse and check timestamp.
	tsUnix, err := strconv.ParseInt(tsHeader, 10, 64)
	if err != nil {
		return "", ErrTimestampSkew
	}
	skew := time.Since(time.Unix(tsUnix, 0))
	if skew < 0 {
		skew = -skew
	}
	maxSkew := time.Duration(maxSkewSeconds) * time.Second
	if skew > maxSkew {
		return "", ErrTimestampSkew
	}

	// Warn on large but still-acceptable skew.
	if skew > 30*time.Second {
		slog.Warn("clock skew warning",
			"skew_seconds", int(skew.Seconds()),
			"key_id", kidHeader)
	}

	// Replay check.
	if lru.Seen(nonceHeader) {
		return "", ErrNonceReplay
	}

	// Decode signature (base64url, no padding).
	sig, err := base64.RawURLEncoding.DecodeString(sigHeader)
	if err != nil {
		return "", ErrInvalidSig
	}

	// Build canonical message.
	bodyHash := BodyHashHex(body)
	msg := CanonicalMessage(r.Method, r.URL.Path, bodyHash, tsUnix, nonceHeader)

	// Verify signature.
	if !ed25519.Verify(ed25519.PublicKey(key.PubKey), msg, sig) {
		return "", ErrBadSignature
	}

	// Record the nonce after successful verification. Add is the
	// authoritative atomic check-and-set: if two copies of the same request
	// race past the Seen() pre-check above, only one wins here.
	if !lru.Add(nonceHeader) {
		return "", ErrNonceReplay
	}

	return kidHeader, nil
}
