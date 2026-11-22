// SPDX-License-Identifier: MIT

package docker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeLoginDocker installs a docker that records its argv and stdin, then
// exits with the given status, printing stderr text first. Not for parallel
// tests: it goes through PATH.
func fakeLoginDocker(t *testing.T, exitCode, stderrText string) (argvPath, stdinPath string) {
	t.Helper()
	binDir := t.TempDir()
	argvPath = filepath.Join(binDir, "argv")
	stdinPath = filepath.Join(binDir, "stdin")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$PORTWING_TEST_ARGV\"\ncat > \"$PORTWING_TEST_STDIN\"\n" +
		"printf '%s' \"$PORTWING_TEST_STDERR\" >&2\nexit " + exitCode + "\n"
	if err := os.WriteFile(filepath.Join(binDir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PORTWING_TEST_ARGV", argvPath)
	t.Setenv("PORTWING_TEST_STDIN", stdinPath)
	t.Setenv("PORTWING_TEST_STDERR", stderrText)
	t.Setenv("PATH", binDir+string(filepath.ListSeparator)+os.Getenv("PATH"))
	return argvPath, stdinPath
}

// The password reaches docker on stdin with --password-stdin and appears in no
// argv element, on success and on failure alike.
func TestRegistryLogin_PasswordViaStdinNeverArgv(t *testing.T) {
	const password = "s3cret-pw-value"
	for _, tc := range []struct {
		name, exit string
		wantErr    bool
	}{
		{"success", "0", false},
		{"failure", "1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			argvPath, stdinPath := fakeLoginDocker(t, tc.exit, "")
			cm := &ComposeManager{stacksDir: t.TempDir()}

			err := cm.registryLogin(t.Context(), &RegistryAuth{Server: "registry.example.com", Username: "alice", Password: password})
			if (err != nil) != tc.wantErr {
				t.Fatalf("registryLogin error = %v, wantErr %v", err, tc.wantErr)
			}

			argvRaw, err := os.ReadFile(argvPath)
			if err != nil {
				t.Fatal(err)
			}
			argv := strings.Split(strings.TrimSpace(string(argvRaw)), "\n")
			want := []string{"login", "--username", "alice", "--password-stdin", "--", "registry.example.com"}
			if strings.Join(argv, " ") != strings.Join(want, " ") {
				t.Fatalf("argv = %q, want %q", argv, want)
			}
			for _, a := range argv {
				if strings.Contains(a, password) {
					t.Fatalf("password found in argv element %q", a)
				}
				if a == "--password" || a == "-p" {
					t.Fatalf("argv carries the plain password flag %q", a)
				}
			}
			stdin, err := os.ReadFile(stdinPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(stdin) != password {
				t.Fatalf("stdin = %q, want the password", stdin)
			}
		})
	}
}

// A failed login returns an error naming docker's stderr, without the
// password, and Execute then reports Success=false without running compose.
func TestRegistryLogin_FailureReportsStderrAndStopsExecute(t *testing.T) {
	const password = "s3cret-pw-value"
	argvPath, _ := fakeLoginDocker(t, "1", "unauthorized: bad credentials")
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o750); err != nil {
		t.Fatal(err)
	}
	cm := &ComposeManager{stacksDir: dir, composeBin: "docker", isV2: true}

	err := cm.registryLogin(t.Context(), &RegistryAuth{Server: "registry.example.com", Username: "alice", Password: password})
	if err == nil || !strings.Contains(err.Error(), "docker login") || !strings.Contains(err.Error(), "unauthorized: bad credentials") {
		t.Fatalf("registryLogin error = %v, want docker login failure carrying stderr", err)
	}
	if strings.Contains(err.Error(), password) {
		t.Fatalf("error leaks the password: %v", err)
	}

	resp, err := cm.Execute(t.Context(), ComposeRequest{
		StackName:    "app",
		Operation:    "up",
		RegistryAuth: &RegistryAuth{Server: "registry.example.com", Username: "alice", Password: password},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp.Success || !strings.Contains(resp.Error, "unauthorized: bad credentials") {
		t.Fatalf("Execute = %+v, want a failed response carrying the login error", resp)
	}
	if strings.Contains(resp.Error, password) || strings.Contains(resp.Output, password) {
		t.Fatalf("Execute response leaks the password: %+v", resp)
	}
	argvRaw, err := os.ReadFile(argvPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(argvRaw), "compose") {
		t.Fatalf("compose ran after a failed login; last docker argv:\n%s", argvRaw)
	}
}
