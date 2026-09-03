package auth

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

// FuzzParseKeyLine verifies that parseKeyLine never panics on arbitrary
// input and that any successfully-parsed line produces a well-formed
// AuthorizedKey: KeyID is always 16 hex characters and PubKey is always
// exactly 32 bytes. parseAuthorizedKeys (keys.go:97-138) re-parses every
// non-blank, non-comment line through parseKeyLine on process start,
// SIGHUP, and after enrollment (enroll.go:154), so a malformed line must
// fail closed rather than panic or produce a malformed key.
func FuzzParseKeyLine(f *testing.F) {
	privateKey := ed25519.NewKeyFromSeed([]byte("portwing-auth-fuzz-keyline-seed1"))
	pubB64 := base64.StdEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey))

	// Seed: valid lines (mirrors keys_test.go's table cases).
	f.Add("ed25519 " + pubB64 + " mycomment")
	f.Add("ed25519 " + pubB64)
	f.Add("ed25519 " + pubB64 + " platform:drydock:prod:2026-06-11")

	// Seed: blank lines and comments. parseAuthorizedKeys filters these out
	// before calling parseKeyLine, but parseKeyLine itself must not panic
	// if handed one directly.
	f.Add("")
	f.Add("   ")
	f.Add("# a comment")

	// Seed: hostile inputs — wrong algorithm, too few fields, truncated or
	// invalid base64, wrong decoded length, embedded whitespace/tabs, and
	// an oversized comment.
	f.Add("ed25519")
	f.Add("rsa " + pubB64)
	f.Add("ed25519 not-valid-base64!!!")
	f.Add("ed25519 " + base64.StdEncoding.EncodeToString(make([]byte, 10)))
	f.Add("ed25519 " + pubB64[:len(pubB64)-4])
	f.Add("ed25519\t" + pubB64 + "\tcomment\twith\ttabs")
	f.Add("ed25519 " + pubB64 + " " + strings.Repeat("x", 4096))
	f.Add(strings.Repeat("a", 8192))

	f.Fuzz(func(t *testing.T, line string) {
		key, err := parseKeyLine(line)
		if err != nil {
			// Parse error is expected for most fuzz inputs; not a failure.
			return
		}

		if len(key.KeyID) != 16 {
			t.Fatalf("parsed key has KeyID length %d, want 16: %q", len(key.KeyID), key.KeyID)
		}
		if _, err := hex.DecodeString(key.KeyID); err != nil {
			t.Fatalf("parsed key has non-hex KeyID %q: %v", key.KeyID, err)
		}
		if len(key.PubKey) != 32 {
			t.Fatalf("parsed key has PubKey length %d, want 32", len(key.PubKey))
		}
	})
}
