package docker

import (
	"fmt"
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

// TestBuildCommand_Logs_DefaultTail confirms --tail is always passed for
// "logs" even when the request omits Tail, so an unset request can't dump an
// unbounded log history.
func TestBuildCommand_Logs_DefaultTail(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cm := &ComposeManager{stacksDir: dir, composeBin: "docker", isV2: true}
	if err := os.MkdirAll(filepath.Join(dir, "myapp"), 0o750); err != nil {
		t.Fatal(err)
	}

	cmd, err := cm.buildCommand(t.Context(), ComposeRequest{StackName: "myapp", Operation: "logs"})
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	if !containsAll(cmd.Args, "logs", "--tail", fmt.Sprintf("%d", defaultComposeLogsTail)) {
		t.Fatalf("buildCommand logs (no Tail): unexpected args %v", cmd.Args)
	}
}

// TestBuildCommand_Logs_TailCapped confirms a Tail above the cap is clamped
// rather than passed through verbatim.
func TestBuildCommand_Logs_TailCapped(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cm := &ComposeManager{stacksDir: dir, composeBin: "docker", isV2: true}
	if err := os.MkdirAll(filepath.Join(dir, "myapp"), 0o750); err != nil {
		t.Fatal(err)
	}

	cmd, err := cm.buildCommand(t.Context(), ComposeRequest{StackName: "myapp", Operation: "logs", Tail: 1_000_000})
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	if !containsAll(cmd.Args, "logs", "--tail", fmt.Sprintf("%d", maxComposeLogsTail)) {
		t.Fatalf("buildCommand logs (Tail over cap): unexpected args %v", cmd.Args)
	}
	if containsAll(cmd.Args, "1000000") {
		t.Fatalf("buildCommand logs: uncapped tail leaked into args %v", cmd.Args)
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

func TestBuildCommand_IgnoresEnvFileSymlinkEscapingStacksDir(t *testing.T) {
	t.Parallel()

	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.env")
	if err := os.WriteFile(secret, []byte("TOKEN=leaked\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	cm := &ComposeManager{stacksDir: dir, composeBin: "docker", isV2: true}
	stackDir := filepath.Join(dir, "myapp")
	if err := os.MkdirAll(stackDir, 0o750); err != nil {
		t.Fatal(err)
	}
	// Point .env.drydock at a file outside STACKS_DIR. os.Root must refuse to
	// traverse it, so the flag is dropped rather than leaking the outside file.
	if err := os.Symlink(secret, filepath.Join(stackDir, ".env.drydock")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	cmd, err := cm.buildCommand(t.Context(), ComposeRequest{StackName: "myapp", Operation: "up"})
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	for _, a := range cmd.Args {
		if a == "--env-file" {
			t.Fatalf("buildCommand: escaping env-file symlink was accepted, got %v", cmd.Args)
		}
		if a == secret {
			t.Fatalf("buildCommand: leaked path outside stacks dir, got %v", cmd.Args)
		}
	}
}

func TestBuildCommand_IgnoresNonRegularEnvFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cm := &ComposeManager{stacksDir: dir, composeBin: "docker", isV2: true}
	// .env.drydock as a directory is not a usable env file.
	if err := os.MkdirAll(filepath.Join(dir, "myapp", ".env.drydock"), 0o750); err != nil {
		t.Fatal(err)
	}

	cmd, err := cm.buildCommand(t.Context(), ComposeRequest{StackName: "myapp", Operation: "up"})
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	for _, a := range cmd.Args {
		if a == "--env-file" {
			t.Fatalf("buildCommand: directory accepted as env file, got %v", cmd.Args)
		}
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

func TestStatRootedEnvFile_RejectsStackDirEscapingStacksDir(t *testing.T) {
	t.Parallel()

	cm := &ComposeManager{stacksDir: t.TempDir(), composeBin: "docker", isV2: true}

	// The rooted-path step is purely lexical, so it rejects the traversal before
	// any filesystem call. buildCommand drops the flag on this error rather than
	// falling back to an unrooted stat.
	if _, err := cm.statRootedEnvFile("../escape"); err == nil {
		t.Fatal("statRootedEnvFile: expected error for stack dir escaping stacks dir, got nil")
	}
}

func TestStatRootedEnvFile_ErrorsWhenStacksDirMissing(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "no-such-stacks-dir")
	cm := &ComposeManager{stacksDir: missing, composeBin: "docker", isV2: true}

	// rootedPath still succeeds because it never touches disk; opening the root
	// is the first step that can observe the missing directory.
	envFile, err := cm.statRootedEnvFile("myapp")
	if err == nil {
		t.Fatalf("statRootedEnvFile: expected error for missing stacks dir, got %q", envFile)
	}
	if !strings.Contains(err.Error(), "opening stacks directory") {
		t.Fatalf("statRootedEnvFile: want an open-root error, got %v", err)
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
func TestWriteStackFiles_ResolveEnvPathError(t *testing.T) {
	t.Parallel()

	cm := &ComposeManager{stacksDir: t.TempDir()}

	// StackDir is absolute → resolveStackRoot returns an error →
	// resolvePath used to build the file path fails →
	// writeStackFiles returns the "resolving path" error.
	req := ComposeRequest{
		StackName: "ignored",
		StackDir:  "/absolute/path",
		Files: map[string]string{
			// At least one file so we enter the Files loop which will call
			// resolvePath for the absolute stackDir and error.
			"docker-compose.yml": "services: {}\n",
		},
	}

	if err := cm.writeStackFiles(req); err == nil {
		t.Fatal("expected error from absolute StackDir in writeStackFiles, got nil")
	}
}

// TestWriteStackFiles_ResolveEnvDrydockPathError exercises the specific branch
// where EnvVars are set but the resolvePath(".env.drydock") call fails.
// We achieve this by using an absolute StackDir so resolveStackRoot errors
// when resolvePath is called for .env.drydock.
func TestWriteStackFiles_EnvVarResolvePathError(t *testing.T) {
	t.Parallel()

	cm := &ComposeManager{stacksDir: t.TempDir()}

	// Files is nil so we skip the file-writing loop.
	// EnvVars is non-empty so we enter the .env.drydock writing branch.
	// StackDir is absolute → resolvePath(".env.drydock") → resolveStackRoot errors.
	req := ComposeRequest{
		StackName: "ignored",
		StackDir:  "/absolute/path",
		EnvVars: map[string]string{
			"MY_VAR": "value",
		},
	}

	if err := cm.writeStackFiles(req); err == nil {
		t.Fatal("expected error from absolute StackDir when writing .env.drydock, got nil")
	}
}

// ---- readEvents: ctx.Err() at top of inner decode loop ----

// TestReadEvents_CtxErrAtLoopTop hits the ctx.Err() check at the very top of
// the inner for-loop in readEvents (before each Decode call).
//
// Strategy: the server pre-writes a large batch of non-allowed events into
// the response buffer before the client begins reading.  While readEvents
// iterates with `continue` through each non-allowed event, we cancel the
// context.  Eventually the `ctx.Err() != nil` check at the top of the loop
// fires before the next Decode call.
