// SPDX-License-Identifier: MIT

package docker

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

// idleTimeoutBody cancels its request context when no bytes arrive for
// timeout, which unblocks a Read stuck on a daemon that stopped sending. The
// timer starts at construction, so it also covers the wait for response
// headers, and restarts on every Read that returns data.
type idleTimeoutBody struct {
	body    io.ReadCloser
	timeout time.Duration
	cancel  context.CancelFunc
	timer   *time.Timer
	fired   atomic.Bool
}

func newIdleTimeoutBody(timeout time.Duration, cancel context.CancelFunc) *idleTimeoutBody {
	b := &idleTimeoutBody{timeout: timeout, cancel: cancel}
	b.timer = time.AfterFunc(timeout, func() {
		b.fired.Store(true)
		cancel()
	})
	return b
}

func (b *idleTimeoutBody) timedOut() bool { return b.fired.Load() }

// stop disarms the timer and releases the request context.
func (b *idleTimeoutBody) stop() {
	b.timer.Stop()
	b.cancel()
}

func (b *idleTimeoutBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if n > 0 {
		b.timer.Reset(b.timeout)
	}
	if err != nil && b.fired.Load() {
		err = fmt.Errorf("no data from docker for %s: %w", b.timeout, err)
	}
	return n, err
}

func (b *idleTimeoutBody) Close() error {
	b.timer.Stop()
	err := b.body.Close()
	b.cancel()
	return err
}
