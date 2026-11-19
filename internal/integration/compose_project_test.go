// SPDX-License-Identifier: MIT

//go:build integration

package integration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/docker"
)

const (
	composeProjectLabel    = "com.docker.compose.project"
	composeWorkingDirLabel = "com.docker.compose.project.working_dir"

	composeStackYAML = "services:\n  web:\n    image: " + testImage + "\n    command: [\"sleep\", \"300\"]\n" +
		"  db:\n    image: " + testImage + "\n    command: [\"sleep\", \"300\"]\n"
)

// composeHarness drives ComposeManager and the docker CLI against the real
// daemon. Every resource it creates carries a unique prefix in its compose
// project name, and cleanup removes whatever that prefix matches.
type composeHarness struct {
	t      *testing.T
	socket string
	prefix string
	// dirs are every stack directory (raw and symlink-resolved) used, so cleanup
	// can find containers by working_dir label.
	dirs []string
}

func newComposeHarness(t *testing.T) *composeHarness {
	t.Helper()
	if out, err := exec.Command("docker", "compose", "version").CombinedOutput(); err != nil {
		t.Skipf("docker compose not available: %v\n%s", err, out)
	}
	socket := os.Getenv("PORTWING_TEST_DOCKER_SOCKET")
	if socket == "" {
		socket = strings.TrimPrefix(os.Getenv("DOCKER_HOST"), "unix://")
	}
	if socket == "" {
		socket = "/var/run/docker.sock"
	}
	// The NAS helper exports COMPOSE_PROJECT_NAME, which outranks both -p-less
	// naming and a file's "name:". Production hosts don't set it.
	t.Setenv("COMPOSE_PROJECT_NAME", "")
	_ = os.Unsetenv("COMPOSE_PROJECT_NAME")
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	h := &composeHarness{t: t, socket: socket, prefix: "pwadopt" + hex.EncodeToString(b)}
	t.Cleanup(h.cleanup)
	return h
}

func (h *composeHarness) docker(dir string, args ...string) string {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "DOCKER_HOST=unix://"+h.socket)
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// dockerQuiet is docker() that never fails the test; used by cleanup.
func (h *composeHarness) dockerQuiet(args ...string) string {
	cmd := exec.Command("docker", args...)
	cmd.Env = append(os.Environ(), "DOCKER_HOST=unix://"+h.socket)
	out, _ := cmd.CombinedOutput()
	return string(out)
}

func (h *composeHarness) cleanup() {
	// Containers by working_dir label, then by prefixed project name.
	for _, d := range h.dirs {
		h.rm(h.dockerQuiet("ps", "-aq", "--filter", "label="+composeWorkingDirLabel+"="+d))
	}
	projects := strings.Fields(h.dockerQuiet("ps", "-a", "--format", `{{.Label "`+composeProjectLabel+`"}}`))
	for _, p := range projects {
		if strings.Contains(p, h.prefix) {
			h.rm(h.dockerQuiet("ps", "-aq", "--filter", "label="+composeProjectLabel+"="+p))
		}
	}
	nets := strings.Fields(h.dockerQuiet("network", "ls", "--format", `{{.Name}}`))
	for _, n := range nets {
		if strings.Contains(n, h.prefix) {
			h.dockerQuiet("network", "rm", n)
		}
	}
	vols := strings.Fields(h.dockerQuiet("volume", "ls", "-q"))
	for _, v := range vols {
		if strings.Contains(v, h.prefix) {
			h.dockerQuiet("volume", "rm", v)
		}
	}
}

func (h *composeHarness) rm(ids string) {
	for _, id := range strings.Fields(ids) {
		h.dockerQuiet("rm", "-f", id)
	}
}

// stacksDir returns a fresh STACKS_DIR. With symlink it is a link to a real
// directory, which is how a symlinked STACKS_DIR looks in production.
func (h *composeHarness) stacksDir(symlink bool) (stacks, real string) {
	h.t.Helper()
	// Resolve the temp root so "real" is truly canonical (macOS /var is a
	// symlink to /private/var).
	base, err := filepath.EvalSymlinks(h.t.TempDir())
	if err != nil {
		h.t.Fatalf("EvalSymlinks: %v", err)
	}
	real = filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		h.t.Fatalf("mkdir: %v", err)
	}
	stacks = real
	if symlink {
		stacks = filepath.Join(base, "link")
		if err := os.Symlink(real, stacks); err != nil {
			h.t.Fatalf("symlink: %v", err)
		}
	}
	return stacks, real
}

func (h *composeHarness) writeStack(stacks, rel, yaml string) string {
	h.t.Helper()
	dir := filepath.Join(stacks, rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		h.t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(yaml), 0o644); err != nil {
		h.t.Fatalf("write compose.yaml: %v", err)
	}
	h.dirs = append(h.dirs, dir)
	if r, err := filepath.EvalSymlinks(dir); err == nil && r != dir {
		h.dirs = append(h.dirs, r)
	}
	return dir
}

func (h *composeHarness) manager(stacks string) *docker.ComposeManager {
	h.t.Helper()
	client, err := docker.NewClient(h.socket, 30)
	if err != nil {
		h.t.Fatalf("docker.NewClient: %v", err)
	}
	return docker.NewComposeManagerWithLister(stacks, client.GetAPIVersion(), h.socket, client)
}

func (h *composeHarness) run(cm *docker.ComposeManager, op, rel string) string {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	resp, err := cm.Execute(ctx, docker.ComposeRequest{Operation: op, StackName: rel, StackDir: rel})
	if err != nil {
		h.t.Fatalf("Execute %s %s: %v", op, rel, err)
	}
	if !resp.Success {
		h.t.Fatalf("Execute %s %s failed: %s\n%s", op, rel, resp.Error, resp.Output)
	}
	return resp.Output
}

type labelledContainer struct {
	ID, Project, WorkingDir, State string
}

// containersFor lists containers whose working_dir label is one of dirs.
func (h *composeHarness) containersFor(dirs ...string) []labelledContainer {
	h.t.Helper()
	var out []labelledContainer
	seen := map[string]bool{}
	for _, d := range dirs {
		if seen[d] {
			continue
		}
		seen[d] = true
		raw := h.docker("", "ps", "-a", "--no-trunc", "--filter", "label="+composeWorkingDirLabel+"="+d,
			"--format", `{{.ID}}|{{.Label "`+composeProjectLabel+`"}}|{{.Label "`+composeWorkingDirLabel+`"}}|{{.State}}`)
		for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
			if line == "" {
				continue
			}
			f := strings.Split(line, "|")
			out = append(out, labelledContainer{ID: f[0], Project: f[1], WorkingDir: f[2], State: f[3]})
		}
	}
	return out
}

func (h *composeHarness) projectsIn(cs []labelledContainer) map[string]bool {
	m := map[string]bool{}
	for _, c := range cs {
		m[c.Project] = true
	}
	return m
}

// TestComposeProjectNamesDoNotCollide: customer-a/<base> and customer-b/<base>
// share a directory basename but must run under different projects, and
// taking one down must leave the other running.
func TestComposeProjectNamesDoNotCollide(t *testing.T) {
	h := newComposeHarness(t)
	stacks, _ := h.stacksDir(false)
	base := h.prefix + "v2"
	dirA := h.writeStack(stacks, "customer-a/"+base, composeStackYAML)
	dirB := h.writeStack(stacks, "customer-b/"+base, composeStackYAML)
	cm := h.manager(stacks)

	h.run(cm, "up", "customer-a/"+base)
	h.run(cm, "up", "customer-b/"+base)

	a, b := h.containersFor(dirA), h.containersFor(dirB)
	if len(a) != 2 || len(b) != 2 {
		t.Fatalf("want 2 containers per stack, got a=%d b=%d", len(a), len(b))
	}
	pa, pb := h.projectsIn(a), h.projectsIn(b)
	if len(pa) != 1 || len(pb) != 1 {
		t.Fatalf("each stack must be one project: a=%v b=%v", pa, pb)
	}
	for p := range pa {
		if pb[p] {
			t.Fatalf("both stacks run under project %q", p)
		}
		if p == base {
			t.Fatalf("stack a used the basename project %q", p)
		}
	}

	h.run(cm, "down", "customer-b/"+base)
	if left := h.containersFor(dirB); len(left) != 0 {
		t.Fatalf("down left customer-b containers: %+v", left)
	}
	after := h.containersFor(dirA)
	if len(after) != 2 {
		t.Fatalf("down on customer-b changed customer-a: %+v", after)
	}
	for i := range after {
		if after[i].State != "running" {
			t.Fatalf("customer-a container %s is %s, want running", after[i].ID, after[i].State)
		}
	}
}

// TestComposeLegacyStackIsAdopted: a stack first deployed the pre-fix way (no
// -p, project = directory basename) is adopted by the new ComposeManager. The
// "link-*" cases cover a symlinked STACKS_DIR, where Docker's recorded
// working_dir can be the link path or the resolved path.
func TestComposeLegacyStackIsAdopted(t *testing.T) {
	cases := []struct {
		name        string
		symlink     bool
		legacyViaOK string // "stacks" deploys legacy via the STACKS_DIR path, "real" via the resolved path
	}{
		{"plain", false, "stacks"},
		{"symlink-legacy-via-link", true, "stacks"},
		{"symlink-legacy-via-real", true, "real"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newComposeHarness(t)
			stacks, real := h.stacksDir(tc.symlink)
			base := h.prefix + "v2"
			rel := "customer-a/" + base
			dir := h.writeStack(stacks, rel, composeStackYAML)
			legacyDir := dir
			if tc.legacyViaOK == "real" {
				legacyDir = filepath.Join(real, rel)
			}

			// Pre-fix behavior: --project-directory, no -p.
			h.docker(legacyDir, "compose", "--project-directory", legacyDir, "up", "-d", "--remove-orphans")
			before := h.containersFor(dir, legacyDir)
			if len(before) != 2 {
				t.Fatalf("legacy deploy: want 2 containers, got %+v", before)
			}
			for _, c := range before {
				if c.Project != base {
					t.Fatalf("legacy project = %q, want basename %q", c.Project, base)
				}
			}
			t.Logf("working_dir label recorded by Docker: %q (STACKS_DIR %q, legacy dir %q)", before[0].WorkingDir, stacks, legacyDir)

			cm := h.manager(stacks)
			h.run(cm, "up", rel)
			ps := h.run(cm, "ps", rel)

			after := h.containersFor(dir, legacyDir)
			if len(after) != 2 {
				t.Fatalf("after adoption want 2 containers (no copy, no orphan removal), got %+v", after)
			}
			ids := map[string]bool{}
			for _, c := range before {
				ids[c.ID] = true
			}
			for _, c := range after {
				if !ids[c.ID] {
					t.Fatalf("container %s was recreated, not adopted", c.ID)
				}
				if c.Project != base || c.State != "running" {
					t.Fatalf("container %s project=%q state=%q, want %q running", c.ID, c.Project, c.State, base)
				}
			}
			if !strings.Contains(ps, "web") || !strings.Contains(ps, "db") {
				t.Fatalf("ps did not show both services under the adopted project:\n%s", ps)
			}

			ls := h.docker("", "compose", "ls", "-a", "--format", "json")
			if !strings.Contains(ls, `"Name":"`+base+`"`) {
				t.Fatalf("compose ls does not list project %q:\n%s", base, ls)
			}
			// No second copy under any other project name from this run.
			all := strings.Fields(h.docker("", "ps", "-aq", "--filter", "name="+h.prefix))
			if len(all) != 2 {
				t.Fatalf("want exactly 2 containers named with %q, got %d", h.prefix, len(all))
			}
		})
	}
}

// TestComposeAdoptionDoesNotCrossDirectories: a legacy "<base>" project from
// customer-a must not be adopted by customer-b's same-named directory.
func TestComposeAdoptionDoesNotCrossDirectories(t *testing.T) {
	h := newComposeHarness(t)
	stacks, _ := h.stacksDir(false)
	base := h.prefix + "v2"
	dirA := h.writeStack(stacks, "customer-a/"+base, composeStackYAML)
	dirB := h.writeStack(stacks, "customer-b/"+base, composeStackYAML)

	h.docker(dirA, "compose", "--project-directory", dirA, "up", "-d")
	legacy := h.containersFor(dirA)
	if len(legacy) != 2 {
		t.Fatalf("legacy deploy: want 2 containers, got %+v", legacy)
	}

	cm := h.manager(stacks)
	h.run(cm, "up", "customer-b/"+base)

	b := h.containersFor(dirB)
	if len(b) != 2 {
		t.Fatalf("customer-b: want 2 containers, got %+v", b)
	}
	for _, c := range b {
		if c.Project == base {
			t.Fatalf("customer-b adopted customer-a's project %q", base)
		}
	}
	a := h.containersFor(dirA)
	if len(a) != 2 {
		t.Fatalf("customer-a containers changed: %+v", a)
	}
	for i := range a {
		if a[i].Project != base || a[i].State != "running" || !containsID(legacy, a[i].ID) {
			t.Fatalf("customer-a container disturbed: %+v (was %+v)", a[i], legacy)
		}
	}
}

func containsID(cs []labelledContainer, id string) bool {
	for _, c := range cs {
		if c.ID == id {
			return true
		}
	}
	return false
}

// TestComposeExplicitNameIsKept: a top-level "name:" in the compose file wins
// over derived naming.
func TestComposeExplicitNameIsKept(t *testing.T) {
	h := newComposeHarness(t)
	stacks, _ := h.stacksDir(false)
	custom := h.prefix + "-custom"
	dir := h.writeStack(stacks, "customer-a/v2", "name: "+custom+"\n"+composeStackYAML)
	cm := h.manager(stacks)

	h.run(cm, "up", "customer-a/v2")
	cs := h.containersFor(dir)
	if len(cs) != 2 {
		t.Fatalf("want 2 containers, got %+v", cs)
	}
	for _, c := range cs {
		if c.Project != custom {
			t.Fatalf("project = %q, want explicit name %q", c.Project, custom)
		}
	}
	h.run(cm, "down", "customer-a/v2")
	if left := h.containersFor(dir); len(left) != 0 {
		t.Fatalf("down left containers: %+v", left)
	}
}
