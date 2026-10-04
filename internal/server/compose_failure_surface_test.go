package server

// compose_failure_surface_test.go pins what the HTTP compose endpoint tells
// its caller when the compose subprocess does not finish cleanly. Hawser
// issue #84 was an edge compose subprocess that exited while the job reported
// success. The endpoint answers 200 with a JSON ComposeResponse, so the
// failure has to be in that body and in the audit outcome.
//
// Not parallel: the fake docker binary is found through PATH, and the mode is
// passed through the environment, both of which t.Setenv serialises.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/docker"
)

// installFakeDocker puts a "docker" script first on PATH. It answers the
// "compose version" probe so ComposeManager picks the v2 form, and otherwise
// behaves as FAKE_COMPOSE_MODE says.
func installFakeDocker(t *testing.T, mode string) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/usr/bin/env sh
if [ "$2" = "version" ]; then echo "Docker Compose version v2.30.0"; exit 0; fi
case "$FAKE_COMPOSE_MODE" in
exit3) printf 'pulling'; printf 'boom' >&2; exit 3 ;;
killed) printf 'starting'; kill -9 $$ ;;
stderr-ok) printf 'orphan warning' >&2; exit 0 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_COMPOSE_MODE", mode)
}

func postCompose(t *testing.T, mode string) (docker.ComposeResponse, int, []audit.Record) {
	t.Helper()
	installFakeDocker(t, mode)

	auditor, closeAudit, err := audit.New("", 16)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeAudit)
	rl := NewRateLimiter()
	t.Cleanup(rl.Stop)
	stacks := t.TempDir()
	if err := os.MkdirAll(filepath.Join(stacks, "app"), 0o750); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		compose:     docker.NewComposeManager(stacks, "1.44", ""),
		rateLimiter: rl,
		auditor:     auditor,
		cfg:         minimalConfig(),
	}

	req := httptest.NewRequest(http.MethodPost, "/_portwing/compose", strings.NewReader(`{"operation":"up","stackName":"app"}`))
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	s.handleCompose(rec, req)

	var resp docker.ComposeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return resp, rec.Code, auditor.Records(10)
}

func TestHandleComposeReportsNonZeroExit(t *testing.T) {
	resp, status, records := postCompose(t, "exit3")

	if status != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200 with the failure in the body", status)
	}
	if resp.Success || !strings.Contains(resp.Error, "exit status 3") {
		t.Fatalf("Success = %v, Error = %q, want a failure carrying the exit status", resp.Success, resp.Error)
	}
	if resp.Output != "pulling\nboom" {
		t.Errorf("Output = %q, want stdout then stderr", resp.Output)
	}
	assertComposeAuditOutcome(t, records, audit.OutcomeError)
}

func TestHandleComposeReportsKilledSubprocess(t *testing.T) {
	resp, _, records := postCompose(t, "killed")

	if resp.Success || !strings.Contains(resp.Error, "signal: killed") {
		t.Fatalf("Success = %v, Error = %q, want a failure carrying the kill reason", resp.Success, resp.Error)
	}
	assertComposeAuditOutcome(t, records, audit.OutcomeError)
}

// Exit status is authoritative: compose prints progress on stderr, so stderr
// output on a zero exit is a success whose text is still returned.
func TestHandleComposeZeroExitWithStderrIsSuccess(t *testing.T) {
	resp, _, records := postCompose(t, "stderr-ok")

	if !resp.Success || resp.Error != "" || resp.Output != "orphan warning" {
		t.Fatalf("response = %+v, want success with the stderr text in Output", resp)
	}
	assertComposeAuditOutcome(t, records, audit.OutcomeAllowed)
}

func assertComposeAuditOutcome(t *testing.T, records []audit.Record, want string) {
	t.Helper()
	for _, record := range records {
		if record.Event == audit.EventComposeOp {
			if record.Outcome != want {
				t.Fatalf("compose audit outcome = %q, want %q", record.Outcome, want)
			}
			return
		}
	}
	t.Fatalf("no compose_op audit record in %+v", records)
}
