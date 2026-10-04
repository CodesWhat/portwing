package edge

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/protocol"
)

func execRecords(logger *audit.Logger) []audit.Record {
	var out []audit.Record
	for _, r := range logger.Records(0) {
		if r.Event == audit.EventExecStart {
			out = append(out, r)
		}
	}
	return out
}

func TestRawExecStartIDClassification(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		method, path, id string
		ok               bool
	}{
		{http.MethodPost, "/exec/abc/start", "abc", true},
		{http.MethodPost, "/v1.47/exec/abc/start", "abc", true},
		{http.MethodPost, "/v1.47/exec/abc/start?x=1", "abc", true},
		{http.MethodPost, "/v1.47/exec/a%2Fb/start", "a/b", true},
		{http.MethodGet, "/v1.47/exec/abc/start", "", false},
		{http.MethodPost, "/v1.47/exec/abc/json", "", false},
		{http.MethodPost, "/containers/create", "", false},
	} {
		id, ok := rawExecStartID(tc.method, tc.path)
		if id != tc.id || ok != tc.ok {
			t.Errorf("%s %s = (%q, %v), want (%q, %v)", tc.method, tc.path, id, ok, tc.id, tc.ok)
		}
	}
}

func TestRawDetachedExecStartAuditedAndSlotReleased(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	logger, closeAudit, err := audit.New("", 20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeAudit)
	c.auditor = logger
	//nolint:bodyclose // the response body is consumed and closed by handleRequest, the code under test.
	c.dockerClient = &fakeDocker{doResp: mkResp(http.StatusOK, "", ""), streamResp: mkResp(http.StatusOK, "", "")}

	c.handleRequest(context.Background(), protocol.RequestMessage{
		RequestID: "raw1", Method: http.MethodPost, Path: "/v1.47/exec/abc123/start",
		Body: []byte(`{"Detach":true}`),
	})
	var resp protocol.ResponseMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeResponse), &resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	records := execRecords(logger)
	if len(records) != 1 || records[0].ExecID != "abc123" || records[0].Outcome != audit.OutcomeAllowed {
		t.Fatalf("exec records = %+v", records)
	}
	c.execAdmissionMu.Lock()
	held := c.rawExecStarts
	c.execAdmissionMu.Unlock()
	if held != 0 {
		t.Fatalf("rawExecStarts = %d after response, want 0", held)
	}
}

func TestRawExecStartRefusedWhenTypedSessionsFillCap(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	logger, closeAudit, err := audit.New("", 20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeAudit)
	c.auditor = logger
	fd := &fakeDocker{}
	c.dockerClient = fd
	for i := 0; i < maxExecSessions; i++ {
		c.execSessions.Store("limit-"+strconv.Itoa(i), &ExecSession{})
	}

	c.handleRequest(context.Background(), protocol.RequestMessage{
		RequestID: "raw2", Method: http.MethodPost, Path: "/exec/abc/start",
	})
	var e protocol.ErrorMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeError), &e)
	if e.RequestID != "raw2" || e.Message != "agent busy: exec session limit reached" {
		t.Fatalf("error = %+v", e)
	}
	if got := len(execRecords(logger)); got != 1 {
		t.Fatalf("exec records = %d, want 1 (refused attempts are audited like the typed path)", got)
	}
}

func TestTypedExecRefusedWhenRawStartsFillCap(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	c.execAdmissionMu.Lock()
	c.rawExecStarts = maxExecSessions
	c.execAdmissionMu.Unlock()

	c.StartExec(context.Background(), protocol.ExecStartMessage{ExecID: "typed", ContainerID: "c1", Cmd: []string{"sh"}})
	var end protocol.ExecEndMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeExecEnd), &end)
	if end.ExecID != "typed" || end.Reason != "session limit reached" {
		t.Fatalf("exec_end = %+v", end)
	}
}

func TestRawExecSlotsShareCapWithTypedSessions(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t)
	for i := 0; i < maxExecSessions-1; i++ {
		c.execSessions.Store("s-"+strconv.Itoa(i), &ExecSession{})
	}
	if !c.acquireRawExecSlot() {
		t.Fatal("last free slot refused")
	}
	if c.acquireRawExecSlot() {
		t.Fatal("slot admitted past the shared cap")
	}
	c.releaseRawExecSlot()
	if !c.acquireRawExecSlot() {
		t.Fatal("released slot not reusable")
	}
}

func TestNonExecRawRequestUnchanged(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	logger, closeAudit, err := audit.New("", 20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeAudit)
	c.auditor = logger
	//nolint:bodyclose // the response body is consumed and closed by handleRequest, the code under test.
	c.dockerClient = &fakeDocker{doResp: mkResp(http.StatusOK, "application/json", `{}`)}
	c.execAdmissionMu.Lock()
	c.rawExecStarts = maxExecSessions
	c.execAdmissionMu.Unlock()

	c.handleRequest(context.Background(), protocol.RequestMessage{RequestID: "n1", Method: http.MethodPost, Path: "/containers/create"})
	expectType(t, ctrl, protocol.TypeResponse)
	if got := len(execRecords(logger)); got != 0 {
		t.Fatalf("non-exec request produced %d exec records", got)
	}
}
