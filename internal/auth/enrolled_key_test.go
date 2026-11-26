// SPDX-License-Identifier: MIT

package auth

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	if !key.Pending {
		t.Fatalf("fresh key must be pending: %+v", key)
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
		if !again.Pending {
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
	if err != nil || confirmed.Pending {
		t.Fatalf("confirmed key: %+v, %v", confirmed, err)
	}
}

// Covers corrupt content only; permission modes are in
// TestLoadOrCreateEnrolledKeyRefusesLoosePermissions.
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

func TestLoadOrCreateEnrolledKeyRefusesLoosePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits")
	}
	for _, mode := range []os.FileMode{0o644, 0o660} {
		t.Run(mode.String(), func(t *testing.T) {
			dir := t.TempDir()
			key, err := LoadOrCreateEnrolledKey(dir, true)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(key.Path, mode); err != nil { //nolint:gosec // loose-permission fixture
				t.Fatal(err)
			}
			before, err := os.ReadFile(key.Path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := LoadOrCreateEnrolledKey(dir, true); err == nil {
				t.Fatalf("a %04o enrolled key must be refused", mode)
			}
			after, err := os.ReadFile(key.Path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Fatal("the key was regenerated")
			}
		})
	}

	// Group-readable 0640 is accepted, matching the other credential files
	// (see TestParseAuthorizedKeys_GroupReadableAccepted).
	t.Run("0640 accepted", func(t *testing.T) {
		dir := t.TempDir()
		key, err := LoadOrCreateEnrolledKey(dir, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(key.Path, 0o640); err != nil { //nolint:gosec // group-readable fixture
			t.Fatal(err)
		}
		if _, err := LoadOrCreateEnrolledKey(dir, false); err != nil {
			t.Fatalf("0640 must be accepted: %v", err)
		}
	})

	// The existence check uses Lstat and the load follows links, so a symlink
	// to a valid 0600 key elsewhere is accepted as that key.
	t.Run("symlink to a valid key", func(t *testing.T) {
		other := t.TempDir()
		target, err := LoadOrCreateEnrolledKey(other, true)
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		if err := os.Symlink(target.Path, filepath.Join(dir, EnrolledKeyFileName)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		got, err := LoadOrCreateEnrolledKey(dir, true)
		if err != nil {
			t.Fatalf("symlink to a valid 0600 key: %v", err)
		}
		if got.Pending {
			t.Fatal("no marker in the link directory, so the key is confirmed")
		}
	})

	t.Run("symlink to a loose key", func(t *testing.T) {
		other := t.TempDir()
		target, err := LoadOrCreateEnrolledKey(other, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(target.Path, 0o644); err != nil { //nolint:gosec // loose-permission fixture
			t.Fatal(err)
		}
		dir := t.TempDir()
		if err := os.Symlink(target.Path, filepath.Join(dir, EnrolledKeyFileName)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if _, err := LoadOrCreateEnrolledKey(dir, true); err == nil {
			t.Fatal("a symlink to a loose key must be refused")
		}
	})
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

func TestLoadOrCreateEnrolledKeyFailureModes(t *testing.T) {
	t.Run("state dir is a file", func(t *testing.T) {
		notADir := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadOrCreateEnrolledKey(notADir, true); err == nil {
			t.Fatal("a state path that is a file must refuse")
		}
	})

	t.Run("key generation fails", func(t *testing.T) {
		prev := generateKeyPair
		generateKeyPair = func(string) ([]byte, string, error) { return nil, "", errors.New("no entropy") }
		t.Cleanup(func() { generateKeyPair = prev })
		dir := t.TempDir()
		if _, err := LoadOrCreateEnrolledKey(dir, true); err == nil || !strings.Contains(err.Error(), "generating enrolled key") {
			t.Fatalf("want a generation error, got %v", err)
		}
		if _, err := os.Lstat(filepath.Join(dir, EnrolledPendingFileName)); err == nil {
			t.Fatal("no marker may be written before a key exists")
		}
	})

	t.Run("marker cannot be written", func(t *testing.T) {
		dir := t.TempDir()
		// A directory where the marker file belongs makes the write fail.
		if err := os.Mkdir(filepath.Join(dir, EnrolledPendingFileName), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadOrCreateEnrolledKey(dir, true); err == nil || !strings.Contains(err.Error(), "writing enrollment marker") {
			t.Fatalf("want a marker error, got %v", err)
		}
		if _, err := os.Lstat(filepath.Join(dir, EnrolledKeyFileName)); err == nil {
			t.Fatal("no key may be written when the marker failed")
		}
	})

	t.Run("key cannot be linked into place", func(t *testing.T) {
		prev := linkFile
		linkFile = func(string, string) error { return errors.New("links unsupported") }
		t.Cleanup(func() { linkFile = prev })
		if _, err := LoadOrCreateEnrolledKey(t.TempDir(), true); err == nil || !strings.Contains(err.Error(), "writing enrolled key") {
			t.Fatalf("want a key write error, got %v", err)
		}
	})
}

func TestConfirmEnrolledKeyReportsRemovalFailure(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := ConfirmEnrolledKey(filepath.Join(notADir, EnrolledPendingFileName))
	if err == nil || !strings.Contains(err.Error(), "removing enrollment marker") {
		t.Fatalf("want a removal error, got %v", err)
	}
}

func TestLoadOrCreateEnrolledKeyDirPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	for _, mode := range []os.FileMode{0o770, 0o720} {
		dir := t.TempDir()
		if err := os.Chmod(dir, mode); err != nil { //nolint:gosec // writable directory fixture
			t.Fatal(err)
		}
		_, err := LoadOrCreateEnrolledKey(dir, true)
		if err == nil || !strings.Contains(err.Error(), "chmod 700") || !strings.Contains(err.Error(), dir) {
			t.Fatalf("mode %04o: want a chmod 700 error naming the dir, got %v", mode, err)
		}
		if _, statErr := os.Lstat(filepath.Join(dir, EnrolledKeyFileName)); statErr == nil {
			t.Fatalf("mode %04o: no key may be written", mode)
		}
	}

	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // private directory fixture
		t.Fatal(err)
	}
	if _, err := LoadOrCreateEnrolledKey(dir, true); err != nil {
		t.Fatalf("0700 must be accepted: %v", err)
	}
	// An existing key in a directory that later became group-writable refuses too.
	if err := os.Chmod(dir, 0o770); err != nil { //nolint:gosec // writable directory fixture
		t.Fatal(err)
	}
	if _, err := LoadOrCreateEnrolledKey(dir, false); err == nil || !strings.Contains(err.Error(), "chmod 700") {
		t.Fatalf("existing key in a group-writable dir: got %v", err)
	}
}

func TestLoadOrCreateEnrolledKeyConcurrentFirstStart(t *testing.T) {
	t.Run("winner still pending", func(t *testing.T) {
		dir := t.TempDir()
		prev := generateKeyPair
		t.Cleanup(func() { generateKeyPair = prev })
		generateKeyPair = func(s string) ([]byte, string, error) {
			// The winner finishes its whole first start while we generate.
			generateKeyPair = prev
			if _, err := LoadOrCreateEnrolledKey(dir, true); err != nil {
				t.Fatalf("winner: %v", err)
			}
			return prev(s)
		}
		key, err := LoadOrCreateEnrolledKey(dir, true)
		if err != nil {
			t.Fatalf("loser must load the winner's key, got %v", err)
		}
		if !key.Pending {
			t.Fatalf("winner has not confirmed: %+v", key)
		}
		if _, err := LoadPrivateKey(key.Path); err != nil {
			t.Fatalf("key unusable: %v", err)
		}
	})

	t.Run("winner already confirmed", func(t *testing.T) {
		// Fail-safe: we cannot tell a stray marker from one another start
		// is relying on, so the marker stays and the key reads as pending.
		// It clears on the next welcome; the reverse (a never-confirmed key
		// read as confirmed) would drop the enrollment code.
		dir := t.TempDir()
		prev := generateKeyPair
		t.Cleanup(func() { generateKeyPair = prev })
		generateKeyPair = func(s string) ([]byte, string, error) {
			generateKeyPair = prev
			won, err := LoadOrCreateEnrolledKey(dir, true)
			if err != nil {
				t.Fatalf("winner: %v", err)
			}
			if err := ConfirmEnrolledKey(won.MarkerPath); err != nil {
				t.Fatalf("confirm: %v", err)
			}
			return prev(s)
		}
		key, err := LoadOrCreateEnrolledKey(dir, true)
		if err != nil {
			t.Fatalf("loser must load the winner's key, got %v", err)
		}
		if !key.Pending {
			t.Fatalf("marker is kept, key must read pending: %+v", key)
		}
		if err := ConfirmEnrolledKey(key.MarkerPath); err != nil {
			t.Fatalf("confirm: %v", err)
		}
		if again, err := LoadOrCreateEnrolledKey(dir, false); err != nil || again.Pending {
			t.Fatalf("confirming clears the marker: %+v %v", again, err)
		}
	})
}

func TestLoadOrCreateEnrolledKeyWorldWritableDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	for _, mode := range []os.FileMode{0o707, 0o702, 0o777} {
		dir := t.TempDir()
		if err := os.Chmod(dir, mode); err != nil { //nolint:gosec // writable directory fixture
			t.Fatal(err)
		}
		_, err := LoadOrCreateEnrolledKey(dir, true)
		if err == nil || !strings.Contains(err.Error(), "chmod 700") || !strings.Contains(err.Error(), dir) {
			t.Fatalf("mode %04o: want a chmod 700 error naming the dir, got %v", mode, err)
		}
	}
}

func TestLoadOrCreateEnrolledKeyLosingLinkKeepsSharedMarker(t *testing.T) {
	dir := t.TempDir()
	prevLink := linkFile
	t.Cleanup(func() { linkFile = prevLink })
	linkFile = func(tmp, dst string) error {
		// Another start saw our marker, linked its own key and has not
		// been confirmed yet. We then lose the link race.
		linkFile = prevLink
		pemData, _, err := GenerateKeyPair("")
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if err := writeFileExclusive(dst, pemData); err != nil {
			t.Fatalf("other start: %v", err)
		}
		return os.ErrExist
	}
	key, err := LoadOrCreateEnrolledKey(dir, true)
	if err != nil {
		t.Fatalf("loser must load the other start's key: %v", err)
	}
	if !key.Pending {
		t.Fatalf("unconfirmed key must stay pending: %+v", key)
	}
	if _, err := os.Lstat(key.MarkerPath); err != nil {
		t.Fatalf("shared marker must survive: %v", err)
	}
}

func TestCreateMarkerExclusiveMarkerGoneBeforeLstat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, EnrolledPendingFileName)
	if err := os.WriteFile(path, []byte("pending\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	prev := lstatFile
	t.Cleanup(func() { lstatFile = prev })
	lstatFile = func(p string) (os.FileInfo, error) {
		_ = os.Remove(p)
		return prev(p)
	}
	if err := createMarkerExclusive(path); !errors.Is(err, os.ErrExist) {
		t.Fatalf("a vanished marker must read as ErrExist, got %v", err)
	}
}

func TestCheckEnrolledKeyDirWindowsSkipsModeCheck(t *testing.T) {
	prev := enrolledKeyGOOS
	t.Cleanup(func() { enrolledKeyGOOS = prev })
	enrolledKeyGOOS = "windows"
	if err := checkEnrolledKeyDir(filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Fatalf("windows has no POSIX modes to check: %v", err)
	}
}

func TestCheckEnrolledKeyDirStatFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	err := checkEnrolledKeyDir(filepath.Join(t.TempDir(), "missing"))
	if !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "checking enrolled key directory") {
		t.Fatalf("want a wrapped not-exist error, got %v", err)
	}
}

func TestLoadOrCreateEnrolledKeyDirTurnsUnsafeAfterWritableProbe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	dir := t.TempDir()
	prev := statDir
	t.Cleanup(func() { statDir = prev })
	calls := 0
	statDir = func(p string) (os.FileInfo, error) {
		calls++
		if calls == 1 {
			// First check: the directory is not there yet.
			return nil, os.ErrNotExist
		}
		// Second check: another process made it group-writable meanwhile.
		if err := os.Chmod(p, 0o770); err != nil { //nolint:gosec // writable directory fixture
			t.Fatal(err)
		}
		return prev(p)
	}
	_, err := LoadOrCreateEnrolledKey(dir, true)
	if err == nil || !strings.Contains(err.Error(), "chmod 700") {
		t.Fatalf("want the unsafe-directory error, got %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(dir, EnrolledKeyFileName)); statErr == nil {
		t.Fatal("no key may be written into an unsafe directory")
	}
}

type fakeMarkerFile struct {
	writeErr, syncErr, closeErr error
	closed                      bool
}

func (f *fakeMarkerFile) WriteString(s string) (int, error) { return len(s), f.writeErr }
func (f *fakeMarkerFile) Sync() error                       { return f.syncErr }
func (f *fakeMarkerFile) Close() error                      { f.closed = true; return f.closeErr }

func TestCreateMarkerExclusiveIOFailuresCleanUp(t *testing.T) {
	boom := errors.New("disk on fire")
	cases := []struct {
		name string
		file *fakeMarkerFile
	}{
		{"write fails", &fakeMarkerFile{writeErr: boom}},
		{"sync fails", &fakeMarkerFile{syncErr: boom}},
		{"close fails", &fakeMarkerFile{closeErr: boom}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), EnrolledPendingFileName)
			if err := os.WriteFile(path, []byte("partial"), 0o600); err != nil {
				t.Fatal(err)
			}
			prev := openMarkerFile
			t.Cleanup(func() { openMarkerFile = prev })
			openMarkerFile = func(string) (markerFile, error) { return tc.file, nil }
			if err := createMarkerExclusive(path); !errors.Is(err, boom) {
				t.Fatalf("want the I/O error, got %v", err)
			}
			if _, err := os.Lstat(path); err == nil {
				t.Fatal("a half-written marker must be removed")
			}
			if !tc.file.closed {
				t.Fatal("the file must be closed")
			}
		})
	}
}

func TestCreateMarkerExclusiveRefusesNonRegularMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, EnrolledPendingFileName)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := createMarkerExclusive(path); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("want a not-regular error, got %v", err)
	}
}
