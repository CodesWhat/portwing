// SPDX-License-Identifier: MIT

package docker

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// argvFixture returns a manager rooted at a temp STACKS_DIR holding an empty
// "app" stack, plus the resolved project directory buildCommand passes.
func argvFixture(t *testing.T) (*ComposeManager, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o750); err != nil {
		t.Fatal(err)
	}
	cm := &ComposeManager{stacksDir: dir, composeBin: "docker", isV2: true}
	projectDir, err := cm.resolvePath("app", ".")
	if err != nil {
		t.Fatal(err)
	}
	return cm, projectDir
}

// TestBuildCommand_ExactArgv pins the whole argv for the options-off request
// of each operation, so a flag added to or dropped from any of them (and a
// destructive flag in particular) fails here rather than slipping through a
// contains-style check.
func TestBuildCommand_ExactArgv(t *testing.T) {
	t.Parallel()

	cm, projectDir := argvFixture(t)
	base := []string{"docker", "compose", "-p", deriveProjectName("app"), "--project-directory", projectDir}

	cases := []struct {
		name string
		req  ComposeRequest
		tail []string
	}{
		{"up", ComposeRequest{Operation: "up"}, []string{"up", "-d", "--remove-orphans"}},
		{"up with options", ComposeRequest{Operation: "up", Build: true, ForceRecreate: true, NoDeps: true, Services: []string{"web", "db"}},
			[]string{"up", "-d", "--remove-orphans", "--build", "--force-recreate", "--no-deps", "web", "db"}},
		{"down", ComposeRequest{Operation: "down"}, []string{"down", "--remove-orphans"}},
		{"down with volumes", ComposeRequest{Operation: "down", RemoveVolumes: true}, []string{"down", "--remove-orphans", "--volumes"}},
		{"pull", ComposeRequest{Operation: "pull"}, []string{"pull"}},
		{"ps", ComposeRequest{Operation: "ps"}, []string{"ps", "--format", "json"}},
		{"logs", ComposeRequest{Operation: "logs"}, []string{"logs", "--tail", "100"}},
		{"restart", ComposeRequest{Operation: "restart"}, []string{"restart"}},
		{"stop", ComposeRequest{Operation: "stop"}, []string{"stop"}},
		{"start", ComposeRequest{Operation: "start"}, []string{"start"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := tc.req
			req.StackName = "app"
			cmd, err := cm.buildCommand(t.Context(), req)
			if err != nil {
				t.Fatalf("buildCommand: %v", err)
			}
			want := append(slices.Clone(base), tc.tail...)
			if !reflect.DeepEqual(cmd.Args, want) {
				t.Fatalf("argv = %q\nwant   %q", cmd.Args, want)
			}
		})
	}
}

// TestBuildCommand_DestructiveFlagsAbsentWhenOff checks the gated flags
// directly: volumes are only removed when the request asks, and no operation
// ever carries -v/--volumes without it.
func TestBuildCommand_DestructiveFlagsAbsentWhenOff(t *testing.T) {
	t.Parallel()

	cm, _ := argvFixture(t)
	for _, op := range []string{"up", "down", "pull", "ps", "logs", "restart", "stop", "start"} {
		cmd, err := cm.buildCommand(t.Context(), ComposeRequest{StackName: "app", Operation: op})
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		for _, a := range cmd.Args {
			if a == "--volumes" || a == "-v" || a == "--rmi" || a == "--force-recreate" || a == "--build" {
				t.Errorf("%s: flag %q present with every option off: %q", op, a, cmd.Args)
			}
		}
		if op != "up" && op != "down" && slices.Contains(cmd.Args, "--remove-orphans") {
			t.Errorf("%s: --remove-orphans is only for up and down: %q", op, cmd.Args)
		}
	}

	// RemoveVolumes must not leak onto any other operation.
	for _, op := range []string{"up", "pull", "ps", "logs", "restart", "stop", "start"} {
		cmd, err := cm.buildCommand(t.Context(), ComposeRequest{StackName: "app", Operation: op, RemoveVolumes: true})
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		if slices.Contains(cmd.Args, "--volumes") {
			t.Errorf("%s: RemoveVolumes leaked --volumes: %q", op, cmd.Args)
		}
	}
}

// TestValidateRequest_RejectsEveryDenylistedEnvKey asserts all 14 keys, and
// that the denylist holds exactly those 14, so adding a key without a test
// (or dropping one) fails here.
func TestValidateRequest_RejectsEveryDenylistedEnvKey(t *testing.T) {
	t.Parallel()

	want := []string{
		"LD_PRELOAD", "LD_LIBRARY_PATH", "PATH", "DOCKER_HOST", "DOCKER_CONFIG",
		"DOCKER_CERT_PATH", "DOCKER_TLS_VERIFY", "DOCKER_CONTEXT", "HOME", "SHELL",
		"BASH_ENV", "ENV", "CDPATH", "IFS",
	}
	if len(want) != 14 {
		t.Fatalf("test lists %d keys, want 14", len(want))
	}
	if len(envVarDenylist) != len(want) {
		t.Errorf("envVarDenylist has %d keys, want %d", len(envVarDenylist), len(want))
	}

	cm := &ComposeManager{stacksDir: t.TempDir()}
	for _, key := range want {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			_, err := cm.validateRequest(ComposeRequest{StackName: "app", EnvVars: map[string]string{key: "x"}})
			if err == nil || !strings.Contains(err.Error(), "not allowed") {
				t.Fatalf("validateRequest(%s) error = %v, want a not-allowed rejection", key, err)
			}
		})
	}

	// A near-miss is still accepted, so the check is an exact match and not a
	// blanket rejection.
	if _, err := cm.validateRequest(ComposeRequest{StackName: "app", EnvVars: map[string]string{"PATH_EXTRA": "x"}}); err != nil {
		t.Errorf("PATH_EXTRA rejected: %v", err)
	}
}
