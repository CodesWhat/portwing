package docker

import (
	"os"
	"path/filepath"
	"testing"
)

// ---- NewComposeManager ----

func TestNewComposeManager_ReturnsNonNil(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cm := NewComposeManager(dir, "v1.44", "/var/run/docker.sock")
	if cm == nil {
		t.Fatal("NewComposeManager returned nil")
	}
	if cm.stacksDir != dir {
		t.Fatalf("stacksDir = %q, want %q", cm.stacksDir, dir)
	}
	// detectCompose sets either "docker" or "docker-compose" — just check non-empty.
	if cm.composeBin == "" {
		t.Fatal("composeBin not set by detectCompose")
	}
}

// ---- Execute: validation failure ----

func TestExecute_ValidationFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cm := &ComposeManager{stacksDir: dir, composeBin: "docker", isV2: true}

	// Empty stack name triggers validation failure.
	resp, err := cm.Execute(t.Context(), ComposeRequest{})
	if err != nil {
		t.Fatalf("Execute: unexpected error %v", err)
	}
	if resp.Success {
		t.Fatal("Execute: expected Success=false for invalid request, got true")
	}
	if resp.Error == "" {
		t.Fatal("Execute: expected non-empty Error for invalid request")
	}
}

// ---- Execute: writeStackFiles failure ----

func TestExecute_WriteStackFilesFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cm := &ComposeManager{stacksDir: dir, composeBin: "docker", isV2: true}

	// Create the stack dir as a file (not a directory) so MkdirAll on it fails.
	stackPath := filepath.Join(dir, "app")
	if err := os.WriteFile(stackPath, []byte("not-a-dir"), 0o600); err != nil {
		t.Fatal(err)
	}

	req := ComposeRequest{
		StackName: "app",
		Operation: "up",
		Files: map[string]string{
			"nested/docker-compose.yml": "services: {}\n",
		},
	}
	resp, err := cm.Execute(t.Context(), req)
	if err != nil {
		t.Fatalf("Execute: unexpected error %v", err)
	}
	if resp.Success {
		t.Fatal("Execute: expected Success=false when writeStackFiles fails")
	}
}

// ---- Execute: buildCommand error (unsupported operation) ----

func TestExecute_BuildCommandError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o750); err != nil {
		t.Fatal(err)
	}
	cm := &ComposeManager{stacksDir: dir, composeBin: "docker", isV2: true}

	resp, err := cm.Execute(t.Context(), ComposeRequest{StackName: "app", Operation: "nuke"})
	if err != nil {
		t.Fatalf("Execute: unexpected error %v", err)
	}
	if resp.Success {
		t.Fatal("Execute: expected Success=false for unsupported operation")
	}
}

// ---- buildCommand: project dir resolve failure ----

// TestBuildCommand_ResolveProjectDirError exercises the error path in
// buildCommand when resolvePath fails for the project directory.
func TestBuildCommand_ResolveProjectDirError(t *testing.T) {
	t.Parallel()

	cm := &ComposeManager{stacksDir: t.TempDir(), composeBin: "docker", isV2: true}

	// Use a traversal stack dir so resolvePath fails.
	req := ComposeRequest{StackName: "ignored", StackDir: "../escape", Operation: "up"}
	_, err := cm.buildCommand(t.Context(), req)
	if err == nil {
		t.Fatal("expected error for escaping stack dir in buildCommand, got nil")
	}
}

// ---- Execute: command failure (binary returns non-zero) ----

func TestExecute_CommandFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o750); err != nil {
		t.Fatal(err)
	}
	// Use /usr/bin/false (always exits 1) as composeBin.
	cm := &ComposeManager{stacksDir: dir, composeBin: "/usr/bin/false", isV2: false}

	resp, err := cm.Execute(t.Context(), ComposeRequest{StackName: "app", Operation: "up"})
	if err != nil {
		t.Fatalf("Execute: unexpected error %v", err)
	}
	if resp.Success {
		t.Fatal("Execute: expected Success=false when command fails")
	}
	if resp.Error == "" {
		t.Fatal("Execute: expected non-empty Error")
	}
}

// ---- Execute: command success ----

func TestExecute_CommandSuccess(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o750); err != nil {
		t.Fatal(err)
	}
	// Use /usr/bin/true (always exits 0) as composeBin.
	cm := &ComposeManager{stacksDir: dir, composeBin: "/usr/bin/true", isV2: false}

	resp, err := cm.Execute(t.Context(), ComposeRequest{StackName: "app", Operation: "up"})
	if err != nil {
		t.Fatalf("Execute: unexpected error %v", err)
	}
	if !resp.Success {
		t.Fatalf("Execute: expected Success=true when command succeeds, got Error=%q", resp.Error)
	}
}

// ---- Execute: command produces both stdout and stderr (merge branch) ----

// TestExecute_MergesStdoutAndStderr exercises the branch where both stdout
// and stderr are non-empty (output != "" when stderr is appended).
func TestExecute_MergesStdoutAndStderr(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o750); err != nil {
		t.Fatal(err)
	}

	// Write a tiny script that produces both stdout and stderr output.
	scriptPath := filepath.Join(dir, "compose-both.sh")
	script := "#!/usr/bin/env sh\nprintf 'stdout output'\nprintf 'stderr output' >&2\nexit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cm := &ComposeManager{stacksDir: dir, composeBin: scriptPath, isV2: false}

	resp, err := cm.Execute(t.Context(), ComposeRequest{StackName: "app", Operation: "up"})
	if err != nil {
		t.Fatalf("Execute: unexpected error %v", err)
	}
	// Success should be false (exit 1), and output should contain both streams.
	if resp.Success {
		t.Fatal("Execute: expected Success=false")
	}
	if resp.Output == "" {
		t.Fatal("Execute: expected merged output, got empty")
	}
}

// ---- Execute: registryLogin failure path ----

func TestExecute_RegistryLoginFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o750); err != nil {
		t.Fatal(err)
	}
	cm := &ComposeManager{stacksDir: dir, composeBin: "docker", isV2: true}

	req := ComposeRequest{
		StackName: "app",
		Operation: "up",
		RegistryAuth: &RegistryAuth{
			Server:   "https://registry.example.com",
			Username: "user",
			Password: "wrongpassword",
		},
	}
	// docker login will fail (no such registry), so Execute should return Success=false.
	resp, err := cm.Execute(t.Context(), req)
	if err != nil {
		t.Fatalf("Execute: unexpected error %v", err)
	}
	if resp.Success {
		// If this machine actually has docker and it somehow succeeds, skip.
		t.Skip("docker login unexpectedly succeeded; skipping")
	}
	if resp.Error == "" {
		t.Fatal("Execute: expected non-empty Error when registry login fails")
	}
}

// ---- registryLogin: success path ----

// TestRegistryLogin_Success exercises the happy path of registryLogin by using
// a fake "docker" binary that always exits 0.
// Note: not parallel because it mutates os.Setenv("PATH").
func TestRegistryLogin_Success(t *testing.T) {
	// Create a fake docker binary that exits 0.
	binDir := t.TempDir()
	fakeBin := filepath.Join(binDir, "docker")
	script := "#!/usr/bin/env sh\nexit 0\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake docker: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", origPath) })                 //nolint:errcheck
	os.Setenv("PATH", binDir+string(filepath.ListSeparator)+origPath) //nolint:errcheck

	cm := &ComposeManager{
		stacksDir:  t.TempDir(),
		apiVersion: "v1.44",
	}

	auth := &RegistryAuth{
		Server:   "https://registry.example.com",
		Username: "user",
		Password: "pass",
	}

	if err := cm.registryLogin(t.Context(), auth); err != nil {
		t.Fatalf("registryLogin: unexpected error %v", err)
	}
}

// ---- validateRequest: file path traversal ----

func TestValidateRequest_FilePathTraversal(t *testing.T) {
	t.Parallel()

	cm := &ComposeManager{stacksDir: t.TempDir()}

	req := ComposeRequest{
		StackName: "app",
		Files: map[string]string{
			"../evil/compose.yml": "services: {}\n",
		},
	}
	if err := cm.validateRequest(req); err == nil {
		t.Fatal("expected error for file path traversal, got nil")
	}
}

// ---- validateRequest: stack path escapes stacks dir ----

func TestValidateRequest_StackPathTraversal(t *testing.T) {
	t.Parallel()

	cm := &ComposeManager{stacksDir: t.TempDir()}

	req := ComposeRequest{
		StackName: "../outside",
	}
	if err := cm.validateRequest(req); err == nil {
		t.Fatal("expected error for stack path traversal, got nil")
	}
}

// ---- writeStackFiles: resolve path error for a file ----

func TestWriteStackFiles_ResolvePathErrorForFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cm := &ComposeManager{stacksDir: dir}

	req := ComposeRequest{
		StackName: "app",
		Files: map[string]string{
			"../../escape.yml": "services: {}\n",
		},
	}
	if err := cm.writeStackFiles(req); err == nil {
		t.Fatal("expected error when file path escapes stack dir, got nil")
	}
}

// ---- writeStackFiles: WriteFile failure (target is a directory) ----

func TestWriteStackFiles_WriteFileFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cm := &ComposeManager{stacksDir: dir}

	// Create target as a directory so WriteFile fails.
	targetDir := filepath.Join(dir, "app", "compose.yml")
	if err := os.MkdirAll(targetDir, 0o750); err != nil {
		t.Fatal(err)
	}

	req := ComposeRequest{
		StackName: "app",
		Files: map[string]string{
			"compose.yml": "services: {}\n",
		},
	}
	if err := cm.writeStackFiles(req); err == nil {
		t.Fatal("expected error when WriteFile target is a directory, got nil")
	}
}

// ---- writeStackFiles: .env.drydock write failure ----

func TestWriteStackFiles_EnvFileDrydockWriteFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cm := &ComposeManager{stacksDir: dir}

	// Create .env.drydock as a directory so WriteFile on it fails.
	envFileAsDir := filepath.Join(dir, "app", ".env.drydock")
	if err := os.MkdirAll(envFileAsDir, 0o750); err != nil {
		t.Fatal(err)
	}

	req := ComposeRequest{
		StackName: "app",
		EnvVars: map[string]string{
			"MY_VAR": "value",
		},
		Files: map[string]string{}, // non-nil so writeStackFiles is reached via Execute's Files != nil branch
	}

	// Call writeStackFiles directly.
	if err := cm.writeStackFiles(req); err == nil {
		t.Fatal("expected error when .env.drydock target is a directory, got nil")
	}
}
