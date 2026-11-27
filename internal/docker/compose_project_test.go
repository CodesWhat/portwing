// SPDX-License-Identifier: MIT

package docker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var validProjectName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

type fakeLister struct {
	containers []ContainerJSON
	err        error
}

func (f fakeLister) ListContainers(context.Context, bool) ([]ContainerJSON, error) {
	return f.containers, f.err
}

// projectFlag returns the value following -p in args, or "" when absent.
func projectFlag(args []string) (string, bool) {
	for i, a := range args {
		if a == "-p" && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func writeStack(t *testing.T, root, rel, file, content string) string {
	t.Helper()
	dir := filepath.Join(root, rel)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if file != "" {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestDeriveProjectName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		rel        string
		wantPrefix string
	}{
		{"simple", "myapp", "myapp-"},
		{"nested", "customer-a/v2", "customer-a-v2-"},
		{"uppercase", "MyApp", "myapp-"},
		{"invalid chars", "My App!/web.v2", "my-app-web-v2-"},
		{"underscores collapse to dash", "a__b", "a-b-"},
		{"leading separators trimmed", "-._x", "x-"},
		{"only invalid chars", "!!!", "stack-"},
		{"dot", ".", "stack-"},
		{"non-ascii", "café/∑", "caf-"},
		{"unclean path", "a//b/../c/", "a-c-"},
		{"long prefix capped", strings.Repeat("abcdefghij/", 20), "abcdefghij-abcdefghij-abcdefghij-abcdefghij-"[:40]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := deriveProjectName(tt.rel)
			if !validProjectName.MatchString(got) {
				t.Fatalf("deriveProjectName(%q) = %q, not a valid project name", tt.rel, got)
			}
			if !strings.HasPrefix(got, tt.wantPrefix) {
				t.Fatalf("deriveProjectName(%q) = %q, want prefix %q", tt.rel, got, tt.wantPrefix)
			}
			if again := deriveProjectName(tt.rel); again != got {
				t.Fatalf("not deterministic: %q then %q", got, again)
			}
		})
	}
}

func TestDeriveProjectName_DistinctPathsNeverCollide(t *testing.T) {
	t.Parallel()

	// Several of these normalize to the same readable prefix; the path hash
	// has to keep them apart. Same-basename nested stacks are the original bug.
	paths := []string{
		"a/b-c", "a-b/c", "a/b/c", "a_b/c", "A/B-C", "a/b.c", "a b/c",
		"customer-a/v2", "customer-b/v2", "v2", "x/v2", "x/V2",
	}
	seen := map[string]string{}
	for _, p := range paths {
		name := deriveProjectName(p)
		if prev, dup := seen[name]; dup {
			t.Fatalf("%q and %q both map to %q", prev, p, name)
		}
		seen[name] = p
	}
}

func TestLegacyProjectName(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"/stacks/customer-a/v2": "v2",
		"/stacks/My.App":        "myapp",
		"/stacks/_web":          "web",
		"/stacks/app_1-b":       "app_1-b",
	}
	for dir, want := range tests {
		if got := legacyProjectName(dir); got != want {
			t.Errorf("legacyProjectName(%q) = %q, want %q", dir, got, want)
		}
	}
}

func TestComposeFileName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
		want string
		err  bool
	}{
		{"plain", "name: custom\nservices: {}\n", "custom", false},
		{"after services", "services: {}\nname: custom\n", "custom", false},
		{"quoted key", "\"name\": custom\n", "custom", false},
		{"quoted value", "name: \"custom\"\n", "custom", false},
		{"interpolated", "name: ${STACK}\n", "${STACK}", false},
		{"crlf", "name: custom\r\nservices: {}\r\n", "custom", false},
		{"indented root", "  name: custom\n  services: {}\n", "custom", false},
		{"next-line scalar", "name:\n  custom\nservices: {}\n", "custom", false},
		{"flow mapping", "{name: custom, services: {}}\n", "custom", false},
		{"document marker", "---\nname: custom\n", "custom", false},
		{"service named name", "services:\n  name:\n    image: x\n", "", false},
		{"nested key", "services:\n  web:\n    name: foo\n", "", false},
		{"container_name", "container_name: x\n", "", false},
		{"null value", "name:\nservices: {}\n", "", false},
		{"empty string", "name: \"\"\n", "", false},
		{"comment only value", "name: # none\n", "", false},
		{"commented out", "# name: custom\n", "", false},
		{"prefix key", "names: x\n", "", false},
		{"empty", "", "", false},
		{"anchor and alias", "x-n: &n custom\nname: *n\n", "custom", false},
		{"merge key", "base: &b\n  name: custom\n<<: *b\n", "custom", false},
		{"numeric name", "name: 123\n", "", false},
		{"duplicate name key", "name: a\nname: b\n", "", true},
		{"duplicate other key", "services: {}\nservices: {}\n", "", true},
		{"tab indent", "services:\n\tweb: {}\n", "", true},
		{"multi document", "name: a\n---\nname: b\n", "a", false},
		{"invalid yaml", "name: [unterminated\n", "", true},
		{"root is a list", "- name: custom\n", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := composeFileName([]byte(tt.yaml))
			if (err != nil) != tt.err {
				t.Fatalf("composeFileName(%q) error = %v, want error %v", tt.yaml, err, tt.err)
			}
			if got != tt.want {
				t.Fatalf("composeFileName(%q) = %q, want %q", tt.yaml, got, tt.want)
			}
		})
	}
}

func TestStackDeclaresName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		files map[string]string
		want  bool
	}{
		{"no files", nil, false},
		{"compose.yaml name", map[string]string{"compose.yaml": "name: custom\n"}, true},
		{"compose.yml name", map[string]string{"compose.yml": "name: custom\n"}, true},
		{"docker-compose.yaml name", map[string]string{"docker-compose.yaml": "name: custom\n"}, true},
		{"docker-compose.yml name", map[string]string{"docker-compose.yml": "name: custom\n"}, true},
		{"override sets name", map[string]string{"compose.yaml": "services: {}\n", "compose.override.yaml": "name: custom\n"}, true},
		{"no name anywhere", map[string]string{"compose.yaml": "services: {}\n", "compose.override.yaml": "services: {}\n"}, false},
		{"unrelated file ignored", map[string]string{"other.yaml": "name: custom\n"}, false},
		{"stale lower-priority file ignored", map[string]string{"compose.yaml": "services: {}\n", "docker-compose.yml": "name: custom\n"}, false},
		{"stale lowest-priority file ignored", map[string]string{"compose.yml": "services: {}\n", "docker-compose.yaml": "name: custom\n"}, false},
		{"stale lower-priority override ignored", map[string]string{"compose.yaml": "services: {}\n", "compose.override.yml": "services: {}\n", "docker-compose.override.yml": "name: custom\n"}, false},
		{"first base wins", map[string]string{"compose.yaml": "name: custom\n", "docker-compose.yml": "services: {}\n"}, true},
		{"indented root", map[string]string{"compose.yaml": "  name: custom\n"}, true},
		{"next-line scalar", map[string]string{"compose.yaml": "name:\n  custom\n"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			dir := writeStack(t, root, "grp/app", "", "")
			for f, c := range tt.files {
				if err := os.WriteFile(filepath.Join(dir, f), []byte(c), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cm := &ComposeManager{stacksDir: root, composeBin: "docker", isV2: true}
			got, err := cm.stackDeclaresName("grp/app")
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("stackDeclaresName = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStackDeclaresName_UnparseableFails(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeStack(t, root, "grp/app", "compose.yaml", "name: [unterminated\n")
	cm := &ComposeManager{stacksDir: root, composeBin: "docker", isV2: true}
	_, err := cm.stackDeclaresName("grp/app")
	if err == nil || !strings.Contains(err.Error(), "parsing compose.yaml") {
		t.Fatalf("err = %v, want a parsing compose.yaml error", err)
	}
	if _, err := cm.projectNameFor(t.Context(), "grp/app", filepath.Join(root, "grp/app")); err == nil {
		t.Fatal("projectNameFor accepted an unparseable compose file")
	}
}

func TestBuildCommand_ProjectName(t *testing.T) {
	t.Parallel()

	const stackDir = "customer-a/v2"

	tests := []struct {
		name     string
		compose  string
		lister   ContainerLister
		wantName func(projectDir string) string // "" means no -p
		wantErr  bool
	}{
		{
			name:     "no name, no lister: derived",
			wantName: func(string) string { return deriveProjectName(stackDir) },
		},
		{
			name:     "explicit name suppresses -p",
			compose:  "name: custom\nservices: {}\n",
			wantName: func(string) string { return "" },
		},
		{
			name:    "explicit name wins over legacy containers",
			compose: "name: custom\nservices: {}\n",
			lister: fakeLister{containers: []ContainerJSON{{Labels: map[string]string{
				composeProjectLabel: "v2", composeWorkingDirLabel: "WORKDIR",
			}}}},
			wantName: func(string) string { return "" },
		},
		{
			name: "legacy containers with matching working_dir: basename adopted",
			lister: fakeLister{containers: []ContainerJSON{{Labels: map[string]string{
				composeProjectLabel: "v2", composeWorkingDirLabel: "WORKDIR",
			}}}},
			wantName: func(string) string { return "v2" },
		},
		{
			name: "same project label, other working_dir: not adopted",
			lister: fakeLister{containers: []ContainerJSON{{Labels: map[string]string{
				composeProjectLabel: "v2", composeWorkingDirLabel: "/stacks/customer-b/v2",
			}}}},
			wantName: func(string) string { return deriveProjectName(stackDir) },
		},
		{
			name: "matching working_dir, other project label: not adopted",
			lister: fakeLister{containers: []ContainerJSON{{Labels: map[string]string{
				composeProjectLabel: "something-else", composeWorkingDirLabel: "WORKDIR",
			}}}},
			wantName: func(string) string { return deriveProjectName(stackDir) },
		},
		{
			name:     "unlabeled containers ignored",
			lister:   fakeLister{containers: []ContainerJSON{{Labels: nil}, {Labels: map[string]string{composeProjectLabel: "v2"}}}},
			wantName: func(string) string { return deriveProjectName(stackDir) },
		},
		{
			name:    "lister error fails closed",
			lister:  fakeLister{err: errors.New("daemon down")},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			file := ""
			if tt.compose != "" {
				file = "compose.yaml"
			}
			dir := writeStack(t, root, stackDir, file, tt.compose)
			abs, err := filepath.Abs(dir)
			if err != nil {
				t.Fatal(err)
			}

			lister := tt.lister
			if fl, ok := lister.(fakeLister); ok {
				for i := range fl.containers {
					if fl.containers[i].Labels[composeWorkingDirLabel] == "WORKDIR" {
						fl.containers[i].Labels[composeWorkingDirLabel] = abs
					}
				}
				lister = fl
			}
			cm := &ComposeManager{stacksDir: root, composeBin: "docker", isV2: true, lister: lister}

			cmd, err := cm.buildCommand(t.Context(), ComposeRequest{StackName: "app", StackDir: stackDir, Operation: "up"})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, present := projectFlag(cmd.Args)
			want := tt.wantName(abs)
			if want == "" {
				if present {
					t.Fatalf("-p %q passed, want none; args %v", got, cmd.Args)
				}
				return
			}
			if got != want {
				t.Fatalf("-p = %q (present=%v), want %q; args %v", got, present, want, cmd.Args)
			}
		})
	}
}

func TestBuildCommand_EveryOperationGetsSameProjectName(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeStack(t, root, "customer-a/v2", "", "")
	cm := &ComposeManager{stacksDir: root, composeBin: "docker", isV2: true}

	want := deriveProjectName("customer-a/v2")
	for _, op := range []string{"up", "down", "pull", "ps", "logs", "restart", "stop", "start"} {
		t.Run(op, func(t *testing.T) {
			t.Parallel()
			cmd, err := cm.buildCommand(t.Context(), ComposeRequest{StackName: "v2", StackDir: "customer-a/v2", Operation: op})
			if err != nil {
				t.Fatal(err)
			}
			got, present := projectFlag(cmd.Args)
			if !present || got != want {
				t.Fatalf("%s: -p = %q (present=%v), want %q; args %v", op, got, present, want, cmd.Args)
			}
			// -p is a global flag and must precede the subcommand.
			for i, a := range cmd.Args {
				if a == op && i < len(cmd.Args) {
					for j, b := range cmd.Args {
						if b == "-p" && j > i {
							t.Fatalf("%s: -p after subcommand: %v", op, cmd.Args)
						}
					}
				}
			}
		})
	}
}

func TestBuildCommand_V1BinaryGetsProjectName(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeStack(t, root, "app", "", "")
	cm := &ComposeManager{stacksDir: root, composeBin: "docker-compose", isV2: false}
	cmd, err := cm.buildCommand(t.Context(), ComposeRequest{StackName: "app", Operation: "ps"})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := projectFlag(cmd.Args); !ok || got != deriveProjectName("app") {
		t.Fatalf("args %v", cmd.Args)
	}
}

func TestProjectNameFor_AdoptionLoggedOnce(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := writeStack(t, root, "app", "", "")
	abs, _ := filepath.Abs(dir)
	cm := &ComposeManager{
		stacksDir: root, composeBin: "docker", isV2: true,
		lister: fakeLister{containers: []ContainerJSON{{Labels: map[string]string{
			composeProjectLabel: "app", composeWorkingDirLabel: abs,
		}}}},
	}
	for range 3 {
		name, err := cm.projectNameFor(t.Context(), "app", abs)
		if err != nil || name != "app" {
			t.Fatalf("projectNameFor = %q, %v", name, err)
		}
	}
	if _, ok := cm.adoptionLogged.Load(abs); !ok {
		t.Fatal("adoption not recorded")
	}
}

// composeConfigProject runs the real "docker compose config" for a stack the
// way buildCommand would invoke any other operation, and returns the project
// name Compose resolves.
func composeConfigProject(t *testing.T, cm *ComposeManager, stackDir string) string {
	t.Helper()
	cmd, err := cm.buildCommand(t.Context(), ComposeRequest{StackName: "s", StackDir: stackDir, Operation: "ps"})
	if err != nil {
		t.Fatal(err)
	}
	// Swap the trailing "ps --format json" for a parse-only "config".
	args := cmd.Args[:len(cmd.Args)-3]
	args = append(args, "config", "--format", "json")
	// #nosec G204 -- test-only invocation of the local docker CLI with args built above.
	out, err := exec.CommandContext(t.Context(), args[0], args[1:]...).Output()
	if err != nil {
		t.Fatalf("docker compose config: %v", err)
	}
	var parsed struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("parsing config output: %v", err)
	}
	return parsed.Name
}

// TestComposeConfig_RealCLI runs the real docker CLI's parse-only "config"
// (no daemon needed) to prove nested same-basename stacks get distinct
// project names and an explicit name: is kept. Skipped without the compose
// plugin.
func TestComposeConfig_RealCLI(t *testing.T) {
	if out, err := exec.Command("docker", "compose", "version").CombinedOutput(); err != nil {
		t.Skipf("docker compose plugin unavailable: %v (%s)", err, strings.TrimSpace(string(out)))
	}

	root := t.TempDir()
	const minimal = "services:\n  web:\n    image: nginx:alpine\n"
	writeStack(t, root, "customer-a/v2", "compose.yaml", minimal)
	writeStack(t, root, "customer-b/v2", "compose.yaml", minimal)
	writeStack(t, root, "named/v2", "compose.yaml", "name: custom\n"+minimal)
	cm := &ComposeManager{stacksDir: root, composeBin: "docker", isV2: true}

	a := composeConfigProject(t, cm, "customer-a/v2")
	b := composeConfigProject(t, cm, "customer-b/v2")
	if a == b {
		t.Fatalf("customer-a/v2 and customer-b/v2 both resolve to project %q", a)
	}
	if want := deriveProjectName("customer-a/v2"); a != want {
		t.Fatalf("customer-a/v2 project = %q, want %q", a, want)
	}
	if got := composeConfigProject(t, cm, "named/v2"); got != "custom" {
		t.Fatalf("explicit name: project = %q, want custom", got)
	}
}

// TestComposeNameDetection_MatchesRealCLI checks that stackDeclaresName agrees
// with what the real docker CLI does for the same files: a declared name must
// reach the CLI untouched (project "custom"), an undeclared one gets the derived
// -p name. Each stack directory is called "v2" so a missed explicit name can't
// hide behind the basename default.
func TestComposeNameDetection_MatchesRealCLI(t *testing.T) {
	if out, err := exec.Command("docker", "compose", "version").CombinedOutput(); err != nil {
		t.Skipf("docker compose plugin unavailable: %v (%s)", err, strings.TrimSpace(string(out)))
	}

	const svc = "services:\n  web:\n    image: nginx:alpine\n"
	tests := []struct {
		name      string
		files     map[string]string
		wantExtra bool // an explicit name is expected to win
	}{
		{"plain", map[string]string{"compose.yaml": "name: custom\n" + svc}, true},
		{"indented root", map[string]string{"compose.yaml": "  name: custom\n  services:\n    web:\n      image: nginx:alpine\n"}, true},
		{"next-line scalar", map[string]string{"compose.yaml": "name:\n  custom\n" + svc}, true},
		{"quoted name", map[string]string{"compose.yaml": "name: \"custom\"\n" + svc}, true},
		{"stale unused file", map[string]string{"compose.yaml": svc, "docker-compose.yml": "name: custom\n" + svc}, false},
		{"override sets name", map[string]string{"compose.yaml": svc, "compose.override.yaml": "name: custom\n"}, true},
		{"override beats base", map[string]string{"compose.yaml": "name: base\n" + svc, "compose.override.yaml": "name: custom\n"}, true},
		{"stale unused override", map[string]string{"compose.yaml": svc, "compose.override.yml": svc, "docker-compose.override.yml": "name: custom\n"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			dir := writeStack(t, root, "grp/v2", "", "")
			for f, c := range tt.files {
				if err := os.WriteFile(filepath.Join(dir, f), []byte(c), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cm := &ComposeManager{stacksDir: root, composeBin: "docker", isV2: true}
			declared, err := cm.stackDeclaresName("grp/v2")
			if err != nil {
				t.Fatal(err)
			}
			if declared != tt.wantExtra {
				t.Fatalf("stackDeclaresName = %v, want %v", declared, tt.wantExtra)
			}
			want := deriveProjectName("grp/v2")
			if declared {
				want = "custom"
			}
			if got := composeConfigProject(t, cm, "grp/v2"); got != want {
				t.Fatalf("CLI project = %q, want %q", got, want)
			}
		})
	}
}

// TestExecute_FakeBinaryReceivesProjectName drives Execute end to end through
// a fake compose binary that records its arguments, for each operation, and
// checks the recorded command line carries -p with the derived name.
func TestExecute_FakeBinaryReceivesProjectName(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeStack(t, root, "customer-a/v2", "compose.yaml", "services: {}\n")
	binDir := t.TempDir()
	calls := filepath.Join(binDir, "calls")
	fake := filepath.Join(binDir, "fake-compose.sh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + calls + "'\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cm := &ComposeManager{stacksDir: root, composeBin: fake}

	ops := []string{"up", "down", "pull", "ps", "logs", "restart", "stop", "start"}
	for _, op := range ops {
		resp, err := cm.Execute(t.Context(), ComposeRequest{StackName: "v2", StackDir: "customer-a/v2", Operation: op})
		if err != nil || !resp.Success {
			t.Fatalf("%s: resp=%+v err=%v", op, resp, err)
		}
	}
	data, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != len(ops) {
		t.Fatalf("got %d recorded calls, want %d: %q", len(lines), len(ops), lines)
	}
	want := "-p " + deriveProjectName("customer-a/v2") + " "
	for i, line := range lines {
		if !strings.Contains(line, want) {
			t.Errorf("%s: recorded %q lacks %q", ops[i], line, want)
		}
	}
}

func TestStackDeclaresName_RejectsEscapingStackDir(t *testing.T) {
	t.Parallel()

	cm := &ComposeManager{stacksDir: t.TempDir(), composeBin: "docker", isV2: true}
	if _, err := cm.stackDeclaresName("../outside"); err == nil || !strings.Contains(err.Error(), "escapes stacks directory") {
		t.Fatalf("err = %v, want an escapes-stacks-directory error", err)
	}
}

// A directory named like a compose file isn't one Compose would load, so it is
// skipped and the next candidate in the group is read instead.
func TestStackDeclaresName_NonRegularFileSkipped(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := writeStack(t, root, "grp/app", "docker-compose.yml", "name: custom\n")
	if err := os.Mkdir(filepath.Join(dir, "compose.yaml"), 0o750); err != nil {
		t.Fatal(err)
	}
	cm := &ComposeManager{stacksDir: root, composeBin: "docker", isV2: true}
	got, err := cm.stackDeclaresName("grp/app")
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("stackDeclaresName = false, want true from the fallback file")
	}
}

func TestStackDeclaresName_UnreadableFileFails(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("file permissions don't apply to root")
	}
	root := t.TempDir()
	dir := writeStack(t, root, "grp/app", "compose.yaml", "name: custom\n")
	path := filepath.Join(dir, "compose.yaml")
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	cm := &ComposeManager{stacksDir: root, composeBin: "docker", isV2: true}
	if _, err := cm.stackDeclaresName("grp/app"); err == nil || !strings.Contains(err.Error(), "reading compose.yaml") {
		t.Fatalf("err = %v, want a reading compose.yaml error", err)
	}
}

func TestNewComposeManagerWithListerWiresLister(t *testing.T) {
	lister := fakeLister{}
	cm := NewComposeManagerWithLister(t.TempDir(), "1.45", "/nonexistent.sock", lister)
	if cm.lister == nil {
		t.Fatal("lister was not wired by NewComposeManagerWithLister")
	}
	if _, ok := cm.lister.(fakeLister); !ok {
		t.Fatalf("lister has type %T, want fakeLister", cm.lister)
	}

	other := fakeLister{err: errors.New("other")}
	cm.SetContainerLister(other)
	got, ok := cm.lister.(fakeLister)
	if !ok || got.err == nil || got.err.Error() != "other" {
		t.Fatalf("SetContainerLister did not replace the lister: %#v", cm.lister)
	}
}

func TestStackDeclaresNameOpenRootError(t *testing.T) {
	cm := &ComposeManager{stacksDir: filepath.Join(t.TempDir(), "missing")}
	declared, err := cm.stackDeclaresName("stack")
	if err == nil {
		t.Fatal("expected error for a missing stacks directory")
	}
	if declared {
		t.Error("declared = true on error")
	}
	if !strings.Contains(err.Error(), "opening stacks directory") {
		t.Errorf("error = %v, want it to wrap \"opening stacks directory\"", err)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error = %v, want it to wrap os.ErrNotExist", err)
	}
}
