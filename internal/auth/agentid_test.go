// SPDX-License-Identifier: MIT

package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateAgentID(t *testing.T) {
	t.Parallel()
	valid := []string{"a", "z", "0", "-", "agent-1", strings.Repeat("a", 64), "0123456789abcdef0123456789abcdef", "550e8400-e29b-41d4-a716-446655440000"}
	for _, id := range valid {
		if err := ValidateAgentID(id); err != nil {
			t.Errorf("ValidateAgentID(%q) = %v, want nil", id, err)
		}
	}
	invalid := []string{"", strings.Repeat("a", 65), "Agent", "a_b", "a b", "a\nb", "a.b", "é", "a/b", "A"}
	for _, id := range invalid {
		if err := ValidateAgentID(id); err == nil {
			t.Errorf("ValidateAgentID(%q) = nil, want error", id)
		}
	}
}

func TestGenerateAgentID(t *testing.T) {
	t.Parallel()
	a, err := GenerateAgentID()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := GenerateAgentID()
	if len(a) != 32 || ValidateAgentID(a) != nil || a == b {
		t.Fatalf("generated IDs %q %q: want distinct 32-char valid IDs", a, b)
	}
	for _, c := range a {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Fatalf("generated ID %q is not lowercase hex", a)
		}
	}
}

func TestLoadOrCreateAgentID_PersistsAcrossCalls(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "state")
	first, err := LoadOrCreateAgentID(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreateAgentID(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("agent ID changed across loads: %q then %q", first, second)
	}
	info, err := os.Stat(filepath.Join(dir, AgentIDFileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("agent ID file mode = %o, want 0600", info.Mode().Perm())
	}
}

func TestLoadOrCreateAgentID_ReadsExistingFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// #nosec G302 -- private test fixture.
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, AgentIDFileName), []byte("my-agent-7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadOrCreateAgentID(dir)
	if err != nil || got != "my-agent-7" {
		t.Fatalf("got %q, %v; want my-agent-7", got, err)
	}
}

func TestLoadOrCreateAgentID_CorruptFileIsFatal(t *testing.T) {
	t.Parallel()
	for name, content := range map[string]string{
		"uppercase": "Bad_ID\n",
		"empty":     "",
		"too long":  strings.Repeat("a", 65),
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			// #nosec G302 -- private test fixture.
			if err := os.Chmod(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, AgentIDFileName), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			id, err := LoadOrCreateAgentID(dir)
			if err == nil || errors.Is(err, ErrStateUnavailable) || id != "" {
				t.Fatalf("got %q, %v; want fatal non-unavailable error and no ID", id, err)
			}
		})
	}
}

func TestLoadOrCreateAgentID_UnavailableDirFallsBack(t *testing.T) {
	t.Parallel()
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := LoadOrCreateAgentID(filepath.Join(blocker, "state"))
	if !errors.Is(err, ErrStateUnavailable) {
		t.Fatalf("err = %v, want ErrStateUnavailable", err)
	}
	if ValidateAgentID(id) != nil {
		t.Fatalf("fallback ID %q must still be valid", id)
	}
}

func TestLoadOrCreateAgentID_WorldReadableFileRefused(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// #nosec G302 -- private test fixture.
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, AgentIDFileName)
	if err := os.WriteFile(path, []byte("abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// #nosec G302 -- private test fixture.
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateAgentID(dir); err == nil || errors.Is(err, ErrStateUnavailable) {
		t.Fatalf("err = %v, want fatal permission error", err)
	}
}

func TestWriteStateFile_ReplacesAtomically(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "x.json")
	for _, content := range []string{"one", "two"} {
		if err := WriteStateFile(path, []byte(content)); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(path)
		if string(got) != content {
			t.Fatalf("got %q want %q", got, content)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}

func TestWriteStateFile_MissingDirFails(t *testing.T) {
	t.Parallel()
	if err := WriteStateFile(filepath.Join(t.TempDir(), "no", "x"), []byte("a")); err == nil {
		t.Fatal("want error")
	}
}

func TestPrepareStateDirectory_ModePolicy(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		mode os.FileMode
		ok   bool
	}{{0o700, true}, {0o770, true}, {0o775, true}, {0o777, false}, {0o702, false}} {
		dir := t.TempDir()
		// #nosec G302 -- private test fixture.
		if err := os.Chmod(dir, tc.mode); err != nil {
			t.Fatal(err)
		}
		if err := PrepareStateDirectory(dir); (err == nil) != tc.ok {
			t.Errorf("mode %04o: err = %v, want ok=%v", tc.mode, err, tc.ok)
		}
	}
	if err := PrepareStateDirectory(""); err == nil {
		t.Error("empty dir must fail")
	}
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PrepareStateDirectory(file); err == nil {
		t.Error("a file is not a state directory")
	}
}

func TestLoadOrCreateAgentID_GroupWritableDirPersists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// #nosec G302 -- private test fixture.
	if err := os.Chmod(dir, 0o775); err != nil {
		t.Fatal(err)
	}
	a, err := LoadOrCreateAgentID(dir)
	if err != nil {
		t.Fatalf("a group-writable (fsGroup) directory must persist: %v", err)
	}
	if b, _ := LoadOrCreateAgentID(dir); a != b {
		t.Fatalf("ID not stable: %q then %q", a, b)
	}
}
