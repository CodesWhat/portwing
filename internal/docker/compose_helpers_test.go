package docker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- buildEnv ----

func TestBuildEnv_SetsDockerAPIVersion(t *testing.T) {
	t.Parallel()

	cm := &ComposeManager{
		stacksDir:    t.TempDir(),
		apiVersion:   "v1.45",
		dockerSocket: "",
	}

	env := cm.buildEnv()

	var found bool
	for _, e := range env {
		if e == "DOCKER_API_VERSION=1.45" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("buildEnv: DOCKER_API_VERSION=1.45 not found in env %v", env)
	}
}

func TestBuildEnv_SetsDockerHostWhenSocketSet(t *testing.T) {
	t.Parallel()

	cm := &ComposeManager{
		stacksDir:    t.TempDir(),
		apiVersion:   "v1.44",
		dockerSocket: "/var/run/docker.sock",
	}

	env := cm.buildEnv()

	var found bool
	for _, e := range env {
		if e == "DOCKER_HOST=unix:///var/run/docker.sock" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("buildEnv: DOCKER_HOST not found in env %v", env)
	}
}

func TestBuildEnv_NoDockerHostWhenSocketEmpty(t *testing.T) {
	t.Parallel()

	cm := &ComposeManager{
		stacksDir:    t.TempDir(),
		apiVersion:   "v1.44",
		dockerSocket: "",
	}

	env := cm.buildEnv()

	for _, e := range env {
		if strings.HasPrefix(e, "DOCKER_HOST=") {
			t.Fatalf("buildEnv: unexpected DOCKER_HOST in env when socket empty: %q", e)
		}
	}
}

func TestBuildEnv_StripsVPrefixFromAPIVersion(t *testing.T) {
	t.Parallel()

	cm := &ComposeManager{
		stacksDir:  t.TempDir(),
		apiVersion: "v1.45",
	}

	env := cm.buildEnv()

	// Should set DOCKER_API_VERSION=1.45, not v1.45.
	for _, e := range env {
		if e == "DOCKER_API_VERSION=v1.45" {
			t.Fatal("buildEnv: DOCKER_API_VERSION should not have 'v' prefix")
		}
	}
}

// ---- buildCommand ----

func TestBuildCommand_Up(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cm := &ComposeManager{
		stacksDir:  dir,
		composeBin: "docker",
		isV2:       true,
	}

	if err := os.MkdirAll(filepath.Join(dir, "myapp"), 0o750); err != nil {
		t.Fatal(err)
	}

	req := ComposeRequest{
		StackName:     "myapp",
		Operation:     "up",
		Build:         true,
		ForceRecreate: true,
		NoDeps:        true,
		Services:      []string{"web"},
	}

	cmd, err := cm.buildCommand(t.Context(), req)
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}

	args := cmd.Args
	if !containsAll(args, "compose", "up", "-d", "--remove-orphans", "--build", "--force-recreate", "--no-deps", "web") {
		t.Fatalf("buildCommand up: unexpected args %v", args)
	}
}

func TestBuildCommand_Down(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cm := &ComposeManager{
		stacksDir:  dir,
		composeBin: "docker",
		isV2:       true,
	}

	if err := os.MkdirAll(filepath.Join(dir, "myapp"), 0o750); err != nil {
		t.Fatal(err)
	}

	req := ComposeRequest{
		StackName:     "myapp",
		Operation:     "down",
		RemoveVolumes: true,
	}

	cmd, err := cm.buildCommand(t.Context(), req)
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}

	if !containsAll(cmd.Args, "down", "--remove-orphans", "--volumes") {
		t.Fatalf("buildCommand down: unexpected args %v", cmd.Args)
	}
}

func TestBuildCommand_Pull(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cm := &ComposeManager{
		stacksDir:  dir,
		composeBin: "docker",
		isV2:       true,
	}
	if err := os.MkdirAll(filepath.Join(dir, "myapp"), 0o750); err != nil {
		t.Fatal(err)
	}

	cmd, err := cm.buildCommand(t.Context(), ComposeRequest{StackName: "myapp", Operation: "pull", Services: []string{"web", "db"}})
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	if !containsAll(cmd.Args, "pull", "web", "db") {
		t.Fatalf("buildCommand pull: unexpected args %v", cmd.Args)
	}
}

func TestBuildCommand_Ps(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cm := &ComposeManager{stacksDir: dir, composeBin: "docker", isV2: true}
	if err := os.MkdirAll(filepath.Join(dir, "myapp"), 0o750); err != nil {
		t.Fatal(err)
	}

	cmd, err := cm.buildCommand(t.Context(), ComposeRequest{StackName: "myapp", Operation: "ps"})
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	if !containsAll(cmd.Args, "ps", "--format", "json") {
		t.Fatalf("buildCommand ps: unexpected args %v", cmd.Args)
	}
}

func TestBuildCommand_Logs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cm := &ComposeManager{stacksDir: dir, composeBin: "docker", isV2: true}
	if err := os.MkdirAll(filepath.Join(dir, "myapp"), 0o750); err != nil {
		t.Fatal(err)
	}

	cmd, err := cm.buildCommand(t.Context(), ComposeRequest{StackName: "myapp", Operation: "logs", Tail: 50, Services: []string{"web"}})
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	if !containsAll(cmd.Args, "logs", "--tail", "50", "web") {
		t.Fatalf("buildCommand logs: unexpected args %v", cmd.Args)
	}
}

func TestBuildCommand_Restart(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cm := &ComposeManager{stacksDir: dir, composeBin: "docker", isV2: true}
	if err := os.MkdirAll(filepath.Join(dir, "myapp"), 0o750); err != nil {
		t.Fatal(err)
	}

	cmd, err := cm.buildCommand(t.Context(), ComposeRequest{StackName: "myapp", Operation: "restart", Services: []string{"web"}})
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	if !containsAll(cmd.Args, "restart", "web") {
		t.Fatalf("buildCommand restart: unexpected args %v", cmd.Args)
	}
}

func TestBuildCommand_Stop(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cm := &ComposeManager{stacksDir: dir, composeBin: "docker", isV2: true}
	if err := os.MkdirAll(filepath.Join(dir, "myapp"), 0o750); err != nil {
		t.Fatal(err)
	}

	cmd, err := cm.buildCommand(t.Context(), ComposeRequest{StackName: "myapp", Operation: "stop", Services: []string{"web"}})
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	if !containsAll(cmd.Args, "stop", "web") {
		t.Fatalf("buildCommand stop: unexpected args %v", cmd.Args)
	}
}

func TestBuildCommand_Start(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cm := &ComposeManager{stacksDir: dir, composeBin: "docker", isV2: true}
	if err := os.MkdirAll(filepath.Join(dir, "myapp"), 0o750); err != nil {
		t.Fatal(err)
	}

	cmd, err := cm.buildCommand(t.Context(), ComposeRequest{StackName: "myapp", Operation: "start", Services: []string{"web"}})
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	if !containsAll(cmd.Args, "start", "web") {
		t.Fatalf("buildCommand start: unexpected args %v", cmd.Args)
	}
}

func TestBuildCommand_UnsupportedOperation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cm := &ComposeManager{stacksDir: dir, composeBin: "docker", isV2: true}
	if err := os.MkdirAll(filepath.Join(dir, "myapp"), 0o750); err != nil {
		t.Fatal(err)
	}

	_, err := cm.buildCommand(t.Context(), ComposeRequest{StackName: "myapp", Operation: "nuke"})
	if err == nil {
		t.Fatal("expected error for unsupported operation, got nil")
	}
}

func TestBuildCommand_V1ComposeBin(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// isV2=false means binary is docker-compose, no "compose" sub-command prefix.
	cm := &ComposeManager{stacksDir: dir, composeBin: "docker-compose", isV2: false}
	if err := os.MkdirAll(filepath.Join(dir, "myapp"), 0o750); err != nil {
		t.Fatal(err)
	}

	cmd, err := cm.buildCommand(t.Context(), ComposeRequest{StackName: "myapp", Operation: "up"})
	if err != nil {
		t.Fatalf("buildCommand (v1): %v", err)
	}
	// First arg should be the binary, not "compose".
	if cmd.Args[0] != "docker-compose" {
		t.Fatalf("cmd.Args[0] = %q, want %q", cmd.Args[0], "docker-compose")
	}
	for _, a := range cmd.Args[1:] {
		if a == "compose" {
			t.Fatal("buildCommand v1: unexpected 'compose' sub-command in args")
		}
	}
}

func TestBuildCommand_UsesEnvFileWhenPresent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cm := &ComposeManager{stacksDir: dir, composeBin: "docker", isV2: true}
	stackDir := filepath.Join(dir, "myapp")
	if err := os.MkdirAll(stackDir, 0o750); err != nil {
		t.Fatal(err)
	}
	// Create .env.drydock so buildCommand picks it up.
	envFile := filepath.Join(stackDir, ".env.drydock")
	if err := os.WriteFile(envFile, []byte("FOO=bar\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd, err := cm.buildCommand(t.Context(), ComposeRequest{StackName: "myapp", Operation: "up"})
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	if !containsAll(cmd.Args, "--env-file", envFile) {
		t.Fatalf("buildCommand: expected --env-file in args, got %v", cmd.Args)
	}
}

func TestBuildCommand_StackDirOverride(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cm := &ComposeManager{stacksDir: dir, composeBin: "docker", isV2: true}
	// Create a subdirectory with a custom name.
	if err := os.MkdirAll(filepath.Join(dir, "custom-dir"), 0o750); err != nil {
		t.Fatal(err)
	}

	cmd, err := cm.buildCommand(t.Context(), ComposeRequest{StackName: "ignored", StackDir: "custom-dir", Operation: "ps"})
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	if !strings.Contains(cmd.Dir, "custom-dir") {
		t.Fatalf("cmd.Dir = %q, expected to contain 'custom-dir'", cmd.Dir)
	}
}

// ---- pathWithin ----

func TestPathWithin(t *testing.T) {
	t.Parallel()

	cases := []struct {
		base, target string
		want         bool
	}{
		{"/stacks", "/stacks", true},           // base == target (rel == ".")
		{"/stacks", "/stacks/app", true},       // child
		{"/stacks", "/stacks/app/sub", true},   // deep child
		{"/stacks", "/other", false},           // outside
		{"/stacks", "/stacks/../other", false}, // traversal
		{"/stacks/app", "/stacks", false},      // parent — target is outside base
	}

	for _, c := range cases {
		got := pathWithin(c.base, c.target)
		if got != c.want {
			t.Errorf("pathWithin(%q, %q) = %v, want %v", c.base, c.target, got, c.want)
		}
	}
}

// ---- writeStackFiles: base64 decoding ----

func TestWriteStackFiles_Base64Content(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cm := &ComposeManager{stacksDir: dir}

	// "hello world" base64-encoded.
	req := ComposeRequest{
		StackName: "app",
		Files: map[string]string{
			"data.txt": "base64:aGVsbG8gd29ybGQ=",
		},
	}

	if err := cm.writeStackFiles(req); err != nil {
		t.Fatalf("writeStackFiles: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "app", "data.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello world" {
		t.Fatalf("decoded content = %q, want %q", string(data), "hello world")
	}
}

func TestWriteStackFiles_InvalidBase64(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cm := &ComposeManager{stacksDir: dir}

	req := ComposeRequest{
		StackName: "app",
		Files: map[string]string{
			"data.txt": "base64:!!!not-valid-base64!!!",
		},
	}

	if err := cm.writeStackFiles(req); err == nil {
		t.Fatal("expected error for invalid base64, got nil")
	}
}

func TestWriteStackFiles_PlainContent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cm := &ComposeManager{stacksDir: dir}

	req := ComposeRequest{
		StackName: "app",
		Files: map[string]string{
			"docker-compose.yml": "services:\n  web:\n    image: nginx\n",
		},
	}

	if err := cm.writeStackFiles(req); err != nil {
		t.Fatalf("writeStackFiles: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "app", "docker-compose.yml"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "nginx") {
		t.Fatalf("file content missing 'nginx': %q", string(data))
	}
}

// ---- resolveStackRoot ----

func TestResolveStackRoot_AbsolutePathRejected(t *testing.T) {
	t.Parallel()

	cm := &ComposeManager{stacksDir: t.TempDir()}
	_, err := cm.resolveStackRoot("/absolute/path")
	if err == nil {
		t.Fatal("expected error for absolute stack path, got nil")
	}
}

// containsAll returns true if slice contains all the given strings.
func containsAll(slice []string, targets ...string) bool {
	for _, target := range targets {
		found := false
		for _, s := range slice {
			if s == target {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
