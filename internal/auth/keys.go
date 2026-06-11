// Package auth implements Ed25519 per-client key authentication for Lookout.
// It provides a key registry loaded from an authorized_keys file (Model B),
// a nonce LRU for replay protection, and request verification helpers.
package auth

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"
)

// AuthorizedKey represents a single entry in the authorized_keys file.
type AuthorizedKey struct {
	// KeyID is hex(SHA-256(raw 32-byte pubkey)[:8]).
	KeyID string
	// PubKey is the raw 32-byte Ed25519 public key.
	PubKey []byte
	// Comment is the optional comment text from the authorized_keys line.
	Comment string
}

// KeyRegistry holds the in-memory map of authorized Ed25519 public keys,
// loaded from an authorized_keys file. It is safe for concurrent use.
type KeyRegistry struct {
	mu       sync.RWMutex
	keys     map[string]*AuthorizedKey // keyed by KeyID
	filePath string
}

// NewKeyRegistry creates a KeyRegistry that will load keys from filePath.
// Call Load to populate it. filePath may be empty, in which case the registry
// is permanently empty (no ed25519 auth configured).
func NewKeyRegistry(filePath string) *KeyRegistry {
	return &KeyRegistry{
		keys:     make(map[string]*AuthorizedKey),
		filePath: filePath,
	}
}

// Load reads the authorized_keys file and replaces the in-memory key map.
// It logs keys that were added or removed relative to the previous load.
// If filePath is empty, Load is a no-op (returns nil).
func (r *KeyRegistry) Load() error {
	if r.filePath == "" {
		return nil
	}

	newKeys, err := parseAuthorizedKeys(r.filePath)
	if err != nil {
		return err
	}

	r.mu.Lock()
	old := r.keys
	r.keys = newKeys
	r.mu.Unlock()

	// Log additions/removals.
	for id, k := range newKeys {
		if _, existed := old[id]; !existed {
			slog.Info("ed25519 key added", "key_id", id, "comment", k.Comment)
		}
	}
	for id, k := range old {
		if _, exists := newKeys[id]; !exists {
			slog.Info("ed25519 key removed", "key_id", id, "comment", k.Comment)
		}
	}

	return nil
}

// LookupByID returns the AuthorizedKey for the given key ID, or (nil, false)
// if no such key is registered.
func (r *KeyRegistry) LookupByID(keyID string) (*AuthorizedKey, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	k, ok := r.keys[keyID]
	return k, ok
}

// Len returns the number of registered keys.
func (r *KeyRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.keys)
}

// parseAuthorizedKeys reads an authorized_keys file and returns the parsed
// key map. It refuses to load a world-readable file on Unix systems.
func parseAuthorizedKeys(path string) (map[string]*AuthorizedKey, error) {
	if err := checkFilePermissions(path); err != nil {
		return nil, err
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening authorized_keys %q: %w", path, err)
	}
	defer f.Close()

	keys := make(map[string]*AuthorizedKey)
	scanner := bufio.NewScanner(f)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Skip blank lines and comments.
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, err := parseKeyLine(line)
		if err != nil {
			slog.Warn("authorized_keys: skipping malformed line",
				"file", path, "line", lineNum, "error", err)
			continue
		}

		if _, dup := keys[key.KeyID]; dup {
			slog.Warn("authorized_keys: duplicate key ID, keeping first occurrence",
				"file", path, "line", lineNum, "key_id", key.KeyID)
			continue
		}

		keys[key.KeyID] = key
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading authorized_keys %q: %w", path, err)
	}

	return keys, nil
}

// parseKeyLine parses a single non-blank, non-comment line from an
// authorized_keys file. Format: "ed25519 <base64-pubkey> [comment]"
func parseKeyLine(line string) (*AuthorizedKey, error) {
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return nil, fmt.Errorf("too few fields")
	}

	algo := parts[0]
	if algo != "ed25519" {
		return nil, fmt.Errorf("unsupported algorithm %q (only ed25519 is accepted)", algo)
	}

	raw, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("base64 decode failed: %w", err)
	}

	if len(raw) != 32 {
		return nil, fmt.Errorf("invalid key length: got %d bytes, want 32", len(raw))
	}

	comment := ""
	if len(parts) > 2 {
		comment = strings.Join(parts[2:], " ")
	}

	keyID := deriveKeyID(raw)

	return &AuthorizedKey{
		KeyID:   keyID,
		PubKey:  raw,
		Comment: comment,
	}, nil
}

// deriveKeyID computes hex(SHA-256(raw 32-byte pubkey)[:8]).
func deriveKeyID(pubKey []byte) string {
	h := sha256.Sum256(pubKey)
	return hex.EncodeToString(h[:8])
}

// checkFilePermissions refuses to load a world-readable authorized_keys file
// on Unix systems (mode & 0044 != 0). On non-Unix platforms it is a no-op.
func checkFilePermissions(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat authorized_keys %q: %w", path, err)
	}
	mode := info.Mode()
	// Refuse if any other (world) read bit is set: mode & 0004.
	// Group read (0040) is allowed so 0640 is fine; world read (0004) is not.
	if mode&0o004 != 0 {
		return fmt.Errorf(
			"authorized_keys file %q is world-readable (mode %04o): "+
				"restrict permissions to 0600 or 0640 before loading",
			path, mode.Perm(),
		)
	}
	return nil
}
