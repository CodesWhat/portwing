package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"

	"golang.org/x/crypto/argon2"
)

// Argon2idParams holds the Argon2id parameters encoded in a PHC string.
type Argon2idParams struct {
	Memory      uint32
	Time        uint32
	Parallelism uint8
	Salt        []byte
	Hash        []byte
}

// ParsePHC parses an Argon2id PHC string of the form:
//
//	$argon2id$v=19$m=<mem>,t=<time>,p=<par>$<base64salt>$<base64hash>
//
// It returns an error for any malformed input.
func ParsePHC(phc string) (*Argon2idParams, error) {
	// Must start with $argon2id$
	if !strings.HasPrefix(phc, "$argon2id$") {
		return nil, fmt.Errorf("argon2id: unsupported algorithm or malformed PHC string")
	}

	// Split on '$', skipping the leading empty field.
	// Expected parts after split on '$':
	//   [0]="" [1]="argon2id" [2]="v=19" [3]="m=...,t=...,p=..." [4]="<salt>" [5]="<hash>"
	parts := strings.Split(phc, "$")
	if len(parts) != 6 {
		return nil, fmt.Errorf("argon2id: expected 6 $ segments, got %d", len(parts))
	}

	if parts[1] != "argon2id" {
		return nil, fmt.Errorf("argon2id: algorithm must be argon2id, got %q", parts[1])
	}

	// Parse version.
	if parts[2] != "v=19" {
		return nil, fmt.Errorf("argon2id: version must be v=19, got %q", parts[2])
	}

	// Parse parameters.
	p, err := parseParams(parts[3])
	if err != nil {
		return nil, err
	}

	// Decode salt.
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return nil, fmt.Errorf("argon2id: invalid base64 salt: %w", err)
	}
	if len(salt) == 0 {
		return nil, fmt.Errorf("argon2id: salt must not be empty")
	}

	// Decode hash.
	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return nil, fmt.Errorf("argon2id: invalid base64 hash: %w", err)
	}
	if len(hash) == 0 {
		return nil, fmt.Errorf("argon2id: hash must not be empty")
	}

	p.Salt = salt
	p.Hash = hash
	return p, nil
}

func parseParams(s string) (*Argon2idParams, error) {
	// Format: m=<mem>,t=<time>,p=<par>
	fields := strings.Split(s, ",")
	if len(fields) != 3 {
		return nil, fmt.Errorf("argon2id: expected 3 parameter fields, got %d", len(fields))
	}

	p := &Argon2idParams{}
	for _, f := range fields {
		kv := strings.SplitN(f, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("argon2id: malformed parameter %q", f)
		}
		v, err := strconv.ParseUint(kv[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("argon2id: parameter %q value %q is not a valid integer", kv[0], kv[1])
		}
		switch kv[0] {
		case "m":
			p.Memory = uint32(v)
		case "t":
			p.Time = uint32(v)
		case "p":
			if v > 255 {
				return nil, fmt.Errorf("argon2id: parallelism %d exceeds maximum 255", v)
			}
			p.Parallelism = uint8(v)
		default:
			return nil, fmt.Errorf("argon2id: unknown parameter %q", kv[0])
		}
	}

	// argon2.IDKey panics on parameters below its minimums; reject them at
	// parse time so a malformed PHC fails at startup, not per request.
	if p.Time < 1 {
		return nil, fmt.Errorf("argon2id: time parameter must be >= 1")
	}
	if p.Parallelism < 1 {
		return nil, fmt.Errorf("argon2id: parallelism parameter must be >= 1")
	}
	if p.Memory < 8*uint32(p.Parallelism) {
		return nil, fmt.Errorf("argon2id: memory parameter must be >= 8*parallelism KiB")
	}
	return p, nil
}

// Verify returns true if password matches the Argon2id hash encoded in p.
// The comparison is constant-time.
func (p *Argon2idParams) Verify(password string) bool {
	derived := argon2.IDKey([]byte(password), p.Salt, p.Time, p.Memory, p.Parallelism, uint32(len(p.Hash)))
	return subtle.ConstantTimeCompare(derived, p.Hash) == 1
}

// HashToken generates an Argon2id PHC string for password using OWASP-recommended
// parameters: m=19456 KiB, t=2, p=1, 16-byte random salt, 32-byte key.
func HashToken(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generating salt: %w", err)
	}
	const (
		memory  uint32 = 19456
		time    uint32 = 2
		threads uint8  = 1
		keyLen  uint32 = 32
	)
	hash := argon2.IDKey([]byte(password), salt, time, memory, threads, keyLen)
	phc := fmt.Sprintf(
		"$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		memory, time, threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	)
	return phc, nil
}

// argon2Verifier wraps a parsed PHC and caches the SHA-256 of the first
// successfully verified token to avoid full Argon2id on every request.
type argon2Verifier struct {
	params    *Argon2idParams
	cacheOnce atomic.Pointer[[sha256.Size]byte]
}

func newArgon2Verifier(params *Argon2idParams) *argon2Verifier {
	return &argon2Verifier{params: params}
}

// Verify returns true if the presented token matches the stored hash.
// After the first successful verification the SHA-256 of the token is cached;
// subsequent calls compare only the SHA-256 sum, keeping per-request cost flat.
// Failed attempts always run through the rate limiter before this function is
// called, so the cache does not weaken the brute-force protection path.
func (v *argon2Verifier) Verify(token string) bool {
	sum := sha256.Sum256([]byte(token))

	// Fast path: compare against cached sum.
	if cached := v.cacheOnce.Load(); cached != nil {
		return subtle.ConstantTimeCompare(sum[:], cached[:]) == 1
	}

	// Slow path: full Argon2id derivation.
	if !v.params.Verify(token) {
		return false
	}

	// Cache the sum on first success; ignore races — both goroutines would
	// cache the same value for the same correct token.
	v.cacheOnce.Store(&sum)
	return true
}
