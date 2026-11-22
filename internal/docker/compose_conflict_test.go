// SPDX-License-Identifier: MIT

package docker

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// snapshotStackTree returns every path under dir with its contents (or "<dir>"), so
// a test can assert a rejected request left the tree byte-identical.
func snapshotStackTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if d.IsDir() {
			out[rel] = "<dir>"
			return nil
		}
		data, err := os.ReadFile(path) //nolint:gosec // test-owned temp tree
		if err != nil {
			return err
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", dir, err)
	}
	return out
}

func assertStackTreesEqual(t *testing.T, before, after map[string]string) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("tree changed: before %v, after %v", before, after)
	}
	for k, v := range before {
		if got, ok := after[k]; !ok || got != v {
			t.Fatalf("tree changed at %q: before %q, after %q (present %v)", k, v, got, ok)
		}
	}
}

func TestWriteStackFilesRejectsFileAndDirectoryConflicts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		existing  map[string]string // paths under the stack dir; a trailing "/" makes a directory
		files     map[string]string
		wantInErr []string
	}{
		{
			name:      "target is a parent directory of another target",
			existing:  map[string]string{"compose.yaml": "old\n"},
			files:     map[string]string{"compose.yaml": "new\n", "z": "file", "z/child": "child"},
			wantInErr: []string{`"z"`, `"z/child"`},
		},
		{
			name:      "target is a deeper ancestor of another target",
			existing:  map[string]string{"compose.yaml": "old\n"},
			files:     map[string]string{"compose.yaml": "new\n", "a/b": "file", "a/b/c/d": "child"},
			wantInErr: []string{`"a/b"`, `"a/b/c/d"`},
		},
		{
			name:      "existing directory at a target file path",
			existing:  map[string]string{"compose.yaml": "old\n", "conf/": ""},
			files:     map[string]string{"compose.yaml": "new\n", "conf": "file"},
			wantInErr: []string{`conf`},
		},
		{
			name:      "existing file where a parent directory is needed",
			existing:  map[string]string{"compose.yaml": "old\n", "conf": "i am a file"},
			files:     map[string]string{"compose.yaml": "new\n", "conf/app.env": "A=1"},
			wantInErr: []string{`conf`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cm := newTestComposeManager(t)
			stackDir := filepath.Join(cm.stacksDir, "stack")
			if err := os.MkdirAll(stackDir, 0o750); err != nil {
				t.Fatal(err)
			}
			for name, content := range tc.existing {
				path := filepath.Join(stackDir, name)
				if strings.HasSuffix(name, "/") {
					if err := os.MkdirAll(path, 0o750); err != nil {
						t.Fatal(err)
					}
					continue
				}
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshotStackTree(t, cm.stacksDir)

			err := cm.writeStackFiles(ComposeRequest{StackName: "stack", Files: tc.files})
			if err == nil {
				t.Fatal("writeStackFiles accepted a file/directory conflict")
			}
			for _, want := range tc.wantInErr {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q does not name %s", err, want)
				}
			}
			assertStackTreesEqual(t, before, snapshotStackTree(t, cm.stacksDir))
		})
	}
}
