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
