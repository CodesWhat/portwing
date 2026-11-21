// SPDX-License-Identifier: MIT

package docker

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/dotenv"
)

// envRoundTripValues are the values Compose's dotenv parsing would alter if
// they were written raw: interpolation, escapes, inline comments, quotes and
// edge whitespace.
var envRoundTripValues = []string{
	"plain",
	"",
	"$VAR",
	"${X}",
	"${X:-fallback}",
	"$$",
	"$$VAR",
	"a #b",
	"a#b",
	`"q"`,
	`'s'`,
	`it's`,
	` lead`,
	`trail `,
	"  both  ",
	"a=b=c",
	"=",
	`back\slash`,
	`trailing\`,
	`\`,
	`\\`,
	`\'`,
	`\"`,
	`\$HOME`,
	`C:\path\to\file`,
	`a\nb`,
	"unicode \u00e9\u4e16\u754c \U0001f600",
	"tab\there",
	"export FOO",
	"mixed 'single' and \"double\" and $dollar and \\back",
}

func parseEnvFile(t *testing.T, data []byte) map[string]string {
	t.Helper()
	// A lookup that resolves everything makes any surviving interpolation
	// visible as a changed value.
	got, err := dotenv.UnmarshalBytesWithLookup(data, func(string) (string, bool) { return "LOOKED_UP", true })
	if err != nil {
		t.Fatalf("compose dotenv rejected the file: %v\n%s", err, data)
	}
	return got
}

func TestEnvFileRoundTripsThroughComposeDotenv(t *testing.T) {
	t.Parallel()
	for _, want := range envRoundTripValues {
		t.Run(want, func(t *testing.T) {
			t.Parallel()
			data, err := renderEnvFile(map[string]string{"KEY": want, "OTHER": "x"})
			if err != nil {
				t.Fatalf("renderEnvFile: %v", err)
			}
			got := parseEnvFile(t, data)
			if got["KEY"] != want {
				t.Errorf("KEY = %q, want %q\nfile: %s", got["KEY"], want, data)
			}
			if got["OTHER"] != "x" {
				t.Errorf("OTHER = %q, want x (a value must not swallow its neighbor)", got["OTHER"])
			}
		})
	}
}

func TestEnvFileRoundTripsAllValuesTogether(t *testing.T) {
	t.Parallel()
	vars := make(map[string]string, len(envRoundTripValues))
	for i, v := range envRoundTripValues {
		vars["V"+strings.Repeat("_", i)+"X"] = v
	}
	data, err := renderEnvFile(vars)
	if err != nil {
		t.Fatal(err)
	}
	got := parseEnvFile(t, data)
	for k, want := range vars {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
}

func TestEnvFileRejectsUnrepresentableValuesAndBadKeys(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		vars map[string]string
		want string
	}{
		{"newline", map[string]string{"BAD_NL": "a\nb"}, "BAD_NL"},
		{"carriage return", map[string]string{"BAD_CR": "a\rb"}, "BAD_CR"},
		{"nul", map[string]string{"BAD_NUL": "a\x00b"}, "BAD_NUL"},
		{"key with space", map[string]string{"A B": "x"}, "A B"},
		{"key with dash", map[string]string{"A-B": "x"}, "A-B"},
		{"key with equals", map[string]string{"A=B": "x"}, "A=B"},
		{"key starting with digit", map[string]string{"1A": "x"}, "1A"},
		{"empty key", map[string]string{"": "x"}, `""`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := renderEnvFile(tc.vars)
			if err == nil {
				t.Fatal("renderEnvFile succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name %q", err, tc.want)
			}
		})
	}
}

func TestWriteStackFilesStoresQuotedEnvFile(t *testing.T) {
	t.Parallel()
	cm := newTestComposeManager(t)
	vars := map[string]string{"A": "$HOME", "B": "it's #1", "C": `x\`}
	if err := cm.writeStackFiles(ComposeRequest{StackName: "app", EnvVars: vars}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(cm.stacksDir, "app", ".env.drydock"))
	if err != nil {
		t.Fatal(err)
	}
	got := parseEnvFile(t, data)
	for k, want := range vars {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
}

// snapshotTree records every path under dir with its mode and contents so a
// test can assert a failed request changed nothing, not even an empty dir.
func snapshotTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	fsys := os.DirFS(dir)
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			snap[path] = "dir " + info.Mode().String()
			return nil
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		snap[path] = info.Mode().String() + " " + string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestWriteStackFilesBadBase64ChangesNothing(t *testing.T) {
	t.Parallel()
	cm := newTestComposeManager(t)
	stack := filepath.Join(cm.stacksDir, "app")
	if err := os.MkdirAll(stack, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stack, "compose.yaml"), []byte("old: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stack, ".env.drydock"), []byte("OLD='1'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, cm.stacksDir)

	// Sorted order puts the bad file in the middle, after valid files that
	// would already have been written by a write-as-you-go implementation.
	err := cm.writeStackFiles(ComposeRequest{
		StackName: "app",
		Files: map[string]string{
			"a-new.yaml":        "new: a\n",
			"compose.yaml":      "new: true\n",
			"m-bad.bin":         "base64:!!!not base64!!!",
			"sub/z-later.yaml":  "later: true\n",
			"zz-also-valid.txt": "base64:aGk=",
		},
		EnvVars: map[string]string{"NEW": "1"},
	})
	if err == nil || !strings.Contains(err.Error(), "m-bad.bin") {
		t.Fatalf("err = %v, want a base64 error naming m-bad.bin", err)
	}
	after := snapshotTree(t, cm.stacksDir)
	if len(before) != len(after) {
		t.Fatalf("tree changed: before %v after %v", before, after)
	}
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s changed: %q -> %q", k, v, after[k])
		}
	}
}

func TestWriteStackFilesBadEnvChangesNothing(t *testing.T) {
	t.Parallel()
	cm := newTestComposeManager(t)
	before := snapshotTree(t, cm.stacksDir)
	err := cm.writeStackFiles(ComposeRequest{
		StackName: "app",
		Files:     map[string]string{"compose.yaml": "x: 1\n"},
		EnvVars:   map[string]string{"GOOD": "1", "BAD_VAR": "a\x00b"},
	})
	if err == nil || !strings.Contains(err.Error(), "BAD_VAR") {
		t.Fatalf("err = %v, want an error naming BAD_VAR", err)
	}
	after := snapshotTree(t, cm.stacksDir)
	if len(before) != len(after) {
		t.Fatalf("tree changed: before %v after %v", before, after)
	}
}

func TestWriteStackFilesLeavesNoTempFiles(t *testing.T) {
	t.Parallel()
	cm := newTestComposeManager(t)
	err := cm.writeStackFiles(ComposeRequest{
		StackName: "app",
		Files:     map[string]string{"compose.yaml": "x: 1\n", "conf/app.conf": "y\n"},
		EnvVars:   map[string]string{"A": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	snap := snapshotTree(t, cm.stacksDir)
	for _, want := range []string{"app/compose.yaml", "app/conf/app.conf", "app/.env.drydock"} {
		if _, ok := snap[want]; !ok {
			t.Errorf("missing %s in %v", want, snap)
		}
	}
	for k := range snap {
		if strings.Contains(k, ".portwing-") {
			t.Errorf("temp file left behind: %s", k)
		}
	}
	if mode := snap["app/.env.drydock"]; !strings.HasPrefix(mode, "-rw------- ") {
		t.Errorf(".env.drydock mode/content = %q, want 0600", mode)
	}
}
