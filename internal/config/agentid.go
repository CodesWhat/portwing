// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/codeswhat/portwing/internal/auth"
)

// generateAgentID is replaced by tests to reach the entropy failure path.
var generateAgentID = auth.GenerateAgentID

// DefaultStateDir is where the container images pre-create a private (0700)
// directory for managed credentials. It is used only when it already exists.
const DefaultStateDir = "/data/credentials"

// ResolveStateDir returns the directory for Portwing's persistent state and
// whether the operator chose it explicitly. STATE_DIR wins, then
// KEY_ROTATION_DIR (so one mounted volume serves both), then DefaultStateDir.
func ResolveStateDir() (dir string, explicit bool) {
	return resolveStateDir(getEnv("KEY_ROTATION_DIR", ""))
}

func resolveStateDir(keyRotationDir string) (string, bool) {
	if dir := getEnv("STATE_DIR", ""); dir != "" {
		return dir, true
	}
	if keyRotationDir != "" {
		return keyRotationDir, true
	}
	return DefaultStateDir, false
}

// V3 unavailability reasons. They appear verbatim in X-Portwing-Reason and in
// the v3Reason field of /_portwing/info.
const (
	ReasonAgentIDNotV3Compatible = "agent-id-not-v3-compatible"
	ReasonAgentIDNotPersistent   = "agent-id-not-persistent"
	ReasonStateNotWritable       = "state-not-writable"
)

// logV3Unavailable reports why v3 is off. It is a WARN only when the operator
// requires v3 (SIGNATURE_MIN_VERSION=3); otherwise v2 and legacy deployments,
// edge and token-only included, are working as configured and it is INFO.
func logV3Unavailable(minVersion int, msg string, args ...any) {
	if minVersion >= 3 {
		slog.Warn(msg+": SIGNATURE_MIN_VERSION=3 is set, so every request will be refused", args...)
		return
	}
	slog.Info(msg, args...)
}

// resolveAgentID returns the agent ID and, when signature version 3 cannot be
// offered on it, the reason why. v2 and legacy signatures are unaffected.
//
//   - AGENT_ID set and valid: used as is, never persisted. v3 also needs a
//     writable state directory so the downgrade locks are durable
//     (state-not-writable otherwise).
//   - AGENT_ID set but not matching ^[a-z0-9-]{1,64}$: still accepted, as
//     every earlier release did, but v3 is refused.
//   - Otherwise the ID persisted in the state directory, generated on first
//     start. With no usable directory the ID is regenerated on every start, so
//     v3 is refused rather than served on an identity nothing can pin.
func resolveAgentID(stateDir string, explicit bool, minVersion int) (id, v3Reason string, err error) {
	if id := getEnv("AGENT_ID", ""); id != "" {
		if err := auth.ValidateAgentID(id); err != nil {
			logV3Unavailable(minVersion, "AGENT_ID is not v3 compatible: signature version 3 is disabled and v2 and legacy keep working; use 1 to 64 characters from a-z, 0-9 and '-' (^[a-z0-9-]{1,64}$), or unset AGENT_ID to use a generated ID",
				"reason", ReasonAgentIDNotV3Compatible)
			return id, ReasonAgentIDNotV3Compatible, nil
		}
		if err := checkLockStateDir(stateDir, explicit); err != nil {
			logV3Unavailable(minVersion, "the state directory is not writable, so signature version 3 is disabled (its downgrade locks could not survive a restart) and v2 and legacy keep working; mount a writable volume at /data/credentials or set STATE_DIR",
				"reason", ReasonStateNotWritable, "detail", err.Error())
			return id, ReasonStateNotWritable, nil
		}
		return id, "", nil
	}
	if !explicit {
		info, err := os.Stat(stateDir)
		if err != nil || !info.IsDir() {
			return ephemeralAgentID(minVersion, fmt.Sprintf("%s does not exist", stateDir))
		}
	}
	id, err = auth.LoadOrCreateAgentID(stateDir)
	if err != nil {
		if errors.Is(err, auth.ErrStateUnavailable) && id != "" {
			return ephemeralAgentID(minVersion, err.Error())
		}
		return "", "", fmt.Errorf("loading agent ID: %w", err)
	}
	// An existing ID can sit in a read-only directory; the locks still need
	// to be writable.
	if err := checkLockStateDir(stateDir, explicit); err != nil {
		logV3Unavailable(minVersion, "the state directory is not writable, so signature version 3 is disabled (its downgrade locks could not survive a restart) and v2 and legacy keep working; mount a writable volume at /data/credentials or set STATE_DIR",
			"reason", ReasonStateNotWritable, "detail", err.Error())
		return id, ReasonStateNotWritable, nil
	}
	return id, "", nil
}

// checkLockStateDir verifies the state directory can hold the signature
// locks. An implicit default that does not exist counts as not writable.
func checkLockStateDir(stateDir string, explicit bool) error {
	if !explicit {
		info, err := os.Stat(stateDir)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("%s does not exist", stateDir)
		}
	}
	return auth.CheckStateWritable(stateDir)
}

func ephemeralAgentID(minVersion int, why string) (string, string, error) {
	id, err := generateAgentID()
	if err != nil {
		return "", "", err
	}
	logV3Unavailable(minVersion, "no persistent state directory and AGENT_ID is not set: the generated agent ID changes on every restart, so signature version 3 is disabled and v2 and legacy keep working; set AGENT_ID or mount a writable volume at /data/credentials (STATE_DIR)",
		"reason", ReasonAgentIDNotPersistent, "detail", why)
	return id, ReasonAgentIDNotPersistent, nil
}

// parseSignatureMinVersion validates SIGNATURE_MIN_VERSION. Unset keeps the
// backward-compatible default of 1 (legacy signatures accepted).
func parseSignatureMinVersion(raw string) (int, error) {
	switch raw {
	case "", "1":
		return 1, nil
	case "2":
		return 2, nil
	case "3":
		return 3, nil
	default:
		return 0, fmt.Errorf("SIGNATURE_MIN_VERSION must be 1, 2 or 3, got %q", raw)
	}
}
