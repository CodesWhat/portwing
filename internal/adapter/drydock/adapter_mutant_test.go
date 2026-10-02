package drydock

// adapter_mutant_test.go adds tests that target Gremlins mutants in adapter.go:
// boundary, negation and arithmetic conditions that existing tests exercised
// but did not pin down at the exact boundary or on both sides of the
// comparison.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/protocol"
)

// followLogWindow and followLogGrace are vars only so tests can shrink them.
// These copies are taken at package initialisation, before any test has run,
// so the production values stay observable however the tests are ordered.
var (
	productionFollowLogWindow = followLogWindow
	productionFollowLogGrace  = followLogGrace
)

// lockedLogSink is a mutex-guarded slog sink. The default logger is
// process-wide, so a handler goroutine left over from an earlier test can
// still log while the test that swapped the logger reads the buffer back.
type lockedLogSink struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *lockedLogSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *lockedLogSink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// captureDebugLogs swaps the default logger for one that records every level
// into the returned sink, and restores the previous logger when the test ends.
// Callers must not run in parallel: the default logger is shared.
func captureDebugLogs(t *testing.T) *lockedLogSink {
	t.Helper()

	sink := &lockedLogSink{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(sink, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return sink
}

// loggedFor reports whether the captured output holds a record with the given
// message about the given container. Matching on the container keeps a stray
// record from another test's goroutine from satisfying, or failing, the check.
func loggedFor(output, message, containerID string) bool {
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, message) && strings.Contains(line, "container="+containerID) {
			return true
		}
	}
	return false
}

// TestMaxContainerLogBytesValue pins the computed value of
// maxContainerLogBytes against the two ARITHMETIC_BASE mutants on its
// `100 * 1024 * 1024` literal (adapter.go:35:29, adapter.go:35:36). Either
// multiplication turned into a division collapses the cap to 0 or to 100
// bytes; the second survives every behavioural test, none of which sends a
// buffered log response larger than that.
//
// Gremlins scores both NOT COVERED whatever tests exist: a package-level
// declaration sits outside every block of the coverage profile it reads. This
// is the check that fails if either is applied by hand.
func TestMaxContainerLogBytesValue(t *testing.T) {
	t.Parallel()

	const want = 100 * 1024 * 1024
	if maxContainerLogBytes != want {
		t.Fatalf("maxContainerLogBytes = %d, want %d", maxContainerLogBytes, want)
	}
}

// TestFollowLogWindowAndGraceDefaults pins the production follow window and
// grace against the ARITHMETIC_BASE mutants on `5 * time.Second`
// (adapter.go:47:22) and `2 * time.Second` (adapter.go:48:22). Either turned
// into a division is a zero duration, which every other test hides by
// overriding the vars or by using a daemon that answers at once. Like the
// constant above, Gremlins scores these NOT COVERED regardless.
func TestFollowLogWindowAndGraceDefaults(t *testing.T) {
	t.Parallel()

	if got, want := productionFollowLogWindow, 5*time.Second; got != want {
		t.Errorf("followLogWindow = %v, want %v", got, want)
	}
	if got, want := productionFollowLogGrace, 2*time.Second; got != want {
		t.Errorf("followLogGrace = %v, want %v", got, want)
	}
}

// TestHandleContainerLogRequestSendsTailOnlyWhenPositive pins the
// `msg.Tail > 0` guard in handleContainerLogRequest on both sides of zero,
// killing the CONDITIONALS_BOUNDARY and CONDITIONALS_NEGATION mutants at
// adapter.go:350:14. A zero tail means "all logs" and must reach the daemon as
// no tail parameter at all: `>=` sends tail=0, which asks the daemon for no
// lines, and `<=` drops a real tail while sending the zero.
func TestHandleContainerLogRequestSendsTailOnlyWhenPositive(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		tail     int
		wantSent bool
		wantTail string
	}{
		{name: "zero is sent as no tail", tail: 0},
		{name: "negative is sent as no tail", tail: -3},
		{name: "one is forwarded", tail: 1, wantSent: true, wantTail: "1"},
		{name: "a larger tail is forwarded", tail: 25, wantSent: true, wantTail: "25"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client, calls, shutdown := newRouteTestDockerClient(t)
			defer shutdown()

			a := NewAdapter(client, "test-agent", AgentInfo{})
			sender := newSyncCaptureSender()
			a.handleContainerLogRequest(context.Background(), sender, protocol.DDContainerLogRequestMessage{
				RequestID:   "req-tail",
				ContainerID: "container-1",
				Tail:        tt.tail,
			})

			query := lastLogsQuery(t, calls)
			if got := query.Has("tail"); got != tt.wantSent {
				t.Fatalf("daemon query %q: tail sent = %v, want %v", query.Encode(), got, tt.wantSent)
			}
			if got := query.Get("tail"); got != tt.wantTail {
				t.Fatalf("daemon query %q: tail = %q, want %q", query.Encode(), got, tt.wantTail)
			}
		})
	}
}

// TestRunContainerLogStreamSendsTailOnlyWhenPositive is the same boundary for
// the streaming path, killing the CONDITIONALS_BOUNDARY mutant at
// adapter.go:465:14. The existing stream tests only ever ask for a positive
// tail, which `>=` forwards just as well.
func TestRunContainerLogStreamSendsTailOnlyWhenPositive(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		tail     int
		wantSent bool
		wantTail string
	}{
		{name: "zero is sent as no tail", tail: 0},
		{name: "one is forwarded", tail: 1, wantSent: true, wantTail: "1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client, calls, shutdown := newRouteTestDockerClient(t)
			defer shutdown()

			a := NewAdapter(client, "test-agent", AgentInfo{})
			sender := newLogStreamTestSender()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Called directly rather than through startContainerLogStream, so
			// the stream has run to its end by the time this returns.
			a.runContainerLogStream(ctx, cancel, sender, protocol.DDContainerLogRequestMessage{
				RequestID:   "req-stream-tail",
				ContainerID: "container-1",
				Tail:        tt.tail,
				Stream:      true,
			})

			query := lastLogsQuery(t, calls)
			if got := query.Has("tail"); got != tt.wantSent {
				t.Fatalf("daemon query %q: tail sent = %v, want %v", query.Encode(), got, tt.wantSent)
			}
			if got := query.Get("tail"); got != tt.wantTail {
				t.Fatalf("daemon query %q: tail = %q, want %q", query.Encode(), got, tt.wantTail)
			}
		})
	}
}

// TestHandleContainerLogRequestFollowDeadlineIsWindowPlusGrace kills the
// ARITHMETIC_BASE mutant at adapter.go:369:57, which turns the follow
// request's context deadline from followLogWindow+followLogGrace into
// followLogWindow-followLogGrace. With the grace longer than the window the
// difference is negative, so the mutated handler starts with a context that
// has already expired and reports an error where the real one, whose deadline
// is an hour out, returns the daemon's logs. Nothing waits on either timer.
func TestHandleContainerLogRequestFollowDeadlineIsWindowPlusGrace(t *testing.T) {
	originalWindow, originalGrace := followLogWindow, followLogGrace
	followLogWindow = time.Second
	followLogGrace = time.Hour
	defer func() {
		followLogWindow, followLogGrace = originalWindow, originalGrace
	}()

	client, calls, shutdown := newRouteTestDockerClient(t)
	defer shutdown()

	a := NewAdapter(client, "test-agent", AgentInfo{})
	sender := newSyncCaptureSender()
	a.handleContainerLogRequest(context.Background(), sender, protocol.DDContainerLogRequestMessage{
		RequestID:   "req-follow-sum",
		ContainerID: "container-1",
		Follow:      true,
	})

	resp, ok := sender.Data().(protocol.DDContainerLogResponseMessage)
	if !ok {
		t.Fatalf("response payload type = %T, want protocol.DDContainerLogResponseMessage", sender.Data())
	}
	if resp.Logs != "log line\n" {
		t.Fatalf("Logs = %q, want the daemon's %q: the follow deadline must not have fired", resp.Logs, "log line\n")
	}
	if got := calls.logsCalls.Load(); got != 1 {
		t.Fatalf("daemon log calls = %d, want 1", got)
	}
}

// TestHandleContainerLogRequestReportsOnlyADecodeThatFailed pins both sides of
// the `err != nil` guard around handleContainerLogRequest's "decoding
// container logs" debug line, killing the CONDITIONALS_NEGATION mutant at
// adapter.go:392:9. The response carries whatever bytes were read either way,
// so this line is the only trace of a stream that was cut short: negated, a
// truncated stream goes unrecorded and every clean one is reported as a
// decode failure with a nil error.
func TestHandleContainerLogRequestReportsOnlyADecodeThatFailed(t *testing.T) {
	const decodeFailed = "decoding container logs"

	t.Run("clean stream is not reported", func(t *testing.T) {
		client, _, shutdown := newRouteTestDockerClient(t)
		defer shutdown()
		logs := captureDebugLogs(t)

		a := NewAdapter(client, "test-agent", AgentInfo{})
		sender := newSyncCaptureSender()
		a.handleContainerLogRequest(context.Background(), sender, protocol.DDContainerLogRequestMessage{
			RequestID:   "req-decode-clean",
			ContainerID: "decode-clean",
		})

		resp, ok := sender.Data().(protocol.DDContainerLogResponseMessage)
		if !ok {
			t.Fatalf("response payload type = %T, want protocol.DDContainerLogResponseMessage", sender.Data())
		}
		if resp.Logs != "log line\n" {
			t.Fatalf("Logs = %q, want %q", resp.Logs, "log line\n")
		}
		if got := logs.String(); loggedFor(got, decodeFailed, "decode-clean") {
			t.Fatalf("a stream that decoded cleanly was reported as failed: %s", got)
		}
	})

	t.Run("truncated frame is reported with its error", func(t *testing.T) {
		client, calls, shutdown := newRouteTestDockerClient(t)
		defer shutdown()
		calls.setLogsResponse("decode-truncated",
			append(routeTestDockerLogHeader(1, uint32(len("partial\n"))), []byte("part")...))
		logs := captureDebugLogs(t)

		a := NewAdapter(client, "test-agent", AgentInfo{})
		sender := newSyncCaptureSender()
		a.handleContainerLogRequest(context.Background(), sender, protocol.DDContainerLogRequestMessage{
			RequestID:   "req-decode-truncated",
			ContainerID: "decode-truncated",
		})

		resp, ok := sender.Data().(protocol.DDContainerLogResponseMessage)
		if !ok {
			t.Fatalf("response payload type = %T, want protocol.DDContainerLogResponseMessage", sender.Data())
		}
		if resp.Logs != "part" {
			t.Fatalf("Logs = %q, want the bytes read before the cut, %q", resp.Logs, "part")
		}
		got := logs.String()
		if !loggedFor(got, decodeFailed, "decode-truncated") {
			t.Fatalf("a truncated stream left no %q record: %s", decodeFailed, got)
		}
		if !strings.Contains(got, "unexpected EOF") {
			t.Fatalf("the %q record does not carry the read error: %s", decodeFailed, got)
		}
	})
}

// TestStartContainerLogStreamAdmission pins the two admission checks in
// startContainerLogStream at their exact edges: the cap rejects at
// maxContainerLogStreams active streams and admits one below it, and a
// requestId is a duplicate only while a stream is registered under it. Each
// refusal is matched on its own message, so one check cannot stand in for the
// other. It fails for the CONDITIONALS_BOUNDARY and CONDITIONALS_NEGATION
// mutants on the cap and the CONDITIONALS_NEGATION mutant on the duplicate
// check.
//
// Deliberately not parallel. The mutation run drives the suite with -failfast,
// which stops starting tests after the first failure, so failing here, early
// and without waiting, ends a mutated run before the parallel stream tests
// begin and each spend a two-second wait on an event that never comes.
func TestStartContainerLogStreamAdmission(t *testing.T) {
	const (
		refusedAtCap     = "too many active container log streams"
		refusedDuplicate = "duplicate requestId"
	)

	for _, tt := range []struct {
		name       string
		active     int    // streams already registered under other request IDs
		registered bool   // whether the requested ID is already registered
		wantError  string // "" means the stream must be admitted
	}{
		{name: "no active streams", active: 0},
		{name: "one below the cap", active: maxContainerLogStreams - 1},
		{name: "at the cap", active: maxContainerLogStreams, wantError: refusedAtCap},
		{name: "requestId already streaming", active: 1, registered: true, wantError: refusedDuplicate},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client, _, shutdown := newRouteTestDockerClient(t)
			defer shutdown()

			a := NewAdapter(client, "test-agent", AgentInfo{})
			a.logStreamsMu.Lock()
			for i := 0; i < tt.active; i++ {
				a.logStreams[fmt.Sprintf("other-%d", i)] = activeContainerLogStream{
					containerID: "container-1",
					cancel:      func() {},
				}
			}
			if tt.registered {
				a.logStreams["req-admission"] = activeContainerLogStream{
					containerID: "container-1",
					cancel:      func() {},
				}
			}
			a.logStreamsMu.Unlock()

			sender := newLogStreamTestSender()
			a.startContainerLogStream(context.Background(), sender, protocol.DDContainerLogRequestMessage{
				RequestID:   "req-admission",
				ContainerID: "container-1",
				Stream:      true,
			})

			first := waitForLogStreamEvent(t, sender)
			if tt.wantError == "" {
				if first.msgType != protocol.TypeDDContainerLogChunk {
					t.Fatalf("first message = %q %s, want the stream admitted and its first %q",
						first.msgType, first.data, protocol.TypeDDContainerLogChunk)
				}
				// Let the admitted stream run out before the fake daemon goes away.
				if end := waitForLogStreamEvent(t, sender); end.msgType != protocol.TypeDDContainerLogEnd {
					t.Fatalf("second message = %q %s, want %q", end.msgType, end.data, protocol.TypeDDContainerLogEnd)
				}
				return
			}

			if first.msgType != protocol.TypeDDContainerLogError {
				t.Fatalf("first message = %q %s, want the stream refused with %q",
					first.msgType, first.data, protocol.TypeDDContainerLogError)
			}
			var refusal protocol.DDContainerLogErrorMessage
			if err := json.Unmarshal(first.data, &refusal); err != nil {
				t.Fatalf("decode refusal: %v", err)
			}
			if refusal.Error != tt.wantError {
				t.Fatalf("refusal = %q, want %q", refusal.Error, tt.wantError)
			}
		})
	}
}
