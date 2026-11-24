// SPDX-License-Identifier: MIT

//go:build !linux

package docker

import (
	"os/exec"
	"syscall"
	"time"
)

// runInProcessGroup runs cmd in its own process group. This is the non-Linux
// (darwin, dev-only) variant with a weaker guarantee than Linux: there is no
// /proc to find group members and no way to keep the leader's PGID reserved
// after it is reaped. So on cancellation the group gets SIGTERM and then
// SIGKILL after composeStopGrace, but once cmd.Wait has returned no group
// signal is ever sent, and a member that outlived the leader is left running.
//
// Accepted race: the grace timer can fire between cmd.Wait returning (which
// reaps the leader and may free its PGID) and g.finish taking the guard, and
// SIGKILL then goes to -pgid. Closing it needs Wait and finish to be atomic
// with respect to the guard, but holding the guard across Wait would block the
// cancel path that Wait itself depends on, and setting done before Wait returns
// would stop the SIGKILL from ever firing on a live group. The window is the
// few instructions between the two calls, needs a cancellation whose grace
// expires at that instant, and the PGID must be reused by then. Darwin is a
// dev-only build target, so this is left as is.
func runInProcessGroup(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	grace := composeStopGrace
	g := newGroupGuard(signalGroup)
	cmd.Cancel = func() error {
		pgid := cmd.Process.Pid
		err := g.cancel(pgid)
		if err == nil {
			time.AfterFunc(grace, func() { _ = g.send(pgid, syscall.SIGKILL) })
		}
		return err
	}
	cmd.WaitDelay = grace + composeWaitDelaySlack

	if err := cmd.Start(); err != nil {
		return err
	}
	err := cmd.Wait()
	g.finish()
	return err
}
