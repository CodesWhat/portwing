package server

// sockguard_presets_test.go evaluates the shipped Sockguard presets offline
// with `sockguard match`, which loads a preset, normalises the request path and
// reports the rule that decides it. It needs the sockguard binary and skips
// when none is on PATH, so CI only runs it where Sockguard is installed.
//
// Portainer 2.39.7 and 2.45.0 fixed an authorization bypass where an
// unrecognised version prefix skipped access control. The invariant here is
// that no odd prefix or path shape unlocks a path the preset denies in its
// canonical form.

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type sockguardDecision struct {
	Decision       string `json:"decision"`
	NormalizedPath string `json:"normalized_path"`
}

func sockguardMatch(t *testing.T, preset, method, path string) sockguardDecision {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sockguard", "-c", filepath.Join("..", "..", "examples", preset), "match", "-X", method, "--path", path, "-o", "json").Output()
	if err != nil {
		t.Fatalf("sockguard match %s %s (%s): %v", method, path, preset, err)
	}
	var d sockguardDecision
	if err := json.Unmarshal(out, &d); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	return d
}

func TestSockguardPresetsDenyOddPrefixesOfDeniedPaths(t *testing.T) {
	if _, err := exec.LookPath("sockguard"); err != nil {
		t.Skip("sockguard binary not on PATH; preset evaluation needs `sockguard match`")
	}
	t.Parallel()

	prefixes := []string{
		"",
		"/v1.47",
		"/v1.47.0",
		"/v01.47",
		"/V1.47",
		"/v1",
		"/v1.47/",
		"/v1.47//",
		"//",
		"/v1.47/.",
		"/v1.47/../v1.47",
		"/v1.47%2F",
	}
	type denied struct{ method, path string }
	// Paths each preset denies in canonical form. build and push carry registry
	// credentials, so a preset that let an odd prefix through would hand them
	// to the daemon unfiltered.
	common := []denied{
		{"POST", "/build"},
		{"POST", "/images/nginx/push"},
		{"GET", "/containers/abc/attach"},
		{"POST", "/containers/abc/attach"},
		{"GET", "/containers/abc/archive"},
		{"GET", "/containers/abc/export"},
		{"GET", "/secrets"},
		{"GET", "/plugins"},
		{"POST", "/commit"},
		{"POST", "/swarm/init"},
	}
	presets := map[string][]denied{
		"sockguard.yaml": append([]denied{
			{"POST", "/containers/abc/exec"},
			{"POST", "/exec/abc/start"},
			{"POST", "/exec/abc/resize"},
			{"GET", "/exec/abc/json"},
		}, common...),
		"sockguard-with-exec.yaml": common,
	}

	for preset, paths := range presets {
		for _, p := range paths {
			// The canonical form must be denied, or the odd forms prove nothing.
			if d := sockguardMatch(t, preset, p.method, p.path); d.Decision != "deny" {
				t.Fatalf("%s: canonical %s %s decision = %q, want deny", preset, p.method, p.path, d.Decision)
			}
			for _, prefix := range prefixes {
				t.Run(preset+"/"+p.method+" "+prefix+p.path, func(t *testing.T) {
					t.Parallel()
					if d := sockguardMatch(t, preset, p.method, prefix+p.path); d.Decision != "deny" {
						t.Fatalf("%s: %s %s decision = %q (normalized %q), want deny like the canonical form",
							preset, p.method, prefix+p.path, d.Decision, d.NormalizedPath)
					}
				})
			}
		}
	}
}

func TestSockguardPresetsAllowCanonicalPathsPortwingNeeds(t *testing.T) {
	if _, err := exec.LookPath("sockguard"); err != nil {
		t.Skip("sockguard binary not on PATH; preset evaluation needs `sockguard match`")
	}
	t.Parallel()

	// Guards against the deny test passing because a preset denies everything.
	for _, preset := range []string{"sockguard.yaml", "sockguard-with-exec.yaml"} {
		for _, p := range [][2]string{
			{"GET", "/containers/json"},
			{"GET", "/v1.47/containers/abc/json"},
			{"GET", "/v1.47.0/containers/abc/stats"},
			{"POST", "/images/create"},
		} {
			if d := sockguardMatch(t, preset, p[0], p[1]); d.Decision != "allow" {
				t.Errorf("%s: %s %s decision = %q, want allow", preset, p[0], p[1], d.Decision)
			}
		}
	}
	if d := sockguardMatch(t, "sockguard-with-exec.yaml", "POST", "/v1.47.0/exec/abc/start"); d.Decision != "allow" {
		t.Errorf("with-exec preset: exec start decision = %q, want allow", d.Decision)
	}
}
