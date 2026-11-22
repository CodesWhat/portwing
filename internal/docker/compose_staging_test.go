// SPDX-License-Identifier: MIT

package docker

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("permission failures can't be provoked as root")
	}
}

// lockDir makes dir unwritable (or unreadable) and restores it on cleanup so
// t.TempDir can remove it.
func lockDir(t *testing.T, dir string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(dir, mode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o750) }) //nolint:gosec // restoring a test-owned temp dir
}

func stagedTempFiles(t *testing.T, dir string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(d.Name(), ".portwing-") {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return found
}

func TestPrepareStackWritesRejectsDuplicateTargets(t *testing.T) {
	t.Parallel()

	cm := &ComposeManager{stacksDir: t.TempDir()}
	cases := map[string]ComposeRequest{
		"two keys, one file": {
			StackName: "app",
			Files:     map[string]string{"a.yml": "x", "./a.yml": "y"},
		},
		"env file collides with a file key": {
			StackName: "app",
			Files:     map[string]string{".env.drydock": "x"},
			EnvVars:   map[string]string{"A": "b"},
		},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := cm.prepareStackWrites(req); err == nil || !strings.Contains(err.Error(), "resolve to the same file") {
				t.Fatalf("err = %v, want duplicate-target error", err)
			}
		})
	}
}

func TestCheckRootedTargetReportsLstatFailure(t *testing.T) {
	skipIfRoot(t)

	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o750); err != nil {
		t.Fatal(err)
	}
	lockDir(t, locked, 0o000)

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()

	err = checkRootedTarget(root, filepath.Join("locked", "file.yml"))
	if err == nil || !strings.Contains(err.Error(), filepath.Join("locked", "file.yml")) {
		t.Fatalf("err = %v, want a checking error naming the path", err)
	}
}

func TestStageRootedFileFailures(t *testing.T) {
	t.Run("parent is a file", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		root, err := os.OpenRoot(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = root.Close() }()

		if _, err := stageRootedFile(root, filepath.Join("f", "x.yml"), []byte("y")); err == nil {
			t.Fatal("expected error staging below a regular file")
		}
		if left := stagedTempFiles(t, dir); len(left) != 0 {
			t.Fatalf("temp files left: %v", left)
		}
	})

	t.Run("directory not writable", func(t *testing.T) {
		skipIfRoot(t)
		dir := t.TempDir()
		ro := filepath.Join(dir, "ro")
		if err := os.Mkdir(ro, 0o750); err != nil {
			t.Fatal(err)
		}
		lockDir(t, ro, 0o500)
		root, err := os.OpenRoot(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = root.Close() }()

		if _, err := stageRootedFile(root, filepath.Join("ro", "x.yml"), []byte("y")); err == nil {
			t.Fatal("expected temp file creation to fail in a read-only directory")
		}
		if left := stagedTempFiles(t, dir); len(left) != 0 {
			t.Fatalf("temp files left: %v", left)
		}
	})
}

func TestWriteAndCloseReportsFailures(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	closed, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = closed.Close()
	if err := writeAndClose(closed, []byte("y")); err == nil || !strings.Contains(err.Error(), "setting permissions") {
		t.Fatalf("closed file: err = %v, want a permissions error", err)
	}

	readOnly, err := os.Open(path) //nolint:gosec // test-owned temp file
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAndClose(readOnly, []byte("y")); err == nil {
		t.Fatal("expected a write error on a read-only handle")
	}
	if got, _ := os.ReadFile(path); string(got) != "x" { //nolint:gosec // test-owned temp file
		t.Fatalf("file changed to %q", got)
	}
}

// A failure staging a later file must remove the temp files already staged for
// earlier ones and leave the tree untouched.
func TestWriteStackFilesStagingFailureCleansUp(t *testing.T) {
	skipIfRoot(t)

	dir := t.TempDir()
	for _, sub := range []string{"a", "b"} {
		if err := os.MkdirAll(filepath.Join(dir, "app", sub), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	lockDir(t, filepath.Join(dir, "app", "b"), 0o500)
	before := snapshotStackTree(t, dir)

	cm := &ComposeManager{stacksDir: dir}
	err := cm.writeStackFiles(ComposeRequest{
		StackName: "app",
		Files:     map[string]string{"a/one.yml": "one", "b/two.yml": "two"},
	})
	if err == nil || !strings.Contains(err.Error(), filepath.Join("app", "b", "two.yml")) {
		t.Fatalf("err = %v, want an error naming b/two.yml", err)
	}
	if left := stagedTempFiles(t, dir); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
	assertStackTreesEqual(t, before, snapshotStackTree(t, dir))
}

// A staged file that disappears before the rename must fail the write, remove
// the remaining temp files and leave the target as it was. The watcher deletes
// the first file's temp while the second (large) file is still being staged.
func TestWriteStackFilesRenameFailureCleansUp(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "app")
	if err := os.MkdirAll(appDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "one.yml"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshotStackTree(t, dir)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			entries, _ := os.ReadDir(appDir)
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".one.yml.portwing-") {
					_ = os.Remove(filepath.Join(appDir, e.Name()))
				}
			}
			time.Sleep(50 * time.Microsecond)
		}
	}()

	big := strings.Repeat("x", 48<<20)
	cm := &ComposeManager{stacksDir: dir}
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		err = cm.writeStackFiles(ComposeRequest{
			StackName: "app",
			Files:     map[string]string{"one.yml": "new", "two.yml": big},
		})
		if err != nil {
			break
		}
		// The watcher lost the race and the write landed. Reset and retry.
		_ = os.Remove(filepath.Join(appDir, "two.yml"))
		if werr := os.WriteFile(filepath.Join(appDir, "one.yml"), []byte("old"), 0o600); werr != nil {
			t.Fatal(werr)
		}
	}
	close(stop)
	wg.Wait()

	if err == nil || !strings.Contains(err.Error(), filepath.Join("app", "one.yml")) {
		t.Fatalf("err = %v, want a rename error naming one.yml", err)
	}
	if left := stagedTempFiles(t, dir); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
	assertStackTreesEqual(t, before, snapshotStackTree(t, dir))
}
