package docker

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteStackFilesUsesPrivatePermissions(t *testing.T) {
	t.Parallel()

	stacksDir := t.TempDir()
	cm := &ComposeManager{stacksDir: stacksDir}

	req := ComposeRequest{
		StackName: "app",
		EnvVars: map[string]string{
			"PORTWING_ENV": "production",
		},
		Files: map[string]string{
			"docker-compose.yml": "services: {}\n",
			"nested/config.yml":  "x: y\n",
		},
	}

	if err := cm.writeStackFiles(req); err != nil {
		t.Fatalf("writeStackFiles: %v", err)
	}

	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not meaningful on windows")
	}

	assertMode(t, filepath.Join(stacksDir, "app"), 0o750)
	assertMode(t, filepath.Join(stacksDir, "app", "nested"), 0o750)
	assertMode(t, filepath.Join(stacksDir, "app", "docker-compose.yml"), 0o600)
	assertMode(t, filepath.Join(stacksDir, "app", "nested", "config.yml"), 0o600)
	assertMode(t, filepath.Join(stacksDir, "app", ".env.drydock"), 0o600)
}

func TestResolvePathRejectsStackFileTraversal(t *testing.T) {
	t.Parallel()

	stacksDir := t.TempDir()
	cm := &ComposeManager{stacksDir: stacksDir}

	if _, err := cm.resolvePath("app", "nested/config.yml"); err != nil {
		t.Fatalf("resolvePath valid nested file: %v", err)
	}

	cases := map[string]struct {
		stackDir string
		path     string
	}{
		"absolute stack dir":    {filepath.Join(stacksDir, "app"), "compose.yml"},
		"absolute file path":    {"app", filepath.Join(stacksDir, "app", "compose.yml")},
		"stack dir traversal":   {"../outside", "compose.yml"},
		"cross stack traversal": {"app", "../other/compose.yml"},
		"cleaned traversal":     {"app", "nested/../../other/compose.yml"},
	}

	for name, c := range cases {
		if _, err := cm.resolvePath(c.stackDir, c.path); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

func TestWriteStackFilesRejectsSymlinkedStackRootEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink setup requires Windows privileges")
	}
	stacksDir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(stacksDir, "app")); err != nil {
		t.Fatal(err)
	}
	cm := &ComposeManager{stacksDir: stacksDir}
	err := cm.writeStackFiles(ComposeRequest{
		StackName: "app",
		Files:     map[string]string{"compose.yml": "attacker-controlled"},
	})
	if err == nil {
		t.Fatal("expected symlinked stack root to be rejected")
	}
	if _, statErr := os.Stat(filepath.Join(outside, "compose.yml")); !os.IsNotExist(statErr) {
		t.Fatalf("write escaped STACKS_DIR through symlink: %v", statErr)
	}
}

func TestWriteStackFilesRejectsSymlinkedNestedFileEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink setup requires Windows privileges")
	}
	stacksDir := t.TempDir()
	stackDir := filepath.Join(stacksDir, "app")
	if err := os.Mkdir(stackDir, 0o750); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "protected")
	if err := os.WriteFile(outside, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(stackDir, "compose.yml")); err != nil {
		t.Fatal(err)
	}
	cm := &ComposeManager{stacksDir: stacksDir}
	err := cm.writeStackFiles(ComposeRequest{
		StackName: "app",
		Files:     map[string]string{"compose.yml": "overwritten"},
	})
	if err == nil {
		t.Fatal("expected symlinked nested file to be rejected")
	}
	got, readErr := os.ReadFile(outside)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "original" {
		t.Fatalf("outside file was modified: %q", got)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %#o, want %#o", path, got, want)
	}
}
