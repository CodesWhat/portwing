// SPDX-License-Identifier: MIT

//go:build linux

package docker

import (
	"bytes"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

var (
	// composeGroupReapTimeout bounds how long Execute waits for every member of
	// the compose process group to disappear after the group has been sent
	// SIGKILL. A member that outlives it is stuck in the kernel, which no
	// amount of waiting fixes.
	composeGroupReapTimeout = 2 * time.Second
	// composeGroupProbeInterval is the gap between group-membership probes.
	composeGroupProbeInterval = 10 * time.Millisecond

	// groupMembers counts the live members of a process group. A variable so
	// tests can simulate a group that never empties.
	groupMembers = liveGroupMembers
)

// cancelTime reports when the run was cancelled, if it was.
func (g *groupGuard) cancelTime() (time.Time, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.cancelledAt, g.cancelled
}

// runInProcessGroup runs cmd in its own process group and returns only after
// the leader has exited and no live member of the group is left. "docker
// compose" (v2) runs Compose as a child of the Docker CLI, so signalling or
// killing only the process we started would leave Compose running while the
// caller released the per-stack lock.
//
// The leader is deliberately left unreaped, as a zombie, until the whole group
// is finished. An unreaped leader keeps its PGID reserved, so a signal to
// -pgid can only reach our group; once it is reaped the ID may be reused by
// an unrelated group. Leader exit is detected with waitid(WNOWAIT), members
// are found by scanning /proc, and cmd.Wait (the reap) happens last.
//
// On context cancellation the group gets SIGTERM once. composeStopGrace is
// measured from the cancellation, whether or not the leader has exited by
// then, and the group gets SIGKILL when it has passed. With no cancellation
// the same bound applies after the leader exits.
func runInProcessGroup(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	grace := composeStopGrace
	g := newGroupGuard(signalGroup)
	cmd.Cancel = func() error { return g.cancel(cmd.Process.Pid) }
	// Wait blocks on the output pipes until every holder of them is gone, so a
	// surviving group member would hang it. WaitDelay is only the backstop for
	// that: it must not fire before the group SIGKILL.
	cmd.WaitDelay = grace + composeWaitDelaySlack

	if err := cmd.Start(); err != nil {
		return err
	}
	pgid := cmd.Process.Pid

	exited := make(chan struct{})
	go func() {
		waitLeaderExit(pgid)
		close(exited)
	}()

	superviseGroup(g, pgid, exited, grace)

	// Nothing signals the group after this point: the reap below releases the
	// PGID.
	g.finish()
	return cmd.Wait()
}

// superviseGroup returns once the leader has exited and the group has no live
// member, or the SIGKILL bound has run out. The leader is still unreaped when
// it returns.
func superviseGroup(g *groupGuard, pgid int, exited <-chan struct{}, grace time.Duration) {
	killed := false
	kill := func() {
		_ = g.send(pgid, syscall.SIGKILL)
		killed = true
	}

	cancelCh := g.cancelCh
	var graceTimer <-chan time.Time
	armGrace := func() {
		at, _ := g.cancelTime()
		t := time.NewTimer(time.Until(at.Add(grace)))
		graceTimer = t.C
		// The timer is only ever read here; letting it fire unread is fine.
	}

	// Leader still running.
running:
	for {
		select {
		case <-exited:
			break running
		case <-cancelCh:
			cancelCh = nil
			armGrace()
		case <-graceTimer:
			graceTimer = nil
			kill()
		}
	}

	// Leader gone: wait for the rest of the group.
	deadline := time.Now().Add(grace)
	if at, ok := g.cancelTime(); ok {
		deadline = at.Add(grace)
	}
	ticker := time.NewTicker(composeGroupProbeInterval)
	defer ticker.Stop()
	for groupMembers(pgid) > 0 {
		if now := time.Now(); !now.Before(deadline) {
			if killed {
				return
			}
			kill()
			deadline = now.Add(composeGroupReapTimeout)
		}
		select {
		case <-ticker.C:
		case <-cancelCh:
			cancelCh = nil
			if !killed {
				at, _ := g.cancelTime()
				deadline = at.Add(grace)
			}
		}
	}
}

// waitLeaderExit blocks until pid has exited, without reaping it.
func waitLeaderExit(pid int) {
	var info unix.Siginfo
	for {
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if err != unix.EINTR {
			return
		}
	}
}

// liveGroupMembers counts processes in group pgid that are not zombies.
func liveGroupMembers(pgid int) int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue // exited between the listing and the read
		}
		if state, grp, ok := parseProcStat(b); ok && grp == pgid && state != 'Z' {
			n++
		}
	}
	return n
}

// parseProcStat extracts the state and process group from a /proc/N/stat
// line. comm (field 2) may contain spaces and parentheses, so the rest of the
// line is only split after the last ')'.
func parseProcStat(b []byte) (state byte, pgrp int, ok bool) {
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return 0, 0, false
	}
	// After comm: state ppid pgrp ...
	f := strings.Fields(string(b[i+1:]))
	if len(f) < 3 || len(f[0]) != 1 {
		return 0, 0, false
	}
	pg, err := strconv.Atoi(f[2])
	if err != nil {
		return 0, 0, false
	}
	return f[0][0], pg, true
}
