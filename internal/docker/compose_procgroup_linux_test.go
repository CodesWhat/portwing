// SPDX-License-Identifier: MIT

//go:build linux

package docker

// Not parallel: these swap the package-level signalGroup seam.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"
)

// killRecorder wraps signalGroup. It records every call, and flags any signal
// sent once the leader has been reaped (its /proc entry is gone) or after
// runInProcessGroup has returned.
type killRecorder struct {
	mu         sync.Mutex
	calls      []syscall.Signal
	violations []string
	returned   bool
	unsafe     bool // true: swallow SIGKILL so the group seems unkillable
}

func (r *killRecorder) kill(pid int, sig syscall.Signal) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, sig)
	if r.returned {
		r.violations = append(r.violations, "signal after return")
	}
	if _, err := os.Stat("/proc/" + strconv.Itoa(-pid) + "/stat"); err != nil {
		r.violations = append(r.violations, "signal after leader was reaped")
	}
	if r.unsafe && sig == syscall.SIGKILL {
		return nil
	}
	return syscall.Kill(pid, sig)
}

func (r *killRecorder) install(t *testing.T) {
	t.Helper()
	old := signalGroup
	signalGroup = r.kill
	t.Cleanup(func() { signalGroup = old })
}

func (r *killRecorder) markReturned() {
	r.mu.Lock()
	r.returned = true
	r.mu.Unlock()
}

func (r *killRecorder) snapshot() ([]syscall.Signal, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]syscall.Signal(nil), r.calls...), append([]string(nil), r.violations...)
}

func TestParseProcStat(t *testing.T) {
	for _, tc := range []struct {
		name  string
		in    string
		state byte
		pgrp  int
		ok    bool
	}{
		{"plain", "123 (sleep) S 1 77 77 0 -1 0", 'S', 77, true},
		{"comm with spaces and parens", "123 (a) Z 1) (b c) R 9 55 55 0", 'R', 55, true},
		{"zombie", "5 (x) Z 1 42 42 0", 'Z', 42, true},
		{"no paren", "garbage", 0, 0, false},
		{"truncated", "5 (x) S 1", 0, 0, false},
		{"bad pgrp", "5 (x) S 1 nope 3", 0, 0, false},
		{"long state", "5 (x) SS 1 2 3", 0, 0, false},
	} {
		state, pgrp, ok := parseProcStat([]byte(tc.in))
		if state != tc.state || pgrp != tc.pgrp || ok != tc.ok {
			t.Errorf("%s: got (%c, %d, %v), want (%c, %d, %v)", tc.name, state, pgrp, ok, tc.state, tc.pgrp, tc.ok)
		}
	}
}

func TestLiveGroupMembersIgnoresZombiesAndOtherGroups(t *testing.T) {
	if n := liveGroupMembers(os.Getpid() + 1<<22); n != 0 {
		t.Fatalf("members of a nonexistent group = %d", n)
	}
	cmd := exec.Command("sleep", "5")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pgid := cmd.Process.Pid
	if n := liveGroupMembers(pgid); n != 1 {
		t.Errorf("live members = %d, want 1", n)
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	waitLeaderExit(pgid) // exited but unreaped: a zombie
	if n := liveGroupMembers(pgid); n != 0 {
		t.Errorf("live members after the leader died = %d, want 0 (zombies don't count)", n)
	}
	_ = cmd.Wait()
}

// (A) Nothing signals the group after the leader has been reaped.
func TestProcGroupNoSignalAfterReap(t *testing.T) {
	rec := &killRecorder{}
	rec.install(t)
	setComposeStopGrace(t, 300*time.Millisecond)

	// Normal exit that leaves a member behind: SIGKILL goes out while the
	// leader is still reserved, and nothing after the reap.
	out := t.TempDir()
	pidFile := filepath.Join(out, "child-pid")
	cm := composeScriptManager(t,
		"sh -c 'echo $$ > "+pidFile+"; exec sleep 60' >/dev/null 2>&1 &\n"+
			"while [ ! -s "+pidFile+" ]; do sleep 0.02; done\nexit 0\n")
	resp, err := cm.Execute(t.Context(), ComposeRequest{StackName: "app", Operation: "up"})
	rec.markReturned()
	if err != nil || !resp.Success {
		t.Fatalf("Execute = %+v, %v", resp, err)
	}
	if !processGone(readPID(t, pidFile)) {
		t.Fatal("member outlived Execute")
	}
	time.Sleep(100 * time.Millisecond)
	calls, violations := rec.snapshot()
	if len(violations) != 0 {
		t.Fatalf("violations: %v", violations)
	}
	if len(calls) != 1 || calls[0] != syscall.SIGKILL {
		t.Fatalf("calls = %v, want exactly one SIGKILL", calls)
	}
}

// (C) The leader exits on SIGTERM at once; a child that traps TERM needs 300 ms
// to wind down. It must get them, not an immediate SIGKILL.
func TestProcGroupGraceSurvivesEarlyLeaderExit(t *testing.T) {
	rec := &killRecorder{}
	rec.install(t)
	const grace = 2 * time.Second
	setComposeStopGrace(t, grace)
	out := t.TempDir()
	started, childUp, childDone := filepath.Join(out, "started"), filepath.Join(out, "child-up"), filepath.Join(out, "child-done")
	cm := composeScriptManager(t,
		"sh -c 'trap \"sleep 0.3; : > "+childDone+"; exit 0\" TERM; : > "+childUp+
			"; while :; do sleep 0.05; done' >/dev/null 2>&1 &\n"+
			"trap 'exit 0' TERM\n: > "+started+"\nwhile :; do sleep 0.05; done\n")
	ctx, cancel := contextWithCancel(t)
	type result struct{ at time.Time }
	done := make(chan result, 1)
	go func() {
		_, _ = cm.Execute(ctx, ComposeRequest{StackName: "app", Operation: "up"})
		rec.markReturned()
		done <- result{time.Now()}
	}()
	waitForFile(t, started)
	waitForFile(t, childUp)
	cancelAt := time.Now()
	cancel()
	r := <-done
	elapsed := r.at.Sub(cancelAt)
	if _, err := os.Stat(childDone); err != nil {
		t.Fatalf("child was killed before finishing its TERM handler: %v", err)
	}
	if elapsed < 300*time.Millisecond || elapsed >= grace {
		t.Errorf("Execute returned %v after cancel, want >=300ms and <%v", elapsed, grace)
	}
	calls, violations := rec.snapshot()
	if len(violations) != 0 {
		t.Fatalf("violations: %v", violations)
	}
	for _, c := range calls {
		if c == syscall.SIGKILL {
			t.Errorf("calls = %v, want no SIGKILL", calls)
		}
	}
}

// A child that ignores TERM is SIGKILLed at the grace, measured from the
// cancellation even though the leader is already gone.
func TestProcGroupKillsTermIgnoringChildAtGrace(t *testing.T) {
	rec := &killRecorder{}
	rec.install(t)
	const grace = 500 * time.Millisecond
	setComposeStopGrace(t, grace)
	out := t.TempDir()
	started, childUp, childPID := filepath.Join(out, "started"), filepath.Join(out, "child-up"), filepath.Join(out, "child-pid")
	cm := composeScriptManager(t,
		"sh -c 'trap \"\" TERM; echo $$ > "+childPID+"; : > "+childUp+
			"; while :; do sleep 0.05; done' >/dev/null 2>&1 &\n"+
			"trap 'exit 0' TERM\n: > "+started+"\nwhile :; do sleep 0.05; done\n")
	ctx, cancel := contextWithCancel(t)
	done := make(chan time.Time, 1)
	go func() {
		_, _ = cm.Execute(ctx, ComposeRequest{StackName: "app", Operation: "up"})
		rec.markReturned()
		done <- time.Now()
	}()
	waitForFile(t, started)
	waitForFile(t, childUp)
	pid := readPID(t, childPID)
	cancelAt := time.Now()
	cancel()
	elapsed := (<-done).Sub(cancelAt)
	if elapsed < grace-50*time.Millisecond || elapsed > grace+1500*time.Millisecond {
		t.Errorf("Execute returned %v after cancel, want about %v", elapsed, grace)
	}
	if !processGone(pid) {
		t.Error("TERM-ignoring child survived")
	}
	calls, violations := rec.snapshot()
	if len(violations) != 0 {
		t.Fatalf("violations: %v", violations)
	}
	if len(calls) != 2 || calls[0] != syscall.SIGTERM || calls[1] != syscall.SIGKILL {
		t.Errorf("calls = %v, want [SIGTERM SIGKILL]", calls)
	}
}

// A leader that ignores TERM is SIGKILLed at the grace while still running.
func TestProcGroupKillsRunningLeaderAtGrace(t *testing.T) {
	rec := &killRecorder{}
	rec.install(t)
	const grace = 300 * time.Millisecond
	setComposeStopGrace(t, grace)
	out := t.TempDir()
	started := filepath.Join(out, "started")
	cm := composeScriptManager(t, "trap '' TERM\n: > "+started+"\nwhile :; do sleep 0.05; done\n")
	ctx, cancel := contextWithCancel(t)
	done := make(chan *ComposeResponse, 1)
	go func() {
		resp, _ := cm.Execute(ctx, ComposeRequest{StackName: "app", Operation: "up"})
		rec.markReturned()
		done <- resp
	}()
	waitForFile(t, started)
	cancel()
	resp := <-done
	if !resp.Cancelled {
		t.Errorf("resp = %+v, want Cancelled", resp)
	}
	calls, violations := rec.snapshot()
	if len(violations) != 0 || len(calls) != 2 || calls[1] != syscall.SIGKILL {
		t.Errorf("calls = %v violations = %v", calls, violations)
	}
}

// A cancel that arrives while the group is draining after a normal leader exit
// re-bases the deadline on the cancellation and sends SIGTERM.
func TestProcGroupCancelDuringDrain(t *testing.T) {
	rec := &killRecorder{}
	rec.install(t)
	setComposeStopGrace(t, 400*time.Millisecond)
	out := t.TempDir()
	childUp := filepath.Join(out, "child-up")
	cm := composeScriptManager(t,
		"sh -c 'trap \"\" TERM; : > "+childUp+"; while :; do sleep 0.05; done' >/dev/null 2>&1 &\n"+
			"while [ ! -e "+childUp+" ]; do sleep 0.02; done\nexit 0\n")
	ctx, cancel := contextWithCancel(t)
	done := make(chan struct{})
	go func() {
		_, _ = cm.Execute(ctx, ComposeRequest{StackName: "app", Operation: "up"})
		rec.markReturned()
		close(done)
	}()
	waitForFile(t, childUp)
	time.Sleep(150 * time.Millisecond) // leader has exited, group draining
	cancel()
	<-done
	calls, violations := rec.snapshot()
	if len(violations) != 0 || len(calls) != 2 || calls[0] != syscall.SIGTERM || calls[1] != syscall.SIGKILL {
		t.Errorf("calls = %v violations = %v", calls, violations)
	}
}

// A group that never empties (a member stuck in the kernel) can't hold
// Execute forever.
func TestProcGroupGivesUpOnUnkillableGroup(t *testing.T) {
	rec := &killRecorder{unsafe: true}
	rec.install(t)
	oldMembers, oldReap, oldProbe := groupMembers, composeGroupReapTimeout, composeGroupProbeInterval
	t.Cleanup(func() {
		groupMembers, composeGroupReapTimeout, composeGroupProbeInterval = oldMembers, oldReap, oldProbe
	})
	groupMembers = func(int) int { return 1 }
	composeGroupReapTimeout, composeGroupProbeInterval = 100*time.Millisecond, time.Millisecond
	setComposeStopGrace(t, 100*time.Millisecond)

	cmd := exec.CommandContext(t.Context(), "sleep", "0.05")
	begin := time.Now()
	if err := runInProcessGroup(cmd); err != nil {
		t.Fatalf("runInProcessGroup = %v", err)
	}
	rec.markReturned()
	if d := time.Since(begin); d < 200*time.Millisecond || d > 3*time.Second {
		t.Errorf("returned after %v, want about grace+reap timeout", d)
	}
	calls, violations := rec.snapshot()
	if len(violations) != 0 || len(calls) != 1 || calls[0] != syscall.SIGKILL {
		t.Errorf("calls = %v violations = %v, want one SIGKILL", calls, violations)
	}
}

// A cancel hook that fires after cleanup must not signal a released PGID.
func TestProcGroupLateCancelDoesNotSignal(t *testing.T) {
	var sent int
	g := newGroupGuard(func(int, syscall.Signal) error { sent++; return nil })
	g.finish()
	if err := g.cancel(1234); !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("cancel after finish = %v, want ErrProcessDone", err)
	}
	if err := g.send(1234, syscall.SIGKILL); !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("send after finish = %v, want ErrProcessDone", err)
	}
	if sent != 0 {
		t.Errorf("%d signals sent after finish", sent)
	}
}

// Cancellation racing a normal exit never produces a signal once cleanup is
// done. Run with -race -count=50.
func TestProcGroupTimerRace(t *testing.T) {
	setComposeStopGrace(t, 20*time.Millisecond)
	old := signalGroup
	t.Cleanup(func() { signalGroup = old })
	for i := range 20 {
		rec := &killRecorder{}
		signalGroup = rec.kill
		ctx, cancel := contextWithCancel(t)
		cmd := exec.CommandContext(ctx, "sleep", "0.01")
		go func() {
			time.Sleep(time.Duration(i%10) * time.Millisecond)
			cancel()
		}()
		_ = runInProcessGroup(cmd)
		rec.markReturned()
		cancel()
		time.Sleep(2 * time.Millisecond)
		if _, violations := rec.snapshot(); len(violations) != 0 {
			t.Fatalf("iteration %d violations: %v", i, violations)
		}
	}
}
