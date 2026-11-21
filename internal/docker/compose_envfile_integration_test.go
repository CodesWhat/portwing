// SPDX-License-Identifier: MIT

//go:build integration

package docker

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEnvFileRoundTripsThroughDockerComposeConfig runs the real `docker
// compose config` against a file renderEnvFile wrote and checks every value
// comes out of interpolation unchanged. It needs the compose plugin but no
// daemon, and skips without it.
func TestEnvFileRoundTripsThroughDockerComposeConfig(t *testing.T) {
	if out, err := exec.Command("docker", "compose", "version").CombinedOutput(); err != nil {
		t.Skipf("docker compose not available: %v\n%s", err, out)
	}

	vars := map[string]string{}
	names := map[string]string{}
	for i, v := range envRoundTripValues {
		name := "RT_" + string(rune('A'+i/26)) + string(rune('A'+i%26))
		vars[name] = v
		names[name] = v
	}
	data, err := renderEnvFile(vars)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env.drydock")
	if err := os.WriteFile(envFile, data, 0o600); err != nil {
		t.Fatal(err)
	}
	// `config` re-escapes every literal $ as $$ so its output can be loaded
	// again; the comparison below undoes that one level.
	yaml := "services:\n  app:\n    image: busybox\n    environment:\n"
	for name := range vars {
		yaml += "      " + name + ": ${" + name + "}\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "compose", "--env-file", envFile, "config", "--format", "json")
	cmd.Dir = dir
	// Keep ambient variables from satisfying a name we failed to define.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose config: %v\n%s", err, out)
	}
	var cfg struct {
		Services map[string]struct {
			Environment map[string]string `json:"environment"`
		} `json:"services"`
	}
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("decode config: %v\n%s", err, out)
	}
	got := cfg.Services["app"].Environment
	for name, want := range names {
		if unescaped := strings.ReplaceAll(got[name], "$$", "$"); unescaped != want {
			t.Errorf("%s = %q (config output %q), want %q", name, unescaped, got[name], want)
		}
	}
}
