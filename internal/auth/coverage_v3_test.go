// SPDX-License-Identifier: MIT

package auth

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests swap package hooks, so none of them run in parallel.

func TestGenerateAgentID_EntropyFailure(t *testing.T) {
	real := randRead
	randRead = func([]byte) (int, error) { return 0, errors.New("no entropy") }
	t.Cleanup(func() { randRead = real })
	if id, err := GenerateAgentID(); err == nil || id != "" {
		t.Fatalf("GenerateAgentID = %q, %v; want an error", id, err)
	}
	// A missing agent-id file with no entropy is fatal, not a silent fallback.
	if id, err := LoadOrCreateAgentID(t.TempDir()); err == nil || errors.Is(err, ErrStateUnavailable) || id != "" {
		t.Fatalf("LoadOrCreateAgentID = %q, %v; want a fatal error", id, err)
	}
}

func TestLoadOrCreateAgentID_ReadFailureIsFatal(t *testing.T) {
	dir := t.TempDir()
	// #nosec G302 -- private test fixture.
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, AgentIDFileName), []byte("abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	real := readRotationState
	readRotationState = func(io.Reader) ([]byte, error) { return nil, errors.New("disk error") }
	t.Cleanup(func() { readRotationState = real })
	if id, err := LoadOrCreateAgentID(dir); err == nil || errors.Is(err, ErrStateUnavailable) || id != "" {
		t.Fatalf("LoadOrCreateAgentID = %q, %v", id, err)
	}
}

func TestLoadOrCreateAgentID_WriteFailureFallsBack(t *testing.T) {
	dir := t.TempDir()
	// #nosec G302 -- read-only private test fixture.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// #nosec G302 -- restore owner write permission for cleanup.
		_ = os.Chmod(dir, 0o700)
	})
	if f, err := os.CreateTemp(dir, "probe"); err == nil {
		_ = f.Close()
		t.Skip("directory permissions are not enforced here")
	}
	id, err := LoadOrCreateAgentID(dir)
	if !errors.Is(err, ErrStateUnavailable) || ValidateAgentID(id) != nil {
		t.Fatalf("LoadOrCreateAgentID = %q, %v; want a usable ID with ErrStateUnavailable", id, err)
	}
}

func TestSignatureLocks_ReadFailureAndOversizeAreErrors(t *testing.T) {
	dir := t.TempDir()
	// #nosec G302 -- private test fixture.
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, SignatureLocksFileName)
	if err := os.WriteFile(path, []byte(`{"version":1,"locks":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	real := readRotationState
	readRotationState = func(io.Reader) ([]byte, error) { return nil, errors.New("disk error") }
	if _, err := OpenSignatureLocks(dir); err == nil || !strings.Contains(err.Error(), "reading signature locks") {
		t.Fatalf("read failure: %v", err)
	}
	readRotationState = real
	t.Cleanup(func() { readRotationState = real })

	big := `{"version":1,"locks":{},"pad":"` + strings.Repeat("a", maxSignatureLocksBytes) + `"}`
	if err := os.WriteFile(path, []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSignatureLocks(dir); err == nil || !strings.Contains(err.Error(), "exceed") {
		t.Fatalf("oversize: %v", err)
	}
}

func TestSignatureLocks_ListIsSortedByKeyID(t *testing.T) {
	t.Parallel()
	locks := NewMemorySignatureLocks()
	for _, id := range []string{"cccccccccccccccc", "aaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbb"} {
		if _, err := locks.Lock(id, lockTestTime); err != nil {
			t.Fatal(err)
		}
	}
	list := locks.List()
	if len(list) != 3 || list[0].KeyID != "aaaaaaaaaaaaaaaa" || list[1].KeyID != "bbbbbbbbbbbbbbbb" || list[2].KeyID != "cccccccccccccccc" {
		t.Fatalf("List = %+v", list)
	}
}

func TestWriteStateFile_FailureStagesLeaveOldStateAndNoTempFiles(t *testing.T) {
	originalCreate := createRotationStateFile
	t.Cleanup(func() { createRotationStateFile = originalCreate })
	for _, stage := range []string{"write", "sync", "close"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "state.json")
			if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			createRotationStateFile = func(dir, pattern string) (rotationStateFile, error) {
				f, err := os.CreateTemp(dir, pattern)
				return failingRotationFile{f, stage}, err
			}
			if err := WriteStateFile(target, []byte("replacement")); err == nil {
				t.Fatal("failure reported success")
			}
			if data, _ := os.ReadFile(target); string(data) != "original" {
				t.Fatalf("old state lost: %q", data)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 1 {
				t.Fatalf("temporary file leaked: %v", entries)
			}
		})
	}
}

func TestWriteStateFile_RenameFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "state.json")
	// A non-empty directory at the target makes the final rename fail.
	if err := os.MkdirAll(filepath.Join(target, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteStateFile(target, []byte("x")); err == nil || !strings.Contains(err.Error(), "replacing state file") {
		t.Fatalf("err = %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("temporary file leaked: %v", entries)
	}
}

func TestErrorTypesDescribeThemselves(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&SignatureLockedError{KeyID: "k"}, ErrSignatureLocked.Error()},
		{&SignatureVersionError{Got: 2, Min: 3}, ErrSignatureVersionTooLow.Error()},
		{&V3UnavailableError{Reason: "state-not-writable"}, ErrV3Unavailable.Error() + ": state-not-writable"},
	} {
		if tc.err.Error() != tc.want {
			t.Errorf("Error() = %q, want %q", tc.err.Error(), tc.want)
		}
	}
}

func TestReasonAndDetailForEveryV3UnavailableReason(t *testing.T) {
	t.Parallel()
	// A bare sentinel carries no reason.
	if ReasonFor(ErrV3Unavailable) != "v3-unavailable" {
		t.Fatalf("bare sentinel reason = %q", ReasonFor(ErrV3Unavailable))
	}
	for reason, hint := range map[string]string{
		"agent-id-not-v3-compatible": "AGENT_ID must be",
		"agent-id-not-persistent":    "not persistent",
		"state-not-writable":         "not writable",
	} {
		err := &V3UnavailableError{Reason: reason}
		if ReasonFor(err) != reason || !strings.Contains(RejectionDetail(err), hint) {
			t.Errorf("%s: reason %q detail %q", reason, ReasonFor(err), RejectionDetail(err))
		}
	}
}

func TestSignatureLocks_LockedKeyTimestampTruncatedToSecond(t *testing.T) {
	t.Parallel()
	locks := NewMemorySignatureLocks()
	at := time.Date(2026, 11, 2, 12, 30, 0, 999, time.UTC)
	if _, err := locks.Lock("aaaaaaaaaaaaaaaa", at); err != nil {
		t.Fatal(err)
	}
	if since, _ := locks.LockedSince("aaaaaaaaaaaaaaaa"); since.Nanosecond() != 0 {
		t.Fatalf("since = %v, want whole seconds", since)
	}
}
