// SPDX-License-Identifier: MIT

//go:build integration

package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/docker"
)

// TestComposeBuildRunsShippedScript ships an entrypoint script in the stack and
// runs it during "up --build". Without a mode the file is 0600, COPY keeps
// that, and the RUN fails with "Permission denied" (reproduced before the
// per-file mode existed).
func TestComposeBuildRunsShippedScript(t *testing.T) {
	h := newComposeHarness(t)
	stacks, _ := h.stacksDir(false)
	rel := h.prefix + "script"
	h.writeStack(stacks, rel, "services:\n  app:\n    build: .\n    command: [\"sleep\", \"60\"]\n")
	cm := h.manager(stacks)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	resp, err := cm.Execute(ctx, docker.ComposeRequest{
		Operation: "up", StackName: rel, StackDir: rel, Build: true,
		Files: map[string]string{
			"Dockerfile":    "FROM " + testImage + "\nCOPY entrypoint.sh /entrypoint.sh\nRUN /entrypoint.sh\n",
			"entrypoint.sh": "#!/bin/sh\necho ran\n",
		},
		FileModes: map[string]os.FileMode{"entrypoint.sh": 0o755},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !resp.Success {
		t.Fatalf("build with shipped script failed: %s\n%s", resp.Error, resp.Output)
	}
}

// TestComposeBuildConfigReadableByNonRoot ships a config file and reads it as
// uid 1000 in the container. Without a mode the file is 0600 and root owned in
// the image, so the read fails (reproduced before the per-file mode existed).
func TestComposeBuildConfigReadableByNonRoot(t *testing.T) {
	h := newComposeHarness(t)
	stacks, _ := h.stacksDir(false)
	rel := h.prefix + "conf"
	h.writeStack(stacks, rel, "services:\n  app:\n    build: .\n    command: [\"sleep\", \"60\"]\n")
	cm := h.manager(stacks)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	resp, err := cm.Execute(ctx, docker.ComposeRequest{
		Operation: "up", StackName: rel, StackDir: rel, Build: true,
		Files: map[string]string{
			"Dockerfile": "FROM " + testImage + "\nCOPY app.conf /app.conf\nUSER 1000\nRUN cat /app.conf\n",
			"app.conf":   "key=value\n",
		},
		FileModes: map[string]os.FileMode{"app.conf": 0o644},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !resp.Success {
		t.Fatalf("non-root read of shipped config failed: %s\n%s", resp.Error, resp.Output)
	}
}
