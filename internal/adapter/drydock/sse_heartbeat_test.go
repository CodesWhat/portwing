// SPDX-License-Identifier: MIT

package drydock

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSSEHeartbeatIntervalDefault(t *testing.T) {
	if sseHeartbeatInterval > 15*time.Second {
		t.Fatalf("sseHeartbeatInterval = %v, want <= 15s", sseHeartbeatInterval)
	}
	if sseHeartbeatInterval >= 45*time.Second {
		t.Fatalf("sseHeartbeatInterval = %v, must be below Drydock's 45s watchdog", sseHeartbeatInterval)
	}
	b := NewSSEBroadcaster(fakeContainerProvider{}, "v-test", AgentInfo{})
	if b.heartbeatInterval != sseHeartbeatInterval {
		t.Fatalf("heartbeatInterval = %v, want %v", b.heartbeatInterval, sseHeartbeatInterval)
	}
}

func TestSSEHeartbeatSentAndEventsKeepFlowing(t *testing.T) {
	b := NewSSEBroadcaster(fakeContainerProvider{}, "v-test", AgentInfo{})
	b.heartbeatInterval = 20 * time.Millisecond

	srv := httptest.NewServer(b)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req) //nolint:bodyclose // closed via body alias below
	if err != nil {
		t.Fatal(err)
	}
	body := resp.Body
	defer func() { _ = body.Close() }()

	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()

	waitFor := func(match func(string) bool) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					t.Fatal("stream closed early")
				}
				if match(l) {
					return
				}
			case <-deadline:
				t.Fatal("timed out waiting for line")
			}
		}
	}

	waitFor(func(l string) bool { return l == ": heartbeat" })

	b.BroadcastContainerRemoved("abc", "web")
	waitFor(func(l string) bool { return strings.Contains(l, "dd:container-removed") })

	waitFor(func(l string) bool { return l == ": heartbeat" })
}

// failWriter succeeds for the first okWrites writes, then errors.
type failWriter struct {
	header   http.Header
	okWrites int
	writes   int
}

func (f *failWriter) Header() http.Header { return f.header }
func (f *failWriter) WriteHeader(int)     {}
func (f *failWriter) Flush()              {}
func (f *failWriter) Write(p []byte) (int, error) {
	f.writes++
	if f.writes > f.okWrites {
		return 0, errors.New("write failed")
	}
	return len(p), nil
}

func TestSSEHeartbeatWriteFailureRemovesClient(t *testing.T) {
	b := NewSSEBroadcaster(fakeContainerProvider{}, "v-test", AgentInfo{})
	b.heartbeatInterval = 10 * time.Millisecond

	// ack and watcher snapshot succeed; the heartbeat write fails.
	w := &failWriter{header: http.Header{}, okWrites: 2}
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)

	done := make(chan struct{})
	go func() {
		b.ServeHTTP(w, req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ServeHTTP did not return after heartbeat write failure")
	}

	b.mu.RLock()
	n := len(b.clients)
	b.mu.RUnlock()
	if n != 0 {
		t.Fatalf("clients = %d, want 0", n)
	}
}
