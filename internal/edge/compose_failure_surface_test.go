package edge

// compose_failure_surface_test.go pins what the controller receives over the
// edge socket when the compose subprocess does not finish cleanly. Hawser
// issue #84 was an edge compose subprocess that exited while the job reported
// success. The reply is a normal response frame whose body is a
// ComposeResponse, so the failure has to be in that body.
//
// Not parallel: the fake docker binary is found through PATH, and the mode is
// passed through the environment, both of which t.Setenv serialises.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codeswhat/portwing/internal/docker"
	"github.com/codeswhat/portwing/internal/protocol"
)

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

func edgeComposeResponse(t *testing.T, mode string) (protocol.ResponseMessage, docker.ComposeResponse) {
	t.Helper()
	installFakeDocker(t, mode)

	c, ctrl := newTestClient(t)
	stacks := t.TempDir()
	if err := os.MkdirAll(filepath.Join(stacks, "app"), 0o750); err != nil {
		t.Fatal(err)
	}
	c.compose = docker.NewComposeManager(stacks, "1.44", "")

	body, err := json.Marshal(docker.ComposeRequest{Operation: "up", StackName: "app"})
	if err != nil {
		t.Fatal(err)
	}
	c.handleRequest(context.Background(), protocol.RequestMessage{
		RequestID: "compose-fail",
		Method:    http.MethodPost,
		Path:      "/_portwing/compose",
		Body:      body,
	})

	var resp protocol.ResponseMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeResponse), &resp)
	var composeResp docker.ComposeResponse
	decodeData(t, resp.Body, &composeResp)
	return resp, composeResp
}

func TestEdgeComposeReportsNonZeroExit(t *testing.T) {
	resp, composeResp := edgeComposeResponse(t, "exit3")

	if resp.RequestID != "compose-fail" || resp.StatusCode != http.StatusOK {
		t.Fatalf("frame = %+v, want the request's ID and a 200 envelope", resp)
	}
	if composeResp.Success || !strings.Contains(composeResp.Error, "exit status 3") {
		t.Fatalf("Success = %v, Error = %q, want a failure carrying the exit status", composeResp.Success, composeResp.Error)
	}
	if composeResp.Output != "pulling\nboom" {
		t.Errorf("Output = %q, want stdout then stderr", composeResp.Output)
	}
}

func TestEdgeComposeReportsKilledSubprocess(t *testing.T) {
	_, composeResp := edgeComposeResponse(t, "killed")

	if composeResp.Success || !strings.Contains(composeResp.Error, "signal: killed") {
		t.Fatalf("Success = %v, Error = %q, want a failure carrying the kill reason", composeResp.Success, composeResp.Error)
	}
}

// Exit status is authoritative: compose prints progress on stderr, so stderr
// output on a zero exit is a success whose text is still returned.
func TestEdgeComposeZeroExitWithStderrIsSuccess(t *testing.T) {
	_, composeResp := edgeComposeResponse(t, "stderr-ok")

	if !composeResp.Success || composeResp.Error != "" || composeResp.Output != "orphan warning" {
		t.Fatalf("response = %+v, want success with the stderr text in Output", composeResp)
	}
}
