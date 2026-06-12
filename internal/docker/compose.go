package docker

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	envVarKeyPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

	envVarDenylist = map[string]bool{
		"LD_PRELOAD":        true,
		"LD_LIBRARY_PATH":   true,
		"PATH":              true,
		"DOCKER_HOST":       true,
		"DOCKER_CONFIG":     true,
		"DOCKER_CERT_PATH":  true,
		"DOCKER_TLS_VERIFY": true,
		"DOCKER_CONTEXT":    true,
		"HOME":              true,
		"SHELL":             true,
		"BASH_ENV":          true,
		"ENV":               true,
		"CDPATH":            true,
		"IFS":               true,
	}
)

type ComposeRequest struct {
	Operation     string            `json:"operation"`
	StackName     string            `json:"stackName"`
	StackDir      string            `json:"stackDir,omitempty"`
	Services      []string          `json:"services,omitempty"`
	Build         bool              `json:"build,omitempty"`
	ForceRecreate bool              `json:"forceRecreate,omitempty"`
	RemoveVolumes bool              `json:"removeVolumes,omitempty"`
	NoDeps        bool              `json:"noDeps,omitempty"`
	Tail          int               `json:"tail,omitempty"`
	EnvVars       map[string]string `json:"envVars,omitempty"`
	Files         map[string]string `json:"files,omitempty"`
	RegistryAuth  *RegistryAuth     `json:"registryAuth,omitempty"`
}

type RegistryAuth struct {
	Server   string `json:"server"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type ComposeResponse struct {
	Success bool   `json:"success"`
	Output  string `json:"output"`
	Error   string `json:"error,omitempty"`
}

// ComposeManager executes Docker Compose operations in a managed stacks
// directory.
type ComposeManager struct {
	stacksDir    string
	composeBin   string
	isV2         bool
	apiVersion   string
	dockerSocket string
}

// NewComposeManager creates a ComposeManager. It auto-detects whether the
// system has Compose v2 (docker compose) or v1 (docker-compose).
func NewComposeManager(stacksDir, apiVersion, dockerSocket string) *ComposeManager {
	cm := &ComposeManager{
		stacksDir:    stacksDir,
		apiVersion:   apiVersion,
		dockerSocket: dockerSocket,
	}
	cm.detectCompose()
	return cm
}

// detectCompose checks for Compose v2 first, then falls back to v1.
func (cm *ComposeManager) detectCompose() {
	if out, err := exec.Command("docker", "compose", "version").CombinedOutput(); err == nil {
		if strings.Contains(string(out), "v2") || strings.Contains(string(out), "V2") {
			cm.composeBin = "docker"
			cm.isV2 = true
			return
		}
	}

	if _, err := exec.LookPath("docker-compose"); err == nil {
		cm.composeBin = "docker-compose"
		cm.isV2 = false
		return
	}

	// Default to v2.
	cm.composeBin = "docker"
	cm.isV2 = true
}

// Execute dispatches a compose operation and returns the result.
func (cm *ComposeManager) Execute(ctx context.Context, req ComposeRequest) (*ComposeResponse, error) {
	if err := cm.validateRequest(req); err != nil {
		return &ComposeResponse{Success: false, Error: err.Error()}, nil
	}

	if req.Files != nil {
		if err := cm.writeStackFiles(req); err != nil {
			return &ComposeResponse{Success: false, Error: fmt.Sprintf("writing stack files: %v", err)}, nil
		}
	}

	if req.RegistryAuth != nil {
		if err := cm.registryLogin(ctx, req.RegistryAuth); err != nil {
			return &ComposeResponse{Success: false, Error: fmt.Sprintf("registry login: %v", err)}, nil
		}
	}

	cmd, err := cm.buildCommand(ctx, req)
	if err != nil {
		return &ComposeResponse{Success: false, Error: err.Error()}, nil
	}

	cmd.Env = cm.buildEnv()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()

	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += stderr.String()
	}

	if err != nil {
		return &ComposeResponse{
			Success: false,
			Output:  output,
			Error:   err.Error(),
		}, nil
	}

	return &ComposeResponse{
		Success: true,
		Output:  output,
	}, nil
}

// validateRequest checks the request for invalid or dangerous inputs.
func (cm *ComposeManager) validateRequest(req ComposeRequest) error {
	if req.StackName == "" {
		return fmt.Errorf("stack name is required")
	}

	// Validate env var keys and values.
	for key, val := range req.EnvVars {
		if !envVarKeyPattern.MatchString(key) {
			return fmt.Errorf("invalid env var key: %q", key)
		}
		if envVarDenylist[key] {
			return fmt.Errorf("env var %q is not allowed", key)
		}
		if strings.ContainsAny(val, "\n\r\x00") {
			return fmt.Errorf("env var %q value contains invalid characters (newline, carriage return, or null)", key)
		}
	}

	// Validate service names (reject names starting with "-").
	for _, svc := range req.Services {
		if strings.HasPrefix(svc, "-") {
			return fmt.Errorf("invalid service name: %q", svc)
		}
	}

	// Validate stack path is within stacksDir.
	stackDir := req.StackDir
	if stackDir == "" {
		stackDir = req.StackName
	}
	if _, err := cm.resolvePath(stackDir, "."); err != nil {
		return fmt.Errorf("invalid stack path: %w", err)
	}

	return nil
}

// writeStackFiles writes the files from the request into the stack directory.
// File contents prefixed with "base64:" are decoded before writing.
func (cm *ComposeManager) writeStackFiles(req ComposeRequest) error {
	stackDir := req.StackDir
	if stackDir == "" {
		stackDir = req.StackName
	}

	for relPath, content := range req.Files {
		absPath, err := cm.resolvePath(stackDir, relPath)
		if err != nil {
			return fmt.Errorf("resolving path %q: %w", relPath, err)
		}

		dir := filepath.Dir(absPath)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating directory %q: %w", dir, err)
		}

		var data []byte
		if strings.HasPrefix(content, "base64:") {
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(content, "base64:"))
			if err != nil {
				return fmt.Errorf("decoding base64 for %q: %w", relPath, err)
			}
			data = decoded
		} else {
			data = []byte(content)
		}

		if err := os.WriteFile(absPath, data, 0o644); err != nil {
			return fmt.Errorf("writing %q: %w", absPath, err)
		}
	}

	// Write .env.drydock if env vars are provided.
	if len(req.EnvVars) > 0 {
		envPath, err := cm.resolvePath(stackDir, ".env.drydock")
		if err != nil {
			return fmt.Errorf("resolving .env.drydock path: %w", err)
		}

		var buf bytes.Buffer
		for key, val := range req.EnvVars {
			fmt.Fprintf(&buf, "%s=%s\n", key, val)
		}

		if err := os.WriteFile(envPath, buf.Bytes(), 0o644); err != nil {
			return fmt.Errorf("writing .env.drydock: %w", err)
		}
	}

	return nil
}

// buildCommand constructs the exec.Cmd for the requested compose operation.
func (cm *ComposeManager) buildCommand(ctx context.Context, req ComposeRequest) (*exec.Cmd, error) {
	stackDir := req.StackDir
	if stackDir == "" {
		stackDir = req.StackName
	}

	projectDir, err := cm.resolvePath(stackDir, ".")
	if err != nil {
		return nil, fmt.Errorf("resolving project dir: %w", err)
	}

	var args []string

	if cm.isV2 {
		args = append(args, "compose")
	}

	// Project directory.
	args = append(args, "--project-directory", projectDir)

	// Env file if it exists.
	envFile := filepath.Join(projectDir, ".env.drydock")
	if _, err := os.Stat(envFile); err == nil {
		args = append(args, "--env-file", envFile)
	}

	switch req.Operation {
	case "up":
		args = append(args, "up", "-d", "--remove-orphans")
		if req.Build {
			args = append(args, "--build")
		}
		if req.ForceRecreate {
			args = append(args, "--force-recreate")
		}
		if req.NoDeps {
			args = append(args, "--no-deps")
		}
		args = append(args, req.Services...)

	case "down":
		args = append(args, "down", "--remove-orphans")
		if req.RemoveVolumes {
			args = append(args, "--volumes")
		}

	case "pull":
		args = append(args, "pull")
		args = append(args, req.Services...)

	case "ps":
		args = append(args, "ps", "--format", "json")

	case "logs":
		args = append(args, "logs")
		if req.Tail > 0 {
			args = append(args, "--tail", fmt.Sprintf("%d", req.Tail))
		}
		args = append(args, req.Services...)

	case "restart":
		args = append(args, "restart")
		args = append(args, req.Services...)

	case "stop":
		args = append(args, "stop")
		args = append(args, req.Services...)

	case "start":
		args = append(args, "start")
		args = append(args, req.Services...)

	default:
		return nil, fmt.Errorf("unsupported compose operation: %q", req.Operation)
	}

	cmd := exec.CommandContext(ctx, cm.composeBin, args...)
	cmd.Dir = projectDir
	return cmd, nil
}

// registryLogin performs a docker login using --password-stdin.
func (cm *ComposeManager) registryLogin(ctx context.Context, auth *RegistryAuth) error {
	cmd := exec.CommandContext(ctx, "docker", "login", "--username", auth.Username, "--password-stdin", auth.Server)
	cmd.Stdin = strings.NewReader(auth.Password)
	cmd.Env = cm.buildEnv()

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker login: %s: %w", stderr.String(), err)
	}
	return nil
}

// resolvePath resolves a relative path within the stack directory and
// verifies it does not escape the stacks directory (path traversal protection).
func (cm *ComposeManager) resolvePath(stackDir, path string) (string, error) {
	base := filepath.Join(cm.stacksDir, stackDir)
	full := filepath.Join(base, path)
	resolved, err := filepath.Abs(full)
	if err != nil {
		return "", fmt.Errorf("resolving absolute path: %w", err)
	}

	absBase, err := filepath.Abs(cm.stacksDir)
	if err != nil {
		return "", fmt.Errorf("resolving stacks dir: %w", err)
	}

	if !strings.HasPrefix(resolved, absBase+string(filepath.Separator)) && resolved != absBase {
		return "", fmt.Errorf("path %q escapes stacks directory", path)
	}

	return resolved, nil
}

// buildEnv constructs the subprocess environment, setting DOCKER_API_VERSION
// and DOCKER_HOST so compose commands target the correct daemon.
func (cm *ComposeManager) buildEnv() []string {
	env := os.Environ()

	env = append(env, "DOCKER_API_VERSION="+strings.TrimPrefix(cm.apiVersion, "v"))

	if cm.dockerSocket != "" {
		env = append(env, "DOCKER_HOST=unix://"+cm.dockerSocket)
	}

	return env
}
