// SPDX-License-Identifier: MIT

package docker

// compose_cancel_test.go pins what a cancelled request does to the compose
// subprocess: SIGTERM first, SIGKILL only after composeStopGrace, and the
// per-stack lock held until the process has actually exited.
//
// Not parallel, for the same ETXTBSY reason as compose_failure_test.go.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setComposeStopGrace(t *testing.T, d time.Duration) {
	t.Helper()
	old := composeStopGrace
	composeStopGrace = d
	t.Cleanup(func() { composeStopGrace = old })
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

type cancelRun struct {
	resp    *ComposeResponse
	err     error
	elapsed time.Duration
}

func runCancelled(t *testing.T, cm *ComposeManager, started string, cancelHook func()) cancelRun {
	t.Helper()
	ctx, cancel := contextWithCancel(t)
	done := make(chan cancelRun, 1)
	go func() {
		begin := time.Now()
		resp, err := cm.Execute(ctx, ComposeRequest{StackName: "app", Operation: "up"})
		done <- cancelRun{resp, err, time.Since(begin)}
	}()
	waitForFile(t, started)
	cancelAt := time.Now()
	cancel()
	if cancelHook != nil {
		cancelHook()
	}
	select {
	case r := <-done:
		r.elapsed = time.Since(cancelAt)
		return r
	case <-time.After(20 * time.Second):
		t.Fatal("Execute did not return after cancel")
		return cancelRun{}
	}
}

func TestExecuteCancelSendsSIGTERMNotSIGKILL(t *testing.T) {
	setComposeStopGrace(t, 10*time.Second)
	out := t.TempDir()
	started, termed := filepath.Join(out, "started"), filepath.Join(out, "termed")
	cm := composeScriptManager(t, "trap 'echo term > "+termed+"; sleep 0.3; exit 7' TERM\n"+
		": > "+started+"\nwhile :; do sleep 0.05; done\n")

	r := runCancelled(t, cm, started, nil)
	if r.err != nil {
		t.Fatalf("Execute: %v", r.err)
	}
	if r.resp.Success || !r.resp.Cancelled {
		t.Fatalf("resp = %+v, want a failed, cancelled response", r.resp)
	}
	if _, err := os.Stat(termed); err != nil {
		t.Fatalf("subprocess never saw SIGTERM: %v", err)
	}
	if strings.Contains(r.resp.Error, "killed") {
		t.Errorf("Error = %q, subprocess was SIGKILLed instead of stopped", r.resp.Error)
	}
	if r.elapsed < 250*time.Millisecond {
		t.Errorf("Execute returned after %v, before the subprocess finished its 300ms wind-down", r.elapsed)
	}
}

func TestExecuteCancelKillsAfterGraceWhenSIGTERMIgnored(t *testing.T) {
	setComposeStopGrace(t, 400*time.Millisecond)
	started := filepath.Join(t.TempDir(), "started")
	cm := composeScriptManager(t, "trap '' TERM\n: > "+started+"\nwhile :; do sleep 0.05; done\n")

	r := runCancelled(t, cm, started, nil)
	if r.err != nil {
		t.Fatalf("Execute: %v", r.err)
	}
	if !r.resp.Cancelled || !strings.Contains(r.resp.Error, "killed") {
		t.Fatalf("resp = %+v, want a cancelled response ended by SIGKILL", r.resp)
	}
	if r.elapsed < 350*time.Millisecond {
		t.Errorf("returned after %v, want the grace period to elapse before SIGKILL", r.elapsed)
	}
}

func TestExecuteCancelHoldsStackLockUntilProcessExits(t *testing.T) {
	setComposeStopGrace(t, 10*time.Second)
	out := t.TempDir()
	started, release := filepath.Join(out, "started"), filepath.Join(out, "release")
	// On SIGTERM the script keeps running until release exists.
	cm := composeScriptManager(t, "trap 'while [ ! -e "+release+" ]; do sleep 0.02; done; exit 0' TERM\n"+
		": > "+started+"\nwhile :; do sleep 0.05; done\n")

	ctx, cancel := contextWithCancel(t)
	done := make(chan *ComposeResponse, 1)
	go func() {
		resp, _ := cm.Execute(ctx, ComposeRequest{StackName: "app", Operation: "up"})
		done <- resp
	}()
	waitForFile(t, started)
	cancel()

	key, err := cm.validateRequest(ComposeRequest{StackName: "app", Operation: "up"})
	if err != nil {
		t.Fatal(err)
	}
	// A second deploy must not get the lock while the first process lives.
	waitCtx, waitCancel := contextWithTimeout(t, 300*time.Millisecond)
	defer waitCancel()
	if unlock, err := cm.lockStack(waitCtx, key); err == nil {
		unlock()
		t.Fatal("stack lock was free while the cancelled subprocess was still running")
	}
	select {
	case <-done:
		t.Fatal("Execute returned while the subprocess was still running")
	default:
	}

	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case resp := <-done:
		if !resp.Cancelled {
			t.Errorf("resp = %+v, want Cancelled", resp)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Execute did not return after the subprocess exited")
	}
	unlock, err := cm.lockStack(t.Context(), key)
	if err != nil {
		t.Fatalf("lock not released after exit: %v", err)
	}
	unlock()
}

func TestExecuteNonCancelledFailureIsNotCancelled(t *testing.T) {
	cm := composeScriptManager(t, "exit 3\n")
	resp, err := cm.Execute(t.Context(), ComposeRequest{StackName: "app", Operation: "up"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Cancelled {
		t.Error("a plain non-zero exit was marked Cancelled")
	}
}

func contextWithCancel(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx, cancel
}

func contextWithTimeout(t *testing.T, d time.Duration) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), d)
}
