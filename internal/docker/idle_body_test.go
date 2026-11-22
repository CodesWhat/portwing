// SPDX-License-Identifier: MIT

package docker

import (
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

const idleTestTimeout = 40 * time.Millisecond

type scriptedRead struct {
	n   int
	err error
}

// fakeIdleReader returns scripted reads, optionally blocking each until gate
// is closed or receives.
type fakeIdleReader struct {
	reads  []scriptedRead
	gate   chan struct{}
	closed atomic.Bool
}

func (f *fakeIdleReader) Read(p []byte) (int, error) {
	if f.gate != nil {
		<-f.gate
	}
	r := f.reads[0]
	f.reads = f.reads[1:]
	return r.n, r.err
}

func (f *fakeIdleReader) Close() error {
	f.closed.Store(true)
	return nil
}

func newTestIdleBody(r io.ReadCloser) (*idleTimeoutBody, *atomic.Int32) {
	return newTestIdleBodyWithTimeout(r, idleTestTimeout)
}

func newTestIdleBodyWithTimeout(r io.ReadCloser, timeout time.Duration) (*idleTimeoutBody, *atomic.Int32) {
	var cancels atomic.Int32
	b := newIdleTimeoutBody(timeout, func() { cancels.Add(1) })
	b.body = r
	return b, &cancels
}

func TestIdleBodyEOFStopsTimer(t *testing.T) {
	t.Parallel()
	b, cancels := newTestIdleBody(&fakeIdleReader{reads: []scriptedRead{{3, nil}, {0, io.EOF}}})
	buf := make([]byte, 8)
	if _, err := b.Read(buf); err != nil {
		t.Fatalf("first Read: %v", err)
	}
	if _, err := b.Read(buf); !errors.Is(err, io.EOF) {
		t.Fatalf("second Read err = %v, want EOF", err)
	}
	time.Sleep(4 * idleTestTimeout)
	if got := cancels.Load(); got != 0 {
		t.Fatalf("cancel called %d times after EOF, want 0", got)
	}
	if b.timedOut() {
		t.Fatal("timer fired after EOF")
	}
}

func TestIdleBodyDataWithErrorStopsTimer(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	b, cancels := newTestIdleBody(&fakeIdleReader{reads: []scriptedRead{{5, boom}}})
	if n, err := b.Read(make([]byte, 8)); n != 5 || !errors.Is(err, boom) {
		t.Fatalf("Read = %d, %v, want 5, boom", n, err)
	}
	time.Sleep(4 * idleTestTimeout)
	if got := cancels.Load(); got != 0 {
		t.Fatalf("cancel called %d times after data+error, want 0", got)
	}
}

func TestIdleBodyCloseDuringReadDoesNotRearm(t *testing.T) {
	t.Parallel()
	gate := make(chan struct{})
	fake := &fakeIdleReader{reads: []scriptedRead{{4, nil}}, gate: gate}
	b, cancels := newTestIdleBody(fake)

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		_, _ = b.Read(make([]byte, 8))
	}()
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := cancels.Load(); got != 1 {
		t.Fatalf("cancel calls after Close = %d, want 1", got)
	}
	close(gate) // the Read now returns bytes after Close finished
	<-readDone
	time.Sleep(4 * idleTestTimeout)
	if got := cancels.Load(); got != 1 {
		t.Fatalf("cancel calls = %d, want 1: the timer re-armed after Close", got)
	}
}

func TestIdleBodyProgressingReadIsNeverCancelled(t *testing.T) {
	t.Parallel()
	reads := make([]scriptedRead, 0, 9)
	for range 8 {
		reads = append(reads, scriptedRead{2, nil})
	}
	reads = append(reads, scriptedRead{0, io.EOF})
	// A wide timeout with gaps a fifth of it keeps scheduler delay on a
	// loaded runner from looking like an idle stall; the total still runs
	// well past one timeout.
	const timeout = time.Second
	b, cancels := newTestIdleBodyWithTimeout(&fakeIdleReader{reads: reads}, timeout)
	buf := make([]byte, 8)
	for range 8 {
		time.Sleep(timeout / 5)
		if _, err := b.Read(buf); err != nil {
			t.Fatalf("Read: %v", err)
		}
		if cancels.Load() != 0 || b.timedOut() {
			t.Fatal("cancelled while bytes were arriving")
		}
	}
}

func TestIdleBodyFiredTimerSurfacesAsFailure(t *testing.T) {
	t.Parallel()
	gate := make(chan struct{})
	fake := &fakeIdleReader{reads: []scriptedRead{{0, io.EOF}}, gate: gate}
	b, cancels := newTestIdleBody(fake)
	time.Sleep(4 * idleTestTimeout)
	if cancels.Load() != 1 || !b.timedOut() {
		t.Fatalf("timer did not fire: cancels=%d", cancels.Load())
	}
	close(gate)
	_, err := b.Read(make([]byte, 8))
	if !errors.Is(err, ErrIdleLogRead) || errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want a wrapped timeout error, not clean EOF", err)
	}
}

// A timer that fires while the body is being retired must not cancel: the
// callback blocks on the mutex, retire marks the body done, and the callback
// then sees done and returns.
func TestIdleBodyTimerFiringAfterRetireDoesNotCancel(t *testing.T) {
	t.Parallel()

	var cancels atomic.Int32
	b := newIdleTimeoutBody(time.Millisecond, func() { cancels.Add(1) })

	b.mu.Lock()
	time.Sleep(30 * time.Millisecond) // the callback is now parked on b.mu
	b.done = true
	b.mu.Unlock()
	time.Sleep(30 * time.Millisecond)

	if b.timedOut() {
		t.Fatal("timer marked a retired body as timed out")
	}
	if got := cancels.Load(); got != 0 {
		t.Fatalf("cancel called %d times, want 0", got)
	}
}
