// SPDX-License-Identifier: MIT

package docker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestComposeRequestFilesAcceptStringOrObject(t *testing.T) {
	var req ComposeRequest
	body := `{"operation":"up","stackName":"s","files":{
		"compose.yaml":"services: {}\n",
		"run.sh":{"content":"#!/bin/sh\n","mode":"0755"},
		"app.conf":{"content":"k=v","mode":420},
		"evil.sh":{"content":"x","mode":"4777"},
		"plain.txt":{"content":"y"}}}`
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Files["compose.yaml"] != "services: {}\n" || req.Files["run.sh"] != "#!/bin/sh\n" || req.Files["plain.txt"] != "y" {
		t.Fatalf("contents wrong: %#v", req.Files)
	}
	want := map[string]os.FileMode{"run.sh": 0o755, "app.conf": 0o644, "evil.sh": 0o755}
	for name, m := range want {
		if req.FileModes[name] != m {
			t.Errorf("mode[%s] = %o, want %o", name, req.FileModes[name], m)
		}
	}
	if _, ok := req.FileModes["compose.yaml"]; ok {
		t.Error("plain string entry must carry no mode")
	}
	if _, ok := req.FileModes["plain.txt"]; ok {
		t.Error("object without mode must carry no mode")
	}
}

func TestComposeRequestFilesRejectBadEntries(t *testing.T) {
	for _, body := range []string{
		`{"files":{"a":5}}`,
		`{"files":{"a":{"content":"x","mode":"rwx"}}}`,
		`{"files":{"a":{"content":"x","mode":-1}}}`,
		`{"files":{"a":{"content":"x","mode":true}}}`,
	} {
		var req ComposeRequest
		if err := json.Unmarshal([]byte(body), &req); err == nil {
			t.Errorf("%s: want error", body)
		}
	}
}

func TestWriteStackFilesAppliesMaskedMode(t *testing.T) {
	dir := t.TempDir()
	cm := NewComposeManager(dir, "1.45", "/nonexistent.sock")
	var req ComposeRequest
	body := `{"operation":"up","stackName":"s","files":{
		"compose.yaml":"services: {}\n",
		"run.sh":{"content":"#!/bin/sh\n","mode":"0755"},
		"evil.sh":{"content":"x","mode":"6777"},
		"conf/app.conf":{"content":"k=v","mode":"0644"}},
		"envVars":{"A":"1"}}`
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	if err := cm.writeStackFiles(req); err != nil {
		t.Fatalf("writeStackFiles: %v", err)
	}
	want := map[string]os.FileMode{
		"compose.yaml": 0o600, "run.sh": 0o755, "evil.sh": 0o755,
		"conf/app.conf": 0o644, ".env.drydock": 0o600,
	}
	for name, m := range want {
		info, err := os.Stat(filepath.Join(dir, "s", name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if got := info.Mode(); got != m {
			t.Errorf("%s mode = %v (%o), want %o", name, got, got, m)
		}
	}
}
