// SPDX-License-Identifier: MIT

package edge

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/docker"
	"github.com/codeswhat/portwing/internal/protocol"
)

// slowSocketDocker serves a fake dockerd on a unix socket that answers every
// non-/version request after delay, and returns a real docker.Client with a
// 1 s REQUEST_TIMEOUT pointed at it.
func slowSocketDocker(t *testing.T, delay time.Duration) *docker.Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "rt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			_, _ = io.WriteString(w, `{"Version":"24.0.5","ApiVersion":"1.45"}`)
			return
		}
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	c, err := docker.NewClient(sock, 1)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func hint(ms int64) *protocol.TimeoutHint { return &protocol.TimeoutHint{Ms: ms, Valid: true} }

func TestRequestTimeoutHintExtendsDeadline(t *testing.T) {
	c, ctrl := newTestClient(t)
	c.dockerClient = slowSocketDocker(t, 1500*time.Millisecond)
	runReadPump(t, c)

	sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
		RequestID: "hinted", Method: http.MethodPost, Path: "/containers/a/stop", TimeoutMs: hint(5000),
	})
	var resp protocol.ResponseMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeResponse), &resp)
	if resp.RequestID != "hinted" || resp.StatusCode != http.StatusOK {
		t.Fatalf("response = %+v", resp)
	}
}

func TestRequestTimeoutAutoDeadlineForStop(t *testing.T) {
	c, ctrl := newTestClient(t)
	c.dockerClient = slowSocketDocker(t, 1500*time.Millisecond)
	runReadPump(t, c)

	// t=1 gives max(1s, 1s+15s), so the 1.5 s stop survives a 1 s default.
	sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
		RequestID: "auto", Method: http.MethodPost, Path: "/containers/a/stop?t=1",
	})
	var resp protocol.ResponseMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeResponse), &resp)
	if resp.RequestID != "auto" {
		t.Fatalf("response = %+v", resp)
	}
}

func TestRequestTimeoutDefaultStillCodedError(t *testing.T) {
	c, ctrl := newTestClient(t)
	c.dockerClient = slowSocketDocker(t, 3*time.Second)
	runReadPump(t, c)

	start := time.Now()
	sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
		RequestID: "plain", Method: http.MethodGet, Path: "/info",
	})
	var e protocol.ErrorMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeError), &e)
	if e.Code != protocol.ErrorCodeRequestTimeout || e.RequestID != "plain" {
		t.Fatalf("error = %+v", e)
	}
	if e.Message != "docker request exceeded 1s" {
		t.Fatalf("message = %q", e.Message)
	}
	if time.Since(start) > 2500*time.Millisecond {
		t.Fatalf("default deadline not enforced, took %s", time.Since(start))
	}
}

func TestRequestTimeoutBodyStreamDeadlineStartsAtDispatch(t *testing.T) {
	c, ctrl := newTestClient(t)
	c.dockerClient = slowSocketDocker(t, 800*time.Millisecond)
	runReadPump(t, c)

	sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
		RequestID: "up", Method: http.MethodPost, Path: "/images/load", BodyStream: true, TimeoutMs: hint(1000),
	})
	// Spend longer than the hint uploading; the deadline hasn't started yet.
	time.Sleep(1200 * time.Millisecond)
	sendEnvelope(t, ctrl, protocol.TypeStream, protocol.StreamMessage{
		RequestID: "up", Data: base64.StdEncoding.EncodeToString([]byte("tar")),
	})
	sendEnvelope(t, ctrl, protocol.TypeStreamEnd, protocol.StreamEndMessage{RequestID: "up", Reason: "complete"})

	var resp protocol.ResponseMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeResponse), &resp)
	if resp.RequestID != "up" || resp.StatusCode != http.StatusOK {
		t.Fatalf("response = %+v", resp)
	}
}

func TestRequestTimeoutHintDecodesLeniently(t *testing.T) {
	t.Parallel()
	cases := []struct {
		json  string
		ms    int64
		valid bool
	}{
		{`60000`, 60000, true},
		{`0`, 0, true},
		{`-1`, -1, true},
		{`1.5`, 0, false},
		{`1e3`, 0, false},
		{`"60000"`, 0, false},
		{`null`, 0, false},
		{`true`, 0, false},
		{`99999999999999999999`, 0, false},
		{`{}`, 0, false},
	}
	for _, tc := range cases {
		var req protocol.RequestMessage
		raw := `{"requestId":"r","method":"GET","path":"/info","timeoutMs":` + tc.json + `}`
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			t.Errorf("%s: request must still decode, got %v", tc.json, err)
			continue
		}
		got := req.TimeoutMs != nil && req.TimeoutMs.Valid
		if got != tc.valid || (got && req.TimeoutMs.Ms != tc.ms) {
			t.Errorf("%s: hint = %+v, want valid=%v ms=%d", tc.json, req.TimeoutMs, tc.valid, tc.ms)
		}
	}
}

// The contract's headline cases, checked on the deadline handed to the Docker
// client rather than by sleeping through 60 or 90 seconds.
func TestRequestTimeoutDeadlinesForStopAndWait(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		hint       *protocol.TimeoutHint
		want       time.Duration
	}{
		{"stop hint 60s", "/containers/a/stop", hint(60000), 60 * time.Second},
		{"wait hint 90s", "/containers/a/wait", hint(90000), 90 * time.Second},
		{"stop t=60 auto", "/containers/a/stop?t=60", nil, 75 * time.Second},
		{"wait auto", "/containers/a/wait", nil, docker.MaxRequestTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, ctrl := newTestClient(t)
			fd := &fakeDocker{doResp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}"))}}
			c.dockerClient = fd
			runReadPump(t, c)
			sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
				RequestID: "d", Method: http.MethodPost, Path: tc.path, TimeoutMs: tc.hint,
			})
			expectType(t, ctrl, protocol.TypeResponse)
			fd.mu.Lock()
			defer fd.mu.Unlock()
			if len(fd.deadlines) != 1 || fd.deadlines[0] != tc.want {
				t.Fatalf("deadlines = %v, want [%v]", fd.deadlines, tc.want)
			}
		})
	}
}

func TestRequestTimeoutAutoDeadlineForWait(t *testing.T) {
	c, ctrl := newTestClient(t)
	c.dockerClient = slowSocketDocker(t, 1500*time.Millisecond)
	runReadPump(t, c)

	sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
		RequestID: "wait", Method: http.MethodPost, Path: "/containers/a/wait?condition=next-exit",
	})
	var resp protocol.ResponseMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeResponse), &resp)
	if resp.RequestID != "wait" || resp.StatusCode != http.StatusOK {
		t.Fatalf("response = %+v", resp)
	}
}

func TestHelloAdvertisesRequestTimeout(t *testing.T) {
	_, _, hello := managedEdgeClient(t)
	if !slices.Contains(hello.Capabilities, protocol.CapRequestTimeout) {
		t.Fatalf("hello capabilities %v missing %q", hello.Capabilities, protocol.CapRequestTimeout)
	}
}
