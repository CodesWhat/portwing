// SPDX-License-Identifier: MIT

package docker

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// These tests swap package-level seams, so they must not run in parallel.

func TestStageRootedFileRetriesOnTempNameCollision(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()

	if err := os.WriteFile(dir+"/.app.conf.portwing-taken", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	suffixes := []string{"taken", "fresh"}
	orig := stagingSuffix
	stagingSuffix = func() string { s := suffixes[0]; suffixes = suffixes[1:]; return s }
	t.Cleanup(func() { stagingSuffix = orig })

	tmp, err := stageRootedFile(root, "app.conf", []byte("x"), 0o600)
	if err != nil {
		t.Fatalf("stageRootedFile: %v", err)
	}
	if tmp != ".app.conf.portwing-fresh" {
		t.Fatalf("tmp = %q, want the second suffix after a collision", tmp)
	}
}

func TestStageRootedFileGivesUpAfterRepeatedCollisions(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()

	if err := os.WriteFile(dir+"/.app.conf.portwing-taken", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	orig := stagingSuffix
	stagingSuffix = func() string { return "taken" }
	t.Cleanup(func() { stagingSuffix = orig })

	_, err = stageRootedFile(root, "app.conf", []byte("x"), 0o600)
	if err == nil || !strings.Contains(err.Error(), "could not create a unique temp file") {
		t.Fatalf("err = %v, want the collision give-up error", err)
	}
}

func TestStageRootedFileRemovesTempWhenWriteFails(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()

	boom := errors.New("write failed")
	orig := writeStaged
	writeStaged = func(f *os.File, _ []byte, _ os.FileMode) error { _ = f.Close(); return boom }
	t.Cleanup(func() { writeStaged = orig })

	if _, err := stageRootedFile(root, "app.conf", []byte("x"), 0o600); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the write failure", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temp file left behind: %v", entries)
	}
}
