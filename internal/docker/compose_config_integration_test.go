// SPDX-License-Identifier: MIT

//go:build integration

package docker

// compose_config_integration_test.go is the parse-only compose tier: it writes
// stacks the way Execute does (writeStackFiles) and hands them to the real
// `docker compose config`, which parses and interpolates without a daemon.
// Each test skips when the compose plugin is missing.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireComposeCLI(t *testing.T) {
	t.Helper()
	if out, err := exec.Command("docker", "compose", "version").CombinedOutput(); err != nil {
		t.Skipf("docker compose not available: %v\n%s", err, out)
	}
}

type composeConfigOutput struct {
	Name     string `json:"name"`
	Services map[string]struct {
		Environment map[string]string `json:"environment"`
	} `json:"services"`
}

// composeConfig runs the real `docker compose config` for a stack with the
// arguments buildCommand would use for any operation (project name, project
// directory, env file), and returns the parsed result.
func composeConfig(t *testing.T, cm *ComposeManager, stackDir string) composeConfigOutput {
	t.Helper()
	cmd, err := cm.buildCommand(t.Context(), ComposeRequest{StackName: "s", StackDir: stackDir, Operation: "ps"})
	if err != nil {
		t.Fatal(err)
	}
	// Swap the trailing "ps --format json" for a parse-only "config".
	args := append(append([]string(nil), cmd.Args[:len(cmd.Args)-3]...), "config", "--format", "json")
	// #nosec G204 -- test-only invocation of the local docker CLI with args built above.
	run := exec.CommandContext(t.Context(), args[0], args[1:]...)
	// Keep ambient variables (COMPOSE_PROJECT_NAME, a stray API variable) from
	// answering for a name Portwing failed to define.
	run.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose config: %v\n%s", err, out)
	}
	var parsed composeConfigOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("decode config: %v\n%s", err, out)
	}
	return parsed
}

// writeComposeRequest writes req's files and env the way Execute does.
func writeComposeRequest(t *testing.T, cm *ComposeManager, req ComposeRequest) {
	t.Helper()
	if _, err := cm.validateRequest(req); err != nil {
		t.Fatalf("validateRequest: %v", err)
	}
	if err := cm.writeStackFiles(req); err != nil {
		t.Fatalf("writeStackFiles: %v", err)
	}
}

func newConfigManager(t *testing.T) *ComposeManager {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &ComposeManager{stacksDir: root, composeBin: "docker", isV2: true}
}

// Env vars arrive in .env.drydock, which Portwing passes as --env-file. Every
// value in the quoting corpus has to come out of interpolation byte for byte.
func TestComposeConfigStackEnvQuoting(t *testing.T) {
	requireComposeCLI(t)
	cm := newConfigManager(t)

	vars := map[string]string{}
	var yaml strings.Builder
	yaml.WriteString("services:\n  app:\n    image: busybox\n    environment:\n")
	for i, v := range envRoundTripValues {
		name := "Q_" + string(rune('A'+i/26)) + string(rune('A'+i%26))
		vars[name] = v
		yaml.WriteString("      " + name + ": ${" + name + "}\n")
	}
	req := ComposeRequest{
		Operation: "up", StackName: "quoting", EnvVars: vars,
		Files: map[string]string{"compose.yaml": yaml.String()},
	}
	writeComposeRequest(t, cm, req)

	got := composeConfig(t, cm, "quoting").Services["app"].Environment
	for name, want := range vars {
		// `config` re-escapes a literal $ as $$ so its output can be loaded
		// again; undo that one level.
		if unescaped := strings.ReplaceAll(got[name], "$$", "$"); unescaped != want {
			t.Errorf("%s = %q (config output %q), want %q", name, unescaped, got[name], want)
		}
	}
}

// A shipped env file, with the per-file mode form, is readable by Compose, and
// the modes land on disk masked as documented.
func TestComposeConfigPerFileModes(t *testing.T) {
	requireComposeCLI(t)
	cm := newConfigManager(t)

	req := ComposeRequest{Operation: "up", StackName: "modes"}
	if err := req.UnmarshalJSON([]byte(`{
		"operation": "up", "stackName": "modes",
		"files": {
			"compose.yaml": "services:\n  app:\n    image: busybox\n    env_file: ./extra.env\n    configs: [appconf]\nconfigs:\n  appconf:\n    file: ./app.conf\n",
			"extra.env": {"content": "FROM_FILE=shipped\n", "mode": "0644"},
			"app.conf": {"content": "k=v\n", "mode": 420},
			"run.sh": {"content": "#!/bin/sh\n", "mode": "04755"},
			"plain.txt": "x"
		}
	}`)); err != nil {
		t.Fatal(err)
	}
	writeComposeRequest(t, cm, req)

	dir := filepath.Join(cm.stacksDir, "modes")
	wantModes := map[string]os.FileMode{
		"compose.yaml": 0o600, // no mode given: the default
		"extra.env":    0o644,
		"app.conf":     0o644,
		"run.sh":       0o755, // setuid bit masked off
		"plain.txt":    0o600,
	}
	for name, want := range wantModes {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if got := info.Mode().Perm(); got != want || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			t.Errorf("%s mode = %v, want %v with no special bits", name, info.Mode(), want)
		}
	}

	if got := composeConfig(t, cm, "modes").Services["app"].Environment["FROM_FILE"]; got != "shipped" {
		t.Errorf("FROM_FILE = %q, want the value from the shipped env_file", got)
	}
}

// Project names: same-basename stacks in different directories resolve to
// different projects, each matching deriveProjectName, and an explicit name:
// in the shipped file is left alone.
func TestComposeConfigProjectNaming(t *testing.T) {
	requireComposeCLI(t)
	cm := newConfigManager(t)

	const minimal = "services:\n  web:\n    image: nginx:alpine\n"
	for _, stack := range []struct{ dir, yaml string }{
		{"customer-a/v2", minimal},
		{"customer-b/v2", minimal},
		{"named/v2", "name: custom-name\n" + minimal},
		{"Odd Dir_1", minimal},
	} {
		writeComposeRequest(t, cm, ComposeRequest{
			Operation: "up", StackName: stack.dir, StackDir: stack.dir,
			Files: map[string]string{"compose.yaml": stack.yaml},
		})
	}

	a := composeConfig(t, cm, "customer-a/v2").Name
	b := composeConfig(t, cm, "customer-b/v2").Name
	if a == b {
		t.Fatalf("customer-a/v2 and customer-b/v2 share project %q", a)
	}
	if want := deriveProjectName("customer-a/v2"); a != want {
		t.Errorf("customer-a/v2 project = %q, want %q", a, want)
	}
	if got := composeConfig(t, cm, "named/v2").Name; got != "custom-name" {
		t.Errorf("explicit name: project = %q, want custom-name", got)
	}
	if got, want := composeConfig(t, cm, "Odd Dir_1").Name, deriveProjectName("Odd Dir_1"); got != want {
		t.Errorf("odd directory name: project = %q, want %q", got, want)
	}
}
