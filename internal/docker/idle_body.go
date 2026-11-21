// SPDX-License-Identifier: MIT

package docker

import (
	"context"
	"fmt"
	"io"
	"sync"
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

	mu   sync.Mutex // guards done and every Reset or Stop of timer
	done bool
}

func newIdleTimeoutBody(timeout time.Duration, cancel context.CancelFunc) *idleTimeoutBody {
	b := &idleTimeoutBody{timeout: timeout, cancel: cancel}
	b.timer = time.AfterFunc(timeout, func() {
		b.mu.Lock()
		if b.done {
			b.mu.Unlock()
			return
		}
		b.fired.Store(true)
		b.mu.Unlock()
		cancel()
	})
	return b
}

func (b *idleTimeoutBody) timedOut() bool { return b.fired.Load() }

// stop disarms the timer and releases the request context.
func (b *idleTimeoutBody) stop() {
	b.retire()
	b.cancel()
}

// retire marks the body done and stops the timer. After it returns the
// callback can no longer cancel and Read can no longer re-arm.
func (b *idleTimeoutBody) retire() {
	b.mu.Lock()
	b.done = true
	b.timer.Stop()
	b.mu.Unlock()
}

func (b *idleTimeoutBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	b.mu.Lock()
	if err != nil {
		b.done = true
		b.timer.Stop()
	} else if n > 0 && !b.done {
		b.timer.Reset(b.timeout)
	}
	b.mu.Unlock()
	if err != nil && b.fired.Load() {
		err = fmt.Errorf("no data from docker for %s: %w", b.timeout, err)
	}
	return n, err
}

func (b *idleTimeoutBody) Close() error {
	b.retire()
	err := b.body.Close()
	b.cancel()
	return err
}
