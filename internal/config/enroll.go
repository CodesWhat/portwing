// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/codeswhat/portwing/internal/auth"
)

// Enrollment code bounds from the PAR-ENROLL contract: 20 to 128 characters
// from [A-Za-z0-9_-].
const (
	enrollCodeMinLen = 20
	enrollCodeMaxLen = 128
)

// loadEnrollCode reads ENROLL_CODE or ENROLL_CODE_FILE. It returns "" when
// neither is set. The code is a credential: it is never put in an error.
func loadEnrollCode() (string, error) {
	code := getEnv("ENROLL_CODE", "")
	file := getEnv("ENROLL_CODE_FILE", "")
	if code != "" && file != "" {
		return "", fmt.Errorf("ENROLL_CODE and ENROLL_CODE_FILE are mutually exclusive: choose one")
	}
	if file != "" {
		c, err := loadTokenFile(file)
		if err != nil {
			return "", fmt.Errorf("reading ENROLL_CODE_FILE: %w", err)
		}
		code = c
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return "", nil
	}
	if len(code) < enrollCodeMinLen || len(code) > enrollCodeMaxLen {
		return "", fmt.Errorf("the enrollment code must be %d to %d characters", enrollCodeMinLen, enrollCodeMaxLen)
	}
	for _, r := range code {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return "", fmt.Errorf("the enrollment code may only contain letters, digits, '_' and '-'")
		}
	}
	return code, nil
}

// enrolledKey is the outcome of first-start enrollment key resolution.
type enrolledKey struct {
	path   string
	marker string // set while the controller has not confirmed the key
	code   string // set only while enrolling
}

// resolveEnrolledKey finds or creates the enrolled Edge key. It returns a zero
// value when there is nothing to use (no persisted key and no code), leaving
// the caller's existing "requires PRIVATE_KEY_FILE" error in place.
//
//   - confirmed key: used, the code is ignored.
//   - pending key: reused; the code, when configured, is sent again.
//   - no key and a code: a key is generated and persisted.
func resolveEnrolledKey(stateDir string, explicit bool, code string) (enrolledKey, error) {
	if !explicit && code != "" {
		if info, err := os.Stat(stateDir); err != nil || !info.IsDir() {
			return enrolledKey{}, fmt.Errorf("enrollment needs a writable state directory: %s does not exist; mount a volume at /data/credentials or set STATE_DIR", stateDir)
		}
	}
	key, err := auth.LoadOrCreateEnrolledKey(stateDir, code != "")
	if errors.Is(err, auth.ErrNoEnrolledKey) {
		return enrolledKey{}, nil
	}
	if err != nil {
		return enrolledKey{}, fmt.Errorf("enrolled Edge key: %w", err)
	}
	out := enrolledKey{path: key.Path}
	if key.Pending {
		out.marker = key.MarkerPath
		out.code = code
	}
	return out, nil
}

// hasConfirmedEnrolledKey reports whether dir holds a valid, confirmed
// (non-pending) enrolled key. It never creates anything; any problem reading
// the key counts as "not confirmed" so resolveEnrolledKey reports it.
func hasConfirmedEnrolledKey(dir string) bool {
	key, err := auth.LoadOrCreateEnrolledKey(dir, false)
	return err == nil && !key.Pending
}
