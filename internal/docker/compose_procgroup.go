// SPDX-License-Identifier: MIT

package docker

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// Portwing builds for linux and darwin only (see .goreleaser.yml), so process
// groups and kill(2) are available without build tags.

var (
	// composeGroupReapTimeout bounds how long Execute waits for every member of
	// the compose process group to disappear after the leader has been reaped.
	// A member that outlives it is a zombie nobody reaps (a container without
	// an init), which no amount of waiting fixes.
	composeGroupReapTimeout = 2 * time.Second
	// composeGroupProbeInterval is the gap between group-existence probes.
	composeGroupProbeInterval = 10 * time.Millisecond
	// composeWaitDelaySlack keeps os/exec's WaitDelay behind the group SIGKILL:
	// the pipe readers are only force-closed once the whole group is dead.
	composeWaitDelaySlack = 5 * time.Second

	// signalGroup is syscall.Kill, a variable so tests can simulate a group
	// that never goes away.
	signalGroup = syscall.Kill
)

// runInProcessGroup runs cmd in its own process group and returns only after
// cmd.Wait has returned and no member of the group is left. "docker compose"
// (v2) runs Compose as a child of the Docker CLI, so signalling or killing
// only the process we started would leave Compose running while the caller
// released the per-stack lock.
//
// On context cancellation the group gets SIGTERM, and SIGKILL once
// composeStopGrace has passed.
func runInProcessGroup(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var (
		mu        sync.Mutex
		finished  bool
		killTimer *time.Timer
	)
	// Captured here because the kill timer outlives this call's stack frame.
	grace, signal := composeStopGrace, signalGroup
	cmd.Cancel = func() error {
		pgid := cmd.Process.Pid
		err := signal(-pgid, syscall.SIGTERM)
		mu.Lock()
		if !finished {
			killTimer = time.AfterFunc(grace, func() { _ = signal(-pgid, syscall.SIGKILL) })
		}
		mu.Unlock()
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	// Wait blocks on the output pipes until every holder of them is gone, so a
	// surviving group member would hang it. WaitDelay is only the backstop for
	// that: it must not fire before the group SIGKILL above.
	cmd.WaitDelay = grace + composeWaitDelaySlack

	if err := cmd.Start(); err != nil {
		return err
	}
	pgid := cmd.Process.Pid
	err := cmd.Wait()

	mu.Lock()
	finished = true
	if killTimer != nil {
		killTimer.Stop()
	}
	mu.Unlock()

	reapProcessGroup(pgid)
	return err
}

// reapProcessGroup kills anything still in the group and waits, bounded, for
// kill(-pgid, 0) to report ESRCH.
func reapProcessGroup(pgid int) {
	if err := signalGroup(-pgid, syscall.SIGKILL); errors.Is(err, syscall.ESRCH) {
		return
	}
	deadline := time.Now().Add(composeGroupReapTimeout)
	for {
		if err := signalGroup(-pgid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(composeGroupProbeInterval)
	}
}
