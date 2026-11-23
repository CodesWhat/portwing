// SPDX-License-Identifier: MIT

package docker

// Not parallel, for the same ETXTBSY reason as compose_failure_test.go.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func readPID(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatalf("bad pid %q: %v", b, err)
	}
	return pid
}

// requireLinuxProcGroup skips tests that rely on the Linux-only guarantee that
// Execute returns with no group member left. The darwin build only signals the
// group on cancel (see compose_procgroup_other.go).
func requireLinuxProcGroup(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("whole-group reaping is Linux-only")
	}
}

// processGone reports whether pid has exited. A zombie nobody has reaped (the
// test container may have no init) counts as gone.
func processGone(pid int) bool {
	if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		return true
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	if i := strings.LastIndexByte(string(b), ')'); i >= 0 && i+2 < len(b) {
		return b[i+2] == 'Z'
	}
	return false
}

// TestExecuteCancelSignalsAndReapsWholeProcessGroup models "docker compose" v2:
// the process Portwing starts spawns the real worker, and both ignore SIGTERM.
func TestExecuteCancelSignalsAndReapsWholeProcessGroup(t *testing.T) {
	requireLinuxProcGroup(t)
	const grace = 500 * time.Millisecond
	setComposeStopGrace(t, grace)
	out := t.TempDir()
	childStarted := filepath.Join(out, "child-started")
	childPID := filepath.Join(out, "child-pid")
	wrapperTermed := filepath.Join(out, "wrapper-termed")
	childTermed := filepath.Join(out, "child-termed")
	started := filepath.Join(out, "started")
	cm := composeScriptManager(t,
		"sh -c 'trap \"echo 1 > "+childTermed+"\" TERM; echo $$ > "+childPID+
			"; : > "+childStarted+"; while :; do sleep 0.05; done' &\n"+
			"trap 'echo 1 > "+wrapperTermed+"' TERM\n"+
			": > "+started+"\nwhile :; do sleep 0.05; done\n")

	ctx, cancel := contextWithCancel(t)
	type result struct {
		resp *ComposeResponse
		at   time.Time
	}
	done := make(chan result, 1)
	go func() {
		resp, _ := cm.Execute(ctx, ComposeRequest{StackName: "app", Operation: "up"})
		done <- result{resp, time.Now()}
	}()
	waitForFile(t, started)
	waitForFile(t, childStarted)
	pid := readPID(t, childPID)
	cancelAt := time.Now()
	cancel()

	// The lock must stay held while the group is alive: a second deploy of the
	// same stack can't get it before the grace period ends.
	key, err := cm.validateRequest(ComposeRequest{StackName: "app", Operation: "up"})
	if err != nil {
		t.Fatal(err)
	}
	waitCtx, waitCancel := contextWithTimeout(t, grace/3)
	if unlock, err := cm.lockStack(waitCtx, key); err == nil {
		unlock()
		t.Fatal("stack lock was free while compose's process group was alive")
	}
	waitCancel()
	if processGone(pid) {
		t.Fatal("child died before the grace period; SIGTERM was ignored so it should still be running")
	}

	var r result
	select {
	case r = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Execute did not return after cancel")
	}
	if elapsed := r.at.Sub(cancelAt); elapsed < grace-50*time.Millisecond {
		t.Errorf("Execute returned %v after cancel, before the %v grace period", elapsed, grace)
	}
	if !r.resp.Cancelled || !strings.Contains(r.resp.Error, "killed") {
		t.Errorf("resp = %+v, want a cancelled response ended by SIGKILL", r.resp)
	}
	for name, f := range map[string]string{"wrapper": wrapperTermed, "child": childTermed} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("%s never saw SIGTERM: %v", name, err)
		}
	}
	if !processGone(pid) {
		t.Fatal("Execute returned while the compose child was still alive")
	}
	unlock, err := cm.lockStack(t.Context(), key)
	if err != nil {
		t.Fatalf("lock not released after the group was gone: %v", err)
	}
	unlock()
}

func TestExecuteReapsGroupMembersLeftAfterNormalExit(t *testing.T) {
	requireLinuxProcGroup(t)
	setComposeStopGrace(t, 300*time.Millisecond)
	out := t.TempDir()
	childPID := filepath.Join(out, "child-pid")
	cm := composeScriptManager(t,
		"sh -c 'echo $$ > "+childPID+"; exec sleep 60' >/dev/null 2>&1 &\n"+
			"while [ ! -s "+childPID+" ]; do sleep 0.02; done\nexit 0\n")
	resp, err := cm.Execute(t.Context(), ComposeRequest{StackName: "app", Operation: "up"})
	if err != nil || !resp.Success {
		t.Fatalf("Execute = %+v, %v", resp, err)
	}
	if !processGone(readPID(t, childPID)) {
		t.Fatal("a leftover group member outlived Execute")
	}
}

// A group that is already gone when the cancel signal is sent is not an error:
// the command just finishes.
func TestRunInProcessGroupCancelOnVanishedGroup(t *testing.T) {
	oldSignal := signalGroup
	t.Cleanup(func() { signalGroup = oldSignal })
	signalGroup = func(int, syscall.Signal) error { return syscall.ESRCH }

	ctx, cancel := contextWithCancel(t)
	cmd := exec.CommandContext(ctx, "sleep", "0.3")
	started := make(chan struct{})
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(started)
		cancel()
	}()
	err := runInProcessGroup(cmd)
	<-started
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("runInProcessGroup = %v", err)
	}
}
