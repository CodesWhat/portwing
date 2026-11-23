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
