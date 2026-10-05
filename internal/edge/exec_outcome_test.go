package edge

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/protocol"
)

func newAuditedTestClient(t *testing.T) (*Client, *audit.Logger, func() protocol.Envelope) {
	t.Helper()
	c, ctrl := newTestClient(t)
	logger, closeAudit, err := audit.New("", 64)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeAudit)
	c.auditor = logger
	return c, logger, func() protocol.Envelope { return expectEnvelope(t, ctrl) }
}

func recordsOfEvent(logger *audit.Logger, event string) []audit.Record {
	var out []audit.Record
	for _, r := range logger.Records(0) {
		if r.Event == event {
			out = append(out, r)
		}
	}
	return out
}

// A typed exec_start is recorded once, at admission, as allowed; when the
// daemon then fails the create, the failure is an api_request error record
// under the exec_start message type, and no second exec_start record appears.
func TestTypedExecStartAdmittedThenDaemonFailureRecorded(t *testing.T) {
	t.Parallel()

	c, logger, next := newAuditedTestClient(t)
	c.dockerClient = &fakeDocker{createExecErr: errors.New("boom")}

	c.StartExec(context.Background(), protocol.ExecStartMessage{ExecID: "e1", ContainerID: "c1"})
	if env := next(); env.Type != protocol.TypeExecEnd {
		t.Fatalf("frame = %q, want exec_end", env.Type)
	}

	starts := recordsOfEvent(logger, audit.EventExecStart)
	if len(starts) != 1 || starts[0].Outcome != audit.OutcomeAllowed || starts[0].ExecID != "e1" || starts[0].Container != "c1" {
		t.Fatalf("exec_start records = %+v, want one allowed record", starts)
	}
	failures := recordsOfEvent(logger, audit.EventAPIRequest)
	if len(failures) != 1 || failures[0].Outcome != audit.OutcomeError || failures[0].Method != protocol.TypeExecStart || failures[0].Path != "c1" {
		t.Fatalf("api_request records = %+v, want one error record for the failed start", failures)
	}
}

func TestTypedExecStartRefusedIsDenied(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		setup func(c *Client)
		msg   protocol.ExecStartMessage
	}{
		{
			name: "session limit",
			setup: func(c *Client) {
				for i := 0; i < maxExecSessions; i++ {
					c.execSessions.Store("limit-"+strconv.Itoa(i), &ExecSession{})
				}
			},
			msg: protocol.ExecStartMessage{ExecID: "e1", ContainerID: "c1"},
		},
		{
			name:  "duplicate exec id",
			setup: func(c *Client) { c.execSessions.Store("e1", &ExecSession{}) },
			msg:   protocol.ExecStartMessage{ExecID: "e1", ContainerID: "c1"},
		},
		{
			name:  "missing exec id",
			setup: func(*Client) {},
			msg:   protocol.ExecStartMessage{ContainerID: "c1"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, logger, next := newAuditedTestClient(t)
			fd := &fakeDocker{}
			c.dockerClient = fd
			tc.setup(c)

			c.StartExec(context.Background(), tc.msg)
			next() // the refusal reply

			starts := recordsOfEvent(logger, audit.EventExecStart)
			if len(starts) != 1 || starts[0].Outcome != audit.OutcomeDenied || starts[0].ExecID != tc.msg.ExecID {
				t.Fatalf("exec_start records = %+v, want one denied record", starts)
			}
			fd.mu.Lock()
			created := len(fd.createCalls)
			fd.mu.Unlock()
			if created != 0 {
				t.Fatalf("refused exec reached the daemon (%d create calls)", created)
			}
		})
	}
}

// Through the read pump with the request cap free, an admitted raw exec start
// is recorded exactly once, by handleRequestTo. An audit moved ahead of the cap
// select in the pump would add a second record for the same attempt.
func TestReadPumpAdmittedRawExecStartAuditedOnce(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	logger, closeAudit, err := audit.New("", 20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeAudit)
	c.auditor = logger
	dc, paths := realDockerDaemon(t)
	c.dockerClient = dc

	runReadPump(t, c)
	sendEnvelope(t, ctrl, protocol.TypeRequest, protocol.RequestMessage{
		RequestID: "pump1", Method: http.MethodPost, Path: "/exec/abc/start",
		Body: []byte(`{"Detach":true}`),
	})
	expectType(t, ctrl, protocol.TypeResponse)
	select {
	case <-paths:
	case <-time.After(readTimeout):
		t.Fatal("admitted start never reached the daemon")
	}

	records := execRecords(logger)
	if len(records) != 1 || records[0].Outcome != audit.OutcomeAllowed || records[0].ExecID != "abc" {
		t.Fatalf("exec records = %+v, want exactly one allowed record", records)
	}
}

func fillPendingBodies(t *testing.T, c *Client) {
	t.Helper()
	c.pendingBodiesMu.Lock()
	defer c.pendingBodiesMu.Unlock()
	if c.pendingBodies == nil {
		c.pendingBodies = make(map[string]*pendingRequestBody)
	}
	for i := 0; i < maxPendingRequestBodies; i++ {
		timer := time.AfterFunc(time.Hour, func() {})
		t.Cleanup(func() { timer.Stop() })
		c.pendingBodies["fill-"+strconv.Itoa(i)] = &pendingRequestBody{timer: timer}
	}
}

// A streamed-body exec start turned away by the pending-body cap is recorded
// once as denied. A refused request that is not an exec start is not.
func TestPendingBodyCapRefusalAuditsRawExecStartDenied(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, path string
		want       int
	}{
		{"exec start", "/v1.47/exec/abc/start", 1},
		{"other request", "/build", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, logger, next := newAuditedTestClient(t)
			fillPendingBodies(t, c)

			c.registerPendingBody(protocol.RequestMessage{
				RequestID: "late", Method: http.MethodPost, Path: tc.path, BodyStream: true,
			}, c.currentOutboundTarget())

			env := next()
			var e protocol.ErrorMessage
			if env.Type != protocol.TypeError {
				t.Fatalf("frame = %q, want error", env.Type)
			}
			decodeData(t, env.Data, &e)
			if e.RequestID != "late" || e.Message != "agent busy: too many concurrent streamed request bodies" {
				t.Fatalf("error = %+v", e)
			}
			records := execRecords(logger)
			if len(records) != tc.want {
				t.Fatalf("exec records = %+v, want %d", records, tc.want)
			}
			if tc.want == 1 && (records[0].Outcome != audit.OutcomeDenied || records[0].ExecID != "abc" || records[0].Container != "/v1.47/exec/abc/start") {
				t.Fatalf("exec record = %+v", records[0])
			}
		})
	}
}

// A path the request handler rejects as invalid (no leading slash once the
// percent-encoding is left alone) is not an exec start, so a cap refusing it
// must not write an exec_start record for it at any of the three refusal sites.
func TestRefusedInvalidPathExecStartNotAudited(t *testing.T) {
	t.Parallel()

	const slashless = "%2Fexec/x/start"
	for _, site := range []string{"inline", "streamed", "pending-body"} {
		t.Run(site, func(t *testing.T) {
			t.Parallel()
			c, ctrl := newTestClient(t)
			logger, closeAudit, err := audit.New("", 20)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(closeAudit)
			c.auditor = logger
			req := protocol.RequestMessage{RequestID: "bad", Method: http.MethodPost, Path: slashless}

			switch site {
			case "inline":
				for i := 0; i < maxStreams; i++ {
					c.streamSem <- struct{}{}
				}
				runReadPump(t, c)
				sendEnvelope(t, ctrl, protocol.TypeRequest, req)
			case "streamed":
				for i := 0; i < maxStreams; i++ {
					c.streamSem <- struct{}{}
				}
				go c.dispatchStreamedBody(context.Background(), req, c.currentOutboundTarget(), 0)
			default:
				fillPendingBodies(t, c)
				req.BodyStream = true
				c.registerPendingBody(req, c.currentOutboundTarget())
			}
			expectType(t, ctrl, protocol.TypeError)

			if records := execRecords(logger); len(records) != 0 {
				t.Fatalf("exec records = %+v, want none for a path the handler rejects", records)
			}
		})
	}
}
