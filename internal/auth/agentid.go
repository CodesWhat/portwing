// SPDX-License-Identifier: MIT

package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// AgentIDFileName is the file under the state directory that holds the
// persisted agent ID.
const AgentIDFileName = "agent-id"

// MaxAgentIDLength is the longest agent ID Portwing accepts or generates.
const MaxAgentIDLength = 64

// ErrStateUnavailable reports that the state directory could not be prepared
// or written. LoadOrCreateAgentID still returns a usable (unpersisted) ID next
// to it, so callers can warn and continue.
var ErrStateUnavailable = errors.New("state directory unavailable")

// ValidateAgentID enforces the agent ID rule shared with Drydock: 1 to 64
// characters, each a lowercase ASCII letter, a digit or a hyphen.
func ValidateAgentID(id string) error {
	if id == "" || len(id) > MaxAgentIDLength {
		return fmt.Errorf("agent ID must be 1 to %d characters, got %d", MaxAgentIDLength, len(id))
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return fmt.Errorf("agent ID may contain only a-z, 0-9 and '-'")
		}
	}
	return nil
}

// randRead is the entropy source for generated agent IDs; tests replace it to
// reach the failure path.
var randRead = rand.Read

// GenerateAgentID returns a fresh random agent ID: 16 random bytes as 32
// lowercase hex characters.
func GenerateAgentID() (string, error) {
	var b [16]byte
	if _, err := randRead(b[:]); err != nil {
		return "", fmt.Errorf("generating agent ID: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// PrepareStateDirectory creates dir (mode 0700) when missing and checks it is a
// real directory that other users cannot write to. Unlike the key rotation
// directory it does not insist on exactly 0700: a Kubernetes fsGroup volume is
// group-writable, and the state held here (agent ID, signature locks) is not
// secret. Group write is accepted, world write is not.
func PrepareStateDirectory(dir string) error {
	if dir == "" {
		return fmt.Errorf("state directory must not be empty")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("checking state directory: %w", err)
	}
	if info.Mode().Perm()&0o002 != 0 {
		return fmt.Errorf("state directory %q is world-writable (mode %04o)", dir, info.Mode().Perm())
	}
	return nil
}

// CheckStateWritable reports whether dir can hold durable state: it must pass
// PrepareStateDirectory and accept a file (written and removed here).
func CheckStateWritable(dir string) error {
	if err := PrepareStateDirectory(dir); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".probe-")
	if err != nil {
		return fmt.Errorf("state directory %q is not writable: %w", dir, err)
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}

// LoadOrCreateAgentID returns the agent ID persisted in dir, creating and
// durably writing a new one on first start. A file that exists but is
// unreadable, over-permissioned or holds an invalid ID is a hard error: silently
// minting a new ID would change the audience every signature is bound to.
// When the directory itself cannot be prepared or written, the error wraps
// ErrStateUnavailable and a fresh unpersisted ID is returned alongside it.
func LoadOrCreateAgentID(dir string) (string, error) {
	path := filepath.Join(dir, AgentIDFileName)
	f, err := openCredentialFile(path, "agent ID")
	if err == nil {
		defer f.Close()
		data, readErr := readRotationState(io.LimitReader(f, MaxAgentIDLength+2))
		if readErr != nil {
			return "", fmt.Errorf("reading agent ID: %w", readErr)
		}
		id := strings.TrimSpace(string(data))
		if err := ValidateAgentID(id); err != nil {
			return "", fmt.Errorf("agent ID file %q: %w", path, err)
		}
		return id, nil
	}
	// A file that exists but fails the open checks (permissions, not regular)
	// is fatal. A path that cannot even be stat'd (missing file, or a parent
	// that is not a directory or not searchable) means there is nothing
	// persisted to protect, so fall through to creating one.
	if _, statErr := os.Lstat(path); statErr == nil {
		return "", err
	}

	id, genErr := GenerateAgentID()
	if genErr != nil {
		return "", genErr
	}
	if err := PrepareStateDirectory(dir); err != nil {
		return id, fmt.Errorf("%w: %w", ErrStateUnavailable, err)
	}
	if err := WriteStateFile(path, []byte(id+"\n")); err != nil {
		return id, fmt.Errorf("%w: %w", ErrStateUnavailable, err)
	}
	return id, nil
}
