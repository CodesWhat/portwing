package docker

// compose_failure_test.go pins how Execute reports a compose subprocess that
// does not finish cleanly. Hawser issue #84 was an edge compose subprocess that
// exited while the job reported success. The caller reads ComposeResponse, so
// the exit status or kill reason must be in Error with Success false.
//
// These tests write and exec a script, so none are parallel: a concurrent fork
// inherits the script's still-open write descriptor and exec fails with
// ETXTBSY (see TestExecute_MergesStdoutAndStderr).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func composeScriptManager(t *testing.T, script string) *ComposeManager {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o750); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "compose.sh")
	if err := os.WriteFile(bin, []byte("#!/usr/bin/env sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &ComposeManager{stacksDir: dir, composeBin: bin, isV2: false}
}

func TestExecuteReportsNonZeroExitStatus(t *testing.T) {
	cm := composeScriptManager(t, "printf 'pulling' \nprintf 'boom' >&2\nexit 3\n")

	resp, err := cm.Execute(t.Context(), ComposeRequest{StackName: "app", Operation: "up"})
	if err != nil {
		t.Fatalf("Execute: unexpected error %v", err)
	}
	if resp.Success {
		t.Fatal("Success = true for a subprocess that exited 3")
	}
	if !strings.Contains(resp.Error, "exit status 3") {
		t.Errorf("Error = %q, want it to carry the exit status", resp.Error)
	}
	if resp.Output != "pulling\nboom" {
		t.Errorf("Output = %q, want stdout then stderr so the caller can see why", resp.Output)
	}
}

func TestExecuteReportsSelfKilledSubprocess(t *testing.T) {
	cm := composeScriptManager(t, "printf 'starting'\nkill -9 $$\n")

	resp, err := cm.Execute(t.Context(), ComposeRequest{StackName: "app", Operation: "up"})
	if err != nil {
		t.Fatalf("Execute: unexpected error %v", err)
	}
	if resp.Success {
		t.Fatal("Success = true for a subprocess killed by SIGKILL")
	}
	if !strings.Contains(resp.Error, "signal: killed") {
		t.Errorf("Error = %q, want it to carry the kill reason", resp.Error)
	}
	if resp.Output != "starting" {
		t.Errorf("Output = %q, want the output produced before the kill", resp.Output)
	}
}

func TestExecuteReportsSubprocessKilledByContextCancel(t *testing.T) {
	cm := composeScriptManager(t, "touch started\nexec sleep 30\n")
	startedFile := filepath.Join(cm.stacksDir, "app", "started")
	ctx, cancel, started := contextWithCancelOnFile(t, startedFile)
	defer cancel()

	resp, err := cm.Execute(ctx, ComposeRequest{StackName: "app", Operation: "up"})
	if err != nil {
		t.Fatalf("Execute: unexpected error %v", err)
	}
	if !started() {
		t.Fatal("the compose script never wrote its start marker, so the cancel did not prove an in-flight kill")
	}
	if resp.Success {
		t.Fatal("Success = true for a subprocess killed by context cancellation")
	}
	if !strings.Contains(resp.Error, "killed") && !strings.Contains(resp.Error, "canceled") {
		t.Errorf("Error = %q, want it to carry the kill reason", resp.Error)
	}
}

// TestExecuteZeroExitWithStderrIsSuccessWithOutputSurfaced documents the
// chosen contract for the exit-zero-with-stderr case. Compose writes ordinary
// progress ("Container x Started") to stderr, so stderr output alone cannot
// mean failure, and the exit status is authoritative. The stderr text is still
// returned in Output so a caller that wants to inspect it can.
func TestExecuteZeroExitWithStderrIsSuccessWithOutputSurfaced(t *testing.T) {
	cm := composeScriptManager(t, "printf 'warning: orphan containers' >&2\nexit 0\n")

	resp, err := cm.Execute(t.Context(), ComposeRequest{StackName: "app", Operation: "up"})
	if err != nil {
		t.Fatalf("Execute: unexpected error %v", err)
	}
	if !resp.Success || resp.Error != "" {
		t.Fatalf("Success = %v, Error = %q, want success: exit status is authoritative", resp.Success, resp.Error)
	}
	if resp.Output != "warning: orphan containers" {
		t.Errorf("Output = %q, want the stderr text surfaced", resp.Output)
	}
}

func TestExecuteReportsMissingComposeBinary(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o750); err != nil {
		t.Fatal(err)
	}
	cm := &ComposeManager{stacksDir: dir, composeBin: filepath.Join(dir, "does-not-exist"), isV2: false}

	resp, err := cm.Execute(t.Context(), ComposeRequest{StackName: "app", Operation: "up"})
	if err != nil {
		t.Fatalf("Execute: unexpected error %v", err)
	}
	if resp.Success || resp.Error == "" {
		t.Fatalf("Success = %v, Error = %q, want a failure that names the cause", resp.Success, resp.Error)
	}
}

// contextWithCancelOnFile returns a context that is canceled once path exists,
// so a test kills the subprocess only after it has really started. The 10s
// bound only stops the watcher if the script never starts; a healthy run
// cancels within milliseconds.
func contextWithCancelOnFile(t *testing.T, path string) (context.Context, context.CancelFunc, func() bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	var seen atomic.Bool
	go func() {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(path); err == nil {
				seen.Store(true)
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
		cancel()
	}()
	return ctx, cancel, seen.Load
}
