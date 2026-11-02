// SPDX-License-Identifier: MIT

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
	"net/url"
	"strconv"
	"time"

	applog "github.com/codeswhat/portwing/internal/log"
)

// Header names used in Ed25519 request authentication.
const (
	HeaderKeyID     = "X-Portwing-Key-ID"
	HeaderTimestamp = "X-Portwing-Timestamp"
	HeaderNonce     = "X-Portwing-Nonce"
	HeaderSignature = "X-Portwing-Signature"
	// HeaderSignatureVersion selects the canonical request format. Version 2
	// signs the complete origin-form request target, including the raw query.
	// Version 3 additionally binds the signature to one agent (the audience).
	HeaderSignatureVersion = "X-Portwing-Signature-Version"
	HeaderReason           = "X-Portwing-Reason"
	// HeaderAudience optionally names the agent ID a version 3 signature was
	// made for. The signature itself is what binds the audience; the header
	// only lets a mismatch be reported as audience-mismatch instead of a bare
	// invalid-signature.
	HeaderAudience    = "X-Portwing-Audience"
	SignatureVersion2 = "2"
	SignatureVersion3 = "3"
)

// signatureV3Domain prefixes every version 3 canonical message so the bytes can
// never be mistaken for a version 2 or legacy message.
const signatureV3Domain = "portwing-sig-v3"

// timeNow is the clock VerifyRequest measures timestamp skew against. Tests pin
// it to a fixed whole second so they can construct a skew that lands exactly on
// a window or warn boundary. The wire timestamp is a whole number of seconds,
// so a skew read off the real wall clock always carries the sub-second
// remainder time.Now().Unix() discarded at signing time, which puts every
// measurement a fraction short of any whole-second target and makes a test that
// aims at one depend on how fast it runs. Production never reassigns it.
var timeNow = time.Now

// Sentinel errors returned by VerifyRequest. Callers can use errors.Is.
var (
	ErrMissingHeaders = errors.New("ed25519: missing required signature headers")
	ErrUnknownKey     = errors.New("ed25519: unknown key ID")
	ErrTimestampSkew  = errors.New("ed25519: timestamp outside allowed window")
	ErrNonceCapacity  = errors.New("ed25519: nonce cache at capacity")
	ErrNonceReplay    = errors.New("ed25519: nonce already seen (replay)")
	ErrBadSignature   = errors.New("ed25519: signature verification failed")
	ErrInvalidNonce   = errors.New("ed25519: nonce must be 32 hex characters")
	ErrInvalidSig     = errors.New("ed25519: signature is not valid base64url")

	ErrAudienceMismatch       = errors.New("ed25519: signature audience does not match this agent")
	ErrSignatureLocked        = errors.New("ed25519: key is locked to signature version 3")
	ErrSignatureVersionTooLow = errors.New("ed25519: signature version below the configured minimum")
)

// SignatureLockedError is returned when a version 2 or legacy signature
// arrives for a key that has already signed a version 3 request.
type SignatureLockedError struct {
	KeyID string
	Since time.Time
}

func (e *SignatureLockedError) Error() string        { return ErrSignatureLocked.Error() }
func (e *SignatureLockedError) Is(target error) bool { return target == ErrSignatureLocked }

// ErrV3Unavailable reports that this agent has no usable v3 audience.
var ErrV3Unavailable = errors.New("ed25519: signature version 3 is unavailable on this agent")

// V3UnavailableError carries the reason v3 is refused. The reason is also the
// X-Portwing-Reason value.
type V3UnavailableError struct {
	Reason string
}

func (e *V3UnavailableError) Error() string        { return ErrV3Unavailable.Error() + ": " + e.Reason }
func (e *V3UnavailableError) Is(target error) bool { return target == ErrV3Unavailable }

// SignatureVersionError is returned when a valid signature uses a version
// below SIGNATURE_MIN_VERSION.
type SignatureVersionError struct {
	Got, Min int
}

func (e *SignatureVersionError) Error() string        { return ErrSignatureVersionTooLow.Error() }
func (e *SignatureVersionError) Is(target error) bool { return target == ErrSignatureVersionTooLow }

// SignaturePolicy is the per-agent signature policy a KeyRegistry enforces.
type SignaturePolicy struct {
	// AgentID is the audience version 3 signatures must be bound to.
	AgentID string
	// MinVersion refuses valid signatures below this version for every key.
	// 0 and 1 accept everything.
	MinVersion int
	// V3Unavailable, when set, refuses version 3 outright with this reason
	// (the agent ID is not v3 compatible or not persistent). Version 2 and
	// legacy are unaffected.
	V3Unavailable string
	// Locks records keys that have signed version 3. nil disables the
	// downgrade lock.
	Locks *SignatureLocks
}

// SetSignaturePolicy installs the signature policy VerifyRequest enforces.
func (r *KeyRegistry) SetSignaturePolicy(p SignaturePolicy) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.policy = p
}

func (r *KeyRegistry) signaturePolicy() SignaturePolicy {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.policy
}

// ReasonFor maps a sentinel error to the value for the X-Portwing-Reason
// header returned with 401 responses.
func ReasonFor(err error) string {
	switch {
	case errors.Is(err, ErrTimestampSkew):
		return "timestamp-skew"
	case errors.Is(err, ErrNonceCapacity):
		return "nonce-capacity"
	case errors.Is(err, ErrNonceReplay):
		return "replay"
	case errors.Is(err, ErrUnknownKey):
		return "unknown-key"
	case errors.Is(err, ErrV3Unavailable):
		var u *V3UnavailableError
		if errors.As(err, &u) {
			return u.Reason
		}
		return "v3-unavailable"
	case errors.Is(err, ErrAudienceMismatch):
		return "audience-mismatch"
	case errors.Is(err, ErrSignatureLocked):
		return "signature-locked"
	case errors.Is(err, ErrSignatureVersionTooLow):
		return "signature-version-too-low"
	default:
		return "invalid-signature"
	}
}

// RejectionDetail returns the 401 response body for a verification error.
// Only the policy rejections (locked, version too low, v3 unavailable), which
// are reachable solely with a valid signature, say more than "unauthorized".
func RejectionDetail(err error) string {
	var locked *SignatureLockedError
	var low *SignatureVersionError
	var unavailable *V3UnavailableError
	switch {
	case errors.As(err, &unavailable):
		switch unavailable.Reason {
		case "agent-id-not-v3-compatible":
			return "signature version 3 is unavailable: AGENT_ID must be 1 to 64 characters from a-z, 0-9 and '-' (or unset it to use a generated ID); v2 and legacy still work"
		case "agent-id-not-persistent":
			return "signature version 3 is unavailable: the agent ID is not persistent; set AGENT_ID or mount a writable volume at /data/credentials (STATE_DIR); v2 and legacy still work"
		case "state-not-writable":
			return "signature version 3 is unavailable: the state directory is not writable, so downgrade locks could not be kept; mount a writable volume at /data/credentials or set STATE_DIR; v2 and legacy still work"
		default:
			return "signature version 3 is unavailable on this agent: " + unavailable.Reason
		}
	case errors.As(err, &locked):
		return fmt.Sprintf("key locked to v3 since %s; clear with portwing signature-lock clear %s",
			locked.Since.UTC().Format(time.RFC3339), locked.KeyID)
	case errors.As(err, &low):
		return fmt.Sprintf("signature version %d is below the minimum %d required by this agent", low.Got, low.Min)
	default:
		return "unauthorized"
	}
}

// HasSignature reports whether the request carries the Portwing signature
// header.
func HasSignature(h http.Header) bool {
	return h.Get(HeaderSignature) != ""
}

// CanonicalMessage constructs the canonical byte string that is signed and
// verified for every authenticated request. In signature version 2, target is
// the complete origin-form request target returned by CanonicalRequestTarget.
// The format is:
//
//	METHOD\nREQUEST-TARGET\nbody-sha256-hex\nunix-timestamp\nnonce
//
// For an empty body, bodyHashHex must be the full SHA-256 of the empty string:
//
//	e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
func CanonicalMessage(method, target, bodyHashHex string, timestampUnix int64, nonce string) []byte {
	return []byte(fmt.Sprintf("%s\n%s\n%s\n%d\n%s",
		method, target, bodyHashHex, timestampUnix, nonce))
}

// CanonicalMessageV3 constructs the version 3 canonical byte string. It is the
// version 2 message prefixed by a fixed domain tag and the audience, the ID of
// the agent the request is for, so a request captured for one agent cannot be
// replayed to another that shares the controller key:
//
//	portwing-sig-v3\nAGENT-ID\nMETHOD\nREQUEST-TARGET\nbody-sha256-hex\nunix-timestamp\nnonce
//
// target is the same query-inclusive form version 2 uses.
func CanonicalMessageV3(agentID, method, target, bodyHashHex string, timestampUnix int64, nonce string) []byte {
	return []byte(fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%d\n%s",
		signatureV3Domain, agentID, method, target, bodyHashHex, timestampUnix, nonce))
}

// CanonicalRequestTarget returns the exact origin-form target covered by a
// version 2 HTTP signature: escaped path plus the unmodified raw query.
func CanonicalRequestTarget(u *url.URL) string {
	target := u.EscapedPath()
	if target == "" {
		target = "/"
	}
	if u.ForceQuery || u.RawQuery != "" {
		target += "?" + u.RawQuery
	}
	return target
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

// VerifyResult describes a successfully verified request.
type VerifyResult struct {
	KeyID string
	// Version is the signature version used: 1 (legacy), 2 or 3.
	Version int
	// NewlyLocked is true when this request locked the key to version 3.
	NewlyLocked bool
	// LockErr is a failure to persist a new lock. The request is still
	// accepted and the lock holds in memory.
	LockErr error
}

// VerifyRequest verifies an incoming HTTP request's Ed25519 signature and
// returns the verified key ID. See VerifyRequestDetailed.
func VerifyRequest(
	r *http.Request,
	body []byte,
	registry *KeyRegistry,
	lru *NonceLRU,
	maxSkewSeconds int,
) (keyID string, err error) {
	res, err := VerifyRequestDetailed(r, body, registry, lru, maxSkewSeconds)
	if err != nil {
		return "", err
	}
	return res.KeyID, nil
}

// VerifyRequestDetailed verifies an incoming HTTP request's Ed25519 signature.
//
// It reads the four Ed25519 headers (X-Portwing-Key-ID, X-Portwing-Timestamp,
// X-Portwing-Nonce, X-Portwing-Signature) and:
//  1. Looks up the key in registry.
//  2. Checks the timestamp against maxSkew.
//  3. Checks the nonce LRU for replay.
//  4. Verifies the Ed25519 signature over the canonical message for the
//     version in X-Portwing-Signature-Version.
//  5. Applies the registry's signature policy (minimum version, downgrade
//     lock). This runs only after the signature verified, so the policy state
//     is never visible to a caller without the key.
//  6. Records the nonce, and for version 3 locks the key.
//
// body is the raw request body (already read by the caller). The caller is
// responsible for replacing r.Body with a new reader if needed.
func VerifyRequestDetailed(
	r *http.Request,
	body []byte,
	registry *KeyRegistry,
	lru *NonceLRU,
	maxSkewSeconds int,
) (VerifyResult, error) {
	sigHeader := r.Header.Get(HeaderSignature)
	if sigHeader == "" {
		return VerifyResult{}, ErrMissingHeaders
	}

	kidHeader := r.Header.Get(HeaderKeyID)
	tsHeader := r.Header.Get(HeaderTimestamp)
	nonceHeader := r.Header.Get(HeaderNonce)

	if kidHeader == "" || tsHeader == "" || nonceHeader == "" {
		return VerifyResult{}, ErrMissingHeaders
	}

	// Validate nonce format: must be exactly 32 hex characters.
	if len(nonceHeader) != 32 {
		return VerifyResult{}, ErrInvalidNonce
	}
	if _, err := hex.DecodeString(nonceHeader); err != nil {
		return VerifyResult{}, ErrInvalidNonce
	}

	// Look up key.
	key, ok := registry.LookupByID(kidHeader)
	if !ok {
		return VerifyResult{}, ErrUnknownKey
	}

	// Parse and check timestamp.
	tsUnix, err := strconv.ParseInt(tsHeader, 10, 64)
	if err != nil {
		return VerifyResult{}, ErrTimestampSkew
	}
	skew := timeNow().Sub(time.Unix(tsUnix, 0))
	maxSkew := time.Duration(maxSkewSeconds) * time.Second
	if skew < -maxSkew || skew > maxSkew {
		return VerifyResult{}, ErrTimestampSkew
	}
	if skew < 0 {
		skew = -skew
	}

	// Warn on large but still-acceptable skew.
	if skew > 30*time.Second {
		slog.Warn("clock skew warning",
			"skew_seconds", int(skew.Seconds()),
			"key_id", applog.Sanitize(kidHeader))
	}

	// Replay check.
	if lru.Seen(nonceHeader) {
		return VerifyResult{}, ErrNonceReplay
	}

	// Decode signature (base64url, no padding).
	sig, err := base64.RawURLEncoding.DecodeString(sigHeader)
	if err != nil {
		return VerifyResult{}, ErrInvalidSig
	}

	// Build the canonical message. Legacy signatures without a version header
	// remain valid only for query-free requests; accepting them with a query
	// would preserve the query-tampering vulnerability fixed by version 2.
	policy := registry.signaturePolicy()
	bodyHash := BodyHashHex(body)
	var (
		version  int
		msg      []byte
		audience string
	)
	switch r.Header.Get(HeaderSignatureVersion) {
	case SignatureVersion3:
		version = 3
		// The signature is checked against the audience the caller names, or
		// this agent's ID when it names none. Every v3-specific rejection
		// (unavailable, no ID, wrong audience) is decided only after the
		// signature verifies, so a caller without the key gets
		// invalid-signature and learns nothing about the agent's ID or state.
		audience = policy.AgentID
		if aud := r.Header.Get(HeaderAudience); aud != "" {
			audience = aud
		}
		msg = CanonicalMessageV3(audience, r.Method, CanonicalRequestTarget(r.URL), bodyHash, tsUnix, nonceHeader)
	case SignatureVersion2:
		version = 2
		msg = CanonicalMessage(r.Method, CanonicalRequestTarget(r.URL), bodyHash, tsUnix, nonceHeader)
	case "":
		version = 1
		if r.URL.RawQuery != "" || r.URL.ForceQuery {
			return VerifyResult{}, ErrBadSignature
		}
		msg = CanonicalMessage(r.Method, r.URL.Path, bodyHash, tsUnix, nonceHeader)
	default:
		return VerifyResult{}, ErrBadSignature
	}

	// Verify signature.
	if !ed25519.Verify(ed25519.PublicKey(key.PubKey), msg, sig) {
		return VerifyResult{}, ErrBadSignature
	}

	// Policy, after the signature so a caller without the key learns nothing
	// about which keys are locked, what the minimum is, or this agent's v3
	// state. None of these rejections records the nonce.
	if version == 3 {
		if policy.V3Unavailable != "" {
			return VerifyResult{}, &V3UnavailableError{Reason: policy.V3Unavailable}
		}
		if policy.AgentID == "" || audience != policy.AgentID {
			return VerifyResult{}, ErrAudienceMismatch
		}
	}
	if policy.MinVersion > version {
		return VerifyResult{}, &SignatureVersionError{Got: version, Min: policy.MinVersion}
	}
	if version < 3 && policy.Locks != nil {
		if since, locked := policy.Locks.LockedSince(kidHeader); locked {
			return VerifyResult{}, &SignatureLockedError{KeyID: kidHeader, Since: since}
		}
	}

	// Record the nonce after successful verification. Add is the
	// authoritative atomic check-and-set: if two copies of the same request
	// race past the Seen() pre-check above, only one wins here.
	if err := lru.Add(nonceHeader); err != nil {
		return VerifyResult{}, err
	}

	res := VerifyResult{KeyID: kidHeader, Version: version}
	if version == 3 && policy.Locks != nil {
		res.NewlyLocked, res.LockErr = policy.Locks.Lock(kidHeader, timeNow())
	}
	return res, nil
}
