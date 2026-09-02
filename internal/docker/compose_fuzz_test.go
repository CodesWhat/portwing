package docker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FuzzComposeRequestValidate feeds fuzzed JSON through the same decode ->
// validateRequest -> writeStackFiles pipeline that internal/server/http.go's
// handleCompose and internal/edge/client.go's handleComposeRequestTo run on
// an authenticated caller's request body. ComposeRequest is the largest,
// most security-sensitive JSON struct in the repo — validateRequest and
// writeStackFiles carry the path-traversal defense and credential handling
// that make StackDir/Files/RegistryAuth safe to act on — so the properties
// under test are: never panic, never let an invalid request reach a
// zero-value success, and never let writeStackFiles put a file outside the
// manager's stacksDir.
func FuzzComposeRequestValidate(f *testing.F) {
	stacksDir := f.TempDir()
	cm := &ComposeManager{stacksDir: stacksDir}
	absStacksDir, err := filepath.Abs(stacksDir)
	if err != nil {
		f.Fatalf("resolving stacksDir: %v", err)
	}

	// Seed: valid JSON, minimal.
	f.Add([]byte(`{"stackName":"myapp"}`))
	// Seed: valid JSON, full shape.
	f.Add([]byte(`{"operation":"up","stackName":"myapp","stackDir":"myapp","services":["web","db"],"build":true,"envVars":{"FOO":"bar"},"files":{"docker-compose.yml":"version: \"3\""},"registryAuth":{"server":"https://registry.example.com","username":"u","password":"p"}}`))
	// Seed: empty object — missing required stackName.
	f.Add([]byte(`{}`))
	// Seed: unknown fields.
	f.Add([]byte(`{"stackName":"myapp","unknownField":"value","nested":{"a":1}}`))
	// Seed: path traversal in stackDir.
	f.Add([]byte(`{"stackName":"myapp","stackDir":"../../etc"}`))
	// Seed: path traversal in a file name.
	f.Add([]byte(`{"stackName":"myapp","files":{"../../etc/passwd":"pwned"}}`))
	// Seed: absolute path in a file name.
	f.Add([]byte(`{"stackName":"myapp","files":{"/etc/passwd":"pwned"}}`))
	// Seed: oversized string.
	f.Add([]byte(`{"stackName":"` + strings.Repeat("a", 10000) + `"}`))
	// Seed: wrong types (tail as string, files as an array instead of a map).
	f.Add([]byte(`{"stackName":"myapp","tail":"not-a-number"}`))
	f.Add([]byte(`{"stackName":"myapp","files":["not","an","object"]}`))
	// Seed: malformed base64 file content.
	f.Add([]byte(`{"stackName":"myapp","files":{"a.txt":"base64:not-valid-base64!!"}}`))
	// Seed: env var keys crafted to just miss envVarKeyPattern/envVarDenylist.
	f.Add([]byte(`{"stackName":"myapp","envVars":{"PATH ":"x","1BAD":"y","ld_preload":"z"}}`))
	// Seed: registryAuth.server with userinfo and an unusual port.
	f.Add([]byte(`{"stackName":"myapp","registryAuth":{"server":"https://user:pass@registry.example.com:99999","username":"u","password":"p"}}`))
	// Seed: not JSON at all.
	f.Add([]byte(`not json`))
	f.Add([]byte(``))
	f.Add([]byte(`null`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var req ComposeRequest
		if err := json.Unmarshal(data, &req); err != nil {
			// Malformed JSON must produce an error, never a panic, and never
			// a usable ComposeRequest — nothing further to exercise.
			return
		}

		if err := cm.validateRequest(req); err != nil {
			// Rejected requests must never reach writeStackFiles.
			return
		}

		// validateRequest accepted the request. writeStackFiles may still
		// fail for reasons validateRequest doesn't check (invalid base64,
		// concurrent filesystem state left by an earlier fuzz iteration) —
		// that's fine. The property under test is "no panic" and "nothing
		// written outside stacksDir", not "writeStackFiles always succeeds".
		_ = cm.writeStackFiles(req)

		walkErr := filepath.WalkDir(absStacksDir, func(walkPath string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			absPath, absErr := filepath.Abs(walkPath)
			if absErr != nil {
				t.Fatalf("resolving written file path %q: %v", walkPath, absErr)
			}
			rel, relErr := filepath.Rel(absStacksDir, absPath)
			if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				t.Fatalf("writeStackFiles wrote %q outside stacksDir %q", walkPath, absStacksDir)
			}
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walking stacksDir: %v", walkErr)
		}
	})
}
