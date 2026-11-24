// SPDX-License-Identifier: MIT

package docker

import (
	"errors"
	"os"
	"sync"
	"syscall"
	"time"
)

// Portwing builds for linux and darwin only (see .goreleaser.yml). The
// platform-specific runInProcessGroup lives in compose_procgroup_linux.go and
// compose_procgroup_other.go.

var (
	// composeWaitDelaySlack keeps os/exec's WaitDelay behind the group SIGKILL:
	// the pipe readers are only force-closed once the whole group is dead.
	composeWaitDelaySlack = 5 * time.Second

	// signalGroup is syscall.Kill, a variable so tests can observe or
	// suppress group signals.
	signalGroup = syscall.Kill
)

// groupGuard serialises every signal sent to the process group with the end of
// the run. Once finish has returned no signal is sent, so a late context
// cancellation (or a grace timer) can never reach a PGID that has been
// released and reused.
type groupGuard struct {
	signal func(int, syscall.Signal) error

	mu          sync.Mutex
	done        bool
	cancelled   bool
	cancelledAt time.Time
	cancelCh    chan struct{}
}

func newGroupGuard(signal func(int, syscall.Signal) error) *groupGuard {
	return &groupGuard{signal: signal, cancelCh: make(chan struct{})}
}

// send signals the group unless the run has finished.
func (g *groupGuard) send(pgid int, sig syscall.Signal) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.done {
		return os.ErrProcessDone
	}
	return g.signal(-pgid, sig)
}

// cancel is the cmd.Cancel hook: SIGTERM to the group, once the run is still
// live, and a record of when cancellation happened.
func (g *groupGuard) cancel(pgid int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.done {
		return os.ErrProcessDone
	}
	err := g.signal(-pgid, syscall.SIGTERM)
	if !g.cancelled {
		g.cancelled = true
		g.cancelledAt = time.Now()
		close(g.cancelCh)
	}
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

// finish ends signalling. It blocks while a signal is in flight.
func (g *groupGuard) finish() {
	g.mu.Lock()
	g.done = true
	g.mu.Unlock()
}
