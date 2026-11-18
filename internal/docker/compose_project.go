// SPDX-License-Identifier: MIT

package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	applog "github.com/codeswhat/portwing/internal/log"
)

const (
	composeProjectLabel    = "com.docker.compose.project"
	composeWorkingDirLabel = "com.docker.compose.project.working_dir"

	// projectNameHashLen is the number of hex characters of the path digest
	// appended to every derived project name (40 bits).
	projectNameHashLen = 10
	// projectNamePrefixMax bounds the readable part of a derived name.
	projectNamePrefixMax = 40
	// maxComposeFileBytes bounds how much of one compose file is scanned for a
	// top-level name key.
	maxComposeFileBytes = 4 * 1024 * 1024
)

// ContainerLister is the slice of the Docker client the compose manager needs
// to find containers deployed by earlier Portwing versions. *Client satisfies
// it.
type ContainerLister interface {
	ListContainers(ctx context.Context, all bool) ([]ContainerJSON, error)
}

var (
	invalidProjectChars = regexp.MustCompile(`[^a-z0-9]+`)
	// composeNameInvalid mirrors how Compose derives a default project name
	// from a directory basename: lowercase, then drop everything outside
	// [a-z0-9_-].
	composeNameInvalid = regexp.MustCompile(`[^-_a-z0-9]+`)
	// topLevelNameKey matches a top-level (column 0) "name:" key, optionally
	// quoted.
	topLevelNameKey = regexp.MustCompile(`^(?:name|"name"|'name')[ \t]*:(?:[ \t]|$)`)

	// Files Compose loads by default from a project directory. Every one is
	// checked, so a name set in any base or override file counts.
	composeBaseFiles     = []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"}
	composeOverrideFiles = []string{"compose.override.yaml", "compose.override.yml", "docker-compose.override.yaml", "docker-compose.override.yml"}
)

// deriveProjectName returns a unique, deterministic Compose project name for a
// stack directory, given its path relative to STACKS_DIR.
//
// The name is "<readable-prefix>-<hash>". The prefix is the lowercased path
// with every run of characters outside [a-z0-9] collapsed to a single "-"
// (trimmed, capped at 40 chars, "stack" when nothing is left). That step is
// lossy: "a/b-c" and "a-b/c" both normalize to "a-b-c". The suffix is the
// first 10 hex chars of the SHA-256 of the exact cleaned, slash-separated
// relative path, so directories that differ only in how they normalize still
// get different names. Distinct paths can only collide if their digests share
// a 40-bit prefix, which is negligible for the handful of stacks one agent
// manages. The result always matches ^[a-z0-9][a-z0-9_-]*$.
func deriveProjectName(relStackDir string) string {
	rel := filepath.ToSlash(filepath.Clean(relStackDir))
	sum := sha256.Sum256([]byte(rel))
	hash := hex.EncodeToString(sum[:])[:projectNameHashLen]

	prefix := invalidProjectChars.ReplaceAllString(strings.ToLower(rel), "-")
	prefix = strings.Trim(prefix, "-")
	if len(prefix) > projectNamePrefixMax {
		prefix = strings.Trim(prefix[:projectNamePrefixMax], "-")
	}
	if prefix == "" {
		prefix = "stack"
	}
	return prefix + "-" + hash
}

// legacyProjectName returns the name Compose derives on its own from the stack
// directory's basename, which is what Portwing <= 1.3.0 ran every stack under
// because it passed --project-directory and never -p.
func legacyProjectName(projectDir string) string {
	name := composeNameInvalid.ReplaceAllString(strings.ToLower(filepath.Base(projectDir)), "")
	return strings.TrimLeft(name, "-_")
}

// stackDeclaresName reports whether any compose file Compose would load by
// default from the project directory sets a top-level "name:". When it does,
// Portwing must not pass -p, because -p overrides the user's explicit name.
//
// Compose itself can't answer this: "docker compose config" always reports a
// name, defaulting to the directory basename, so an explicit "name: v2" is
// indistinguishable from the default in a directory called v2. Scanning the
// files for a column-0 "name:" key needs no subprocess or daemon, and a
// top-level key can't be confused with nested keys because those are
// indented. Reads go through os.Root so they stay beneath STACKS_DIR.
func (cm *ComposeManager) stackDeclaresName(stackDir string) (bool, error) {
	absDir, err := cm.resolveStackRoot(stackDir)
	if err != nil {
		return false, err
	}
	absRoot, err := filepath.Abs(cm.stacksDir)
	if err != nil {
		return false, fmt.Errorf("resolving stacks directory: %w", err)
	}
	relDir, err := filepath.Rel(absRoot, absDir)
	if err != nil {
		return false, fmt.Errorf("resolving stack directory: %w", err)
	}

	root, err := os.OpenRoot(cm.stacksDir)
	if err != nil {
		return false, fmt.Errorf("opening stacks directory: %w", err)
	}
	defer func() { _ = root.Close() }()

	candidates := append(append([]string{}, composeBaseFiles...), composeOverrideFiles...)

	for _, file := range candidates {
		data, err := readRootedFile(root, filepath.Join(relDir, file))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return false, fmt.Errorf("reading %s: %w", file, err)
		}
		if composeFileDeclaresName(data) {
			return true, nil
		}
	}
	return false, nil
}

func readRootedFile(root *os.Root, name string) ([]byte, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, os.ErrNotExist
	}
	return io.ReadAll(io.LimitReader(f, maxComposeFileBytes))
}

// composeFileDeclaresName scans YAML text for a top-level name key with a
// value. A bare "name:" with nothing after it (null) doesn't count.
func composeFileDeclaresName(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		loc := topLevelNameKey.FindStringIndex(line)
		if loc == nil {
			continue
		}
		value := strings.TrimSpace(line[loc[1]:])
		if value == "" || strings.HasPrefix(value, "#") {
			continue
		}
		return true
	}
	return false
}

// hasLegacyDeployment reports whether containers already exist from a stack
// that Portwing <= 1.3.0 deployed from this exact directory. Those carry
// com.docker.compose.project=<basename> AND
// com.docker.compose.project.working_dir=<projectDir>. The working_dir label is
// what keeps this from adopting another directory's project: customer-a/v2 and
// customer-b/v2 share the project label "v2" but not the working directory.
func (cm *ComposeManager) hasLegacyDeployment(ctx context.Context, projectDir, legacyName string) (bool, error) {
	if cm.lister == nil || legacyName == "" {
		return false, nil
	}
	containers, err := cm.lister.ListContainers(ctx, true)
	if err != nil {
		return false, err
	}
	want := filepath.Clean(projectDir)
	wantResolved := evalSymlinksOrSelf(want)
	for _, c := range containers {
		if c.Labels[composeProjectLabel] != legacyName {
			continue
		}
		dir := c.Labels[composeWorkingDirLabel]
		if dir == "" {
			continue
		}
		dir = filepath.Clean(dir)
		if dir == want || evalSymlinksOrSelf(dir) == wantResolved {
			return true, nil
		}
	}
	return false, nil
}

func evalSymlinksOrSelf(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// projectNameFor decides the value for -p. An empty result means "don't pass
// -p" because the stack's compose file names its own project.
//
// Order: an explicit top-level name wins; then an already-running legacy
// deployment keeps its basename project so upgrading doesn't orphan it (the
// next "up --remove-orphans" under a new name would otherwise leave the old
// containers running next to fresh ones); otherwise the derived unique name.
func (cm *ComposeManager) projectNameFor(ctx context.Context, stackDir, projectDir string) (string, error) {
	declared, err := cm.stackDeclaresName(stackDir)
	if err != nil {
		return "", fmt.Errorf("checking compose project name: %w", err)
	}
	if declared {
		return "", nil
	}

	legacy := legacyProjectName(projectDir)
	adopt, err := cm.hasLegacyDeployment(ctx, projectDir, legacy)
	if err != nil {
		return "", fmt.Errorf("checking for existing deployment: %w", err)
	}
	if adopt {
		if _, seen := cm.adoptionLogged.LoadOrStore(projectDir, struct{}{}); !seen {
			slog.Info("adopting existing compose project for stack deployed by an earlier Portwing version",
				"stack_dir", applog.Sanitize(stackDir), "project", legacy)
		}
		return legacy, nil
	}
	return deriveProjectName(stackDir), nil
}
