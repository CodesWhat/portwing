// SPDX-License-Identifier: MIT

package auth

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadOrCreateEnrolledKeyLifecycle(t *testing.T) {
	dir := t.TempDir()

	if _, err := LoadOrCreateEnrolledKey(dir, false); !errors.Is(err, ErrNoEnrolledKey) {
		t.Fatalf("no key and no code: want ErrNoEnrolledKey, got %v", err)
	}

	key, err := LoadOrCreateEnrolledKey(dir, true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !key.Created || !key.Pending {
		t.Fatalf("fresh key must be created and pending: %+v", key)
	}
	info, err := os.Stat(key.Path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %04o, want 0600", info.Mode().Perm())
	}
	if _, err := LoadPrivateKey(key.Path); err != nil {
		t.Fatalf("persisted key does not load: %v", err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, ".enroll-*"))
	if len(leftovers) != 0 {
		t.Fatalf("temp files left behind: %v", leftovers)
	}
	original, _ := os.ReadFile(key.Path)

	// A restart before confirmation reuses the same key and stays pending,
	// with or without a code.
	for _, canCreate := range []bool{true, false} {
		again, err := LoadOrCreateEnrolledKey(dir, canCreate)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if again.Created || !again.Pending {
			t.Fatalf("reload must reuse the pending key: %+v", again)
		}
		got, _ := os.ReadFile(key.Path)
		if string(got) != string(original) {
			t.Fatal("existing key was overwritten")
		}
	}

	if err := ConfirmEnrolledKey(key.MarkerPath); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if err := ConfirmEnrolledKey(key.MarkerPath); err != nil {
		t.Fatalf("confirm is idempotent: %v", err)
	}
	confirmed, err := LoadOrCreateEnrolledKey(dir, true)
	if err != nil || confirmed.Pending || confirmed.Created {
		t.Fatalf("confirmed key: %+v, %v", confirmed, err)
	}
}

func TestLoadOrCreateEnrolledKeyRefusesUnsafeExistingKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, EnrolledKeyFileName)
	if err := os.WriteFile(path, []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateEnrolledKey(dir, true); err == nil {
		t.Fatal("a corrupt existing key must be an error, never regenerated")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "not a key" {
		t.Fatal("existing file was overwritten")
	}
}

func TestLoadOrCreateEnrolledKeyUnwritableDir(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a non-root POSIX user")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil { //nolint:gosec // read-only directory fixture
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // restore the fixture so cleanup can remove it
	if _, err := LoadOrCreateEnrolledKey(dir, true); err == nil {
		t.Fatal("unwritable state dir must refuse")
	}
}

func TestWriteFileExclusiveRefusesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "k")
	if err := writeFileExclusive(path, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := writeFileExclusive(path, []byte("two")); !errors.Is(err, os.ErrExist) {
		t.Fatalf("want ErrExist, got %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "one" {
		t.Fatalf("overwritten: %q", got)
	}
}

func TestWriteFileExclusiveFailsClosedWithoutLinks(t *testing.T) {
	prev := linkFile
	linkFile = func(string, string) error { return errors.New("links unsupported") }
	t.Cleanup(func() { linkFile = prev })

	path := filepath.Join(t.TempDir(), "k")
	if err := writeFileExclusive(path, []byte("one")); err == nil {
		t.Fatal("must fail closed when no-replace link is unavailable")
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nothing may be written: %v", err)
	}
}
