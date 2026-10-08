package edge

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/docker"
	applog "github.com/codeswhat/portwing/internal/log"
	"github.com/codeswhat/portwing/internal/pool"
	"github.com/codeswhat/portwing/internal/protocol"
)

// execInputQueue bounds the per-session input backlog. Input and resizes are
// decoded on the read loop and handed to a single writer goroutine, so this
// buffers the burst that can arrive before the Docker exec is live (and any
// momentary write stall) without ever blocking the read pump.
const (
	execInputQueue       = 256
	execInputFrameLimit  = 64 << 10
	execInputQueuedLimit = 1 << 20
)

// execItem is one ordered unit drained by inputWriter: either stdin bytes to
// write to the exec, or a TTY resize. Routing both through the single drainer
// keeps them in arrival order and — critically — off the read pump, so a slow
// or failing resize can never stall ping/exec dispatch.
type execItem struct {
	data          []byte    // stdin bytes; nil for a resize
	reservedBytes int       // bytes reserved against the per-session input budget
	resize        *resizeOp // non-nil for a resize
}

// resizeOp is a pending TTY resize.
type resizeOp struct {
	cols int
	rows int
}

// ExecSession represents an active exec session tunneled over WebSocket.
//
// Input ordering is the session's core invariant: a single inputWriter
// goroutine drains inbox in arrival order, so keystrokes (and resizes) that race
// ahead of the Docker exec coming up are buffered and replayed in order rather
// than dropped.
type ExecSession struct {
	execID      string // controller-assigned exec ID (used on the wire)
	containerID string
	client      *Client
	target      outboundTarget

	// tty records whether the exec was created with a PTY, which decides how
	// readLoop reads the hijacked stream: a TTY exec is raw bytes, a non-TTY
	// one is Docker's stdcopy multiplexing and has to be demultiplexed. It is
	// written in StartExec before any session goroutine starts, so the read
	// loop observes it without synchronisation.
	tty bool

	// dockerExecID is the Docker-assigned exec instance ID returned by
	// CreateExec. It differs from execID (which is the controller's ID) and is
	// the one Docker's resize endpoint expects. Written once in bringUpExec
	// before connReady is closed; inputWriter only reads it after <-connReady,
	// so the channel close publishes it without a separate lock.
	dockerExecID string

	// conn is the hijacked Docker exec stream. It is nil until the exec is
	// brought up; readers synchronize through connReady (or the mu-guarded
	// closed flag during teardown).
	conn      net.Conn
	connReady chan struct{} // closed once conn is live and ordered I/O may flow

	// inbox carries decoded input and resizes in arrival order for inputWriter
	// to drain.
	inbox chan execItem

	done   chan struct{}
	once   sync.Once
	cancel context.CancelFunc

	mu               sync.Mutex
	closed           bool
	queuedInputBytes int
}

// StartExec registers the exec session synchronously, then brings the Docker
// exec up asynchronously. Registering up front is what makes input ordered:
// exec_input that arrives immediately after exec_start finds the session and is
// queued, instead of racing the bring-up and being dropped.
func (c *Client) StartExec(ctx context.Context, msg protocol.ExecStartMessage) {
	target := c.currentOutboundTarget()
	if msg.ExecID == "" {
		c.auditTypedExecStart(msg, false)
		_ = c.sendTypedMessageTo(target, protocol.TypeExecEnd, protocol.ExecEndMessage{
			Reason: "exec ID is required",
		})
		return
	}

	sessionCtx, sessionCancel := context.WithCancel(ctx)
	session := &ExecSession{
		execID:      msg.ExecID,
		containerID: msg.ContainerID,
		client:      c,
		target:      target,
		// tty defaults to true when the field is absent (nil), preserving the
		// prior hardcoded behavior. Explicit false disables PTY allocation.
		tty:       msg.Tty == nil || *msg.Tty,
		connReady: make(chan struct{}),
		inbox:     make(chan execItem, execInputQueue),
		done:      make(chan struct{}),
		cancel:    sessionCancel,
	}
	admitted := false
	defer func() {
		if !admitted {
			sessionCancel()
		}
	}()

	decision := c.admitTypedExec(msg.ExecID, session)
	// The record is written after the admission lock is released, so audit IO
	// never runs under it, and before the bring-up is spawned.
	c.auditTypedExecStart(msg, decision == execAdmitted)

	if decision == execSlotsFull {
		slog.Warn("exec session limit reached", "max", maxExecSessions)
		// Best-effort error reply; connection loss will surface on the read pump.
		_ = c.sendTypedMessageTo(target, protocol.TypeExecEnd, protocol.ExecEndMessage{
			ExecID: msg.ExecID,
			Reason: "session limit reached",
		})
		return
	}

	if decision == execDuplicateID {
		// Error is the only existing non-terminal rejection shape. Correlation is
		// controller-dependent; exec_end would incorrectly close the live session.
		_ = c.sendTypedMessageTo(target, protocol.TypeError, protocol.ErrorMessage{
			Message:   "duplicate exec ID",
			Code:      "duplicate-exec-id",
			RequestID: msg.ExecID,
		})
		return
	}
	admitted = true

	go session.inputWriter(sessionCtx)
	go c.bringUpExec(sessionCtx, msg, session)
}

// typedExecAdmission is the result of admitting a typed exec session.
type typedExecAdmission int

const (
	execAdmitted typedExecAdmission = iota
	execSlotsFull
	execDuplicateID
)

// admitTypedExec decides whether a typed exec session may start and, when it
// may, registers it, all under execAdmissionMu so the cap shared with raw exec
// starts holds. It does no IO.
func (c *Client) admitTypedExec(execID string, session *ExecSession) typedExecAdmission {
	c.execAdmissionMu.Lock()
	defer c.execAdmissionMu.Unlock()
	if c.execSlotsFullLocked() {
		return execSlotsFull
	}
	if _, loaded := c.execSessions.LoadOrStore(execID, session); loaded {
		return execDuplicateID
	}
	return execAdmitted
}

// auditTypedExecStart writes the exec_start record for a typed exec_start with
// the admission result, before the bring-up reaches the Docker daemon.
func (c *Client) auditTypedExecStart(msg protocol.ExecStartMessage, admitted bool) {
	c.auditor.ExecStart(c.cfg.DrydockURL, msg.ContainerID, msg.ExecID, audit.ExecOutcome(admitted))
}

// bringUpExec performs the Docker round-trips for an already-registered session
// and, on success, wires the live connection and starts streaming.
func (c *Client) bringUpExec(ctx context.Context, msg protocol.ExecStartMessage, session *ExecSession) {
	defer recoverSession("bringUpExec", msg.ExecID)
	if ctx.Err() != nil || session.isClosed() {
		session.abortStart()
		return
	}

	tty := session.tty

	// Create exec instance.
	execID, err := c.dockerClient.CreateExec(ctx, msg.ContainerID, msg.Cmd, msg.User, tty)
	if err != nil {
		slog.Error("failed to create exec", "container", applog.Sanitize(msg.ContainerID), "error", applog.Sanitize(err.Error()))
		session.failStart(ctx, fmt.Sprintf("create exec failed: %v", err))
		return
	}
	if ctx.Err() != nil || session.isClosed() {
		session.abortStart()
		return
	}

	// Record the Docker exec ID so post-startup resizes target the instance
	// Docker actually knows about (not the controller's execID). Safe without a
	// lock: this write happens-before activate closes connReady, and the only
	// reader (inputWriter, via doResize) reads it only after <-connReady.
	session.dockerExecID = execID

	// Start exec and get hijacked connection.
	conn, err := c.dockerClient.StartExec(ctx, execID, tty)
	if err != nil {
		slog.Error("failed to start exec", "execID", applog.Sanitize(execID), "error", applog.Sanitize(err.Error()))
		session.failStart(ctx, fmt.Sprintf("start exec failed: %v", err))
		return
	}

	// Resize terminal to requested dimensions.
	if msg.Cols > 0 && msg.Rows > 0 {
		if err := c.dockerClient.ResizeExec(ctx, execID, msg.Cols, msg.Rows); err != nil {
			slog.Warn("initial resize failed", "execID", applog.Sanitize(execID), "error", applog.Sanitize(err.Error()))
		}
	}

	// Wire the connection. If the session was already torn down while we were
	// bringing the exec up, activate closes the orphaned conn and we stop here.
	if !session.activate(conn) {
		session.auditStartError()
		return
	}

	// Announce readiness; best-effort — connection loss surfaces on the read pump.
	_ = c.sendTypedMessageTo(session.target, protocol.TypeExecReady, protocol.ExecReadyMessage{
		ExecID: msg.ExecID,
	})

	// Start reading output from the exec session.
	go session.readLoop()
}

// HandleInput decodes input and enqueues it for ordered delivery. The enqueue
// is non-blocking: the read pump must keep servicing pings and other sessions,
// so a full queue drops the input with a warning rather than stalling.
func (c *Client) HandleInput(msg protocol.ExecInputMessage) {
	val, ok := c.execSessions.Load(msg.ExecID)
	if !ok {
		slog.Debug("exec session not found for input", "execID", applog.Sanitize(msg.ExecID))
		return
	}

	session := val.(*ExecSession)

	decodedLen := base64.StdEncoding.DecodedLen(len(msg.Data))
	if strings.HasSuffix(msg.Data, "=") {
		decodedLen--
		if strings.HasSuffix(msg.Data, "==") {
			decodedLen--
		}
	}
	if decodedLen > execInputFrameLimit {
		slog.Warn("exec input frame too large", "execID", applog.Sanitize(msg.ExecID), "bytes", decodedLen, "max", execInputFrameLimit)
		return
	}

	data, err := base64.StdEncoding.DecodeString(msg.Data)
	if err != nil {
		slog.Warn("failed to decode exec input", "execID", applog.Sanitize(msg.ExecID), "error", applog.Sanitize(err.Error()))
		return
	}

	switch session.enqueueInput(data) {
	case inputEnqueued:
	case inputSessionClosed:
		slog.Debug("exec input for closed session", "execID", applog.Sanitize(msg.ExecID))
	case inputBudgetExceeded:
		slog.Warn("exec input byte budget exceeded, dropping", "execID", applog.Sanitize(msg.ExecID), "max", execInputQueuedLimit)
	case inputQueueFull:
		slog.Warn("exec input queue full, dropping", "execID", applog.Sanitize(msg.ExecID))
	}
}

type inputEnqueueResult uint8

const (
	inputEnqueued inputEnqueueResult = iota
	inputSessionClosed
	inputBudgetExceeded
	inputQueueFull
)

// enqueueInput atomically reserves decoded bytes and enqueues the frame. The
// reservation stays live while the frame is queued or being written.
func (s *ExecSession) enqueueInput(data []byte) inputEnqueueResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return inputSessionClosed
	}
	if len(data) > execInputQueuedLimit-s.queuedInputBytes {
		return inputBudgetExceeded
	}

	s.queuedInputBytes += len(data)
	select {
	case s.inbox <- execItem{data: data, reservedBytes: len(data)}:
		return inputEnqueued
	default:
		s.queuedInputBytes -= len(data)
		return inputQueueFull
	}
}

func (s *ExecSession) releaseInputBytes(n int) {
	if n == 0 {
		return
	}
	s.mu.Lock()
	s.queuedInputBytes -= n
	s.mu.Unlock()
}

// inputWriter is the session's single input writer. It waits for the exec to go
// live, then drains inbox in order, writing each chunk to the connection. Being
// the only writer is what guarantees input ordering.
func (s *ExecSession) inputWriter(ctx context.Context) {
	defer recoverSession("inputWriter", s.execID)

	select {
	case <-s.connReady:
	case <-s.done:
		return
	}

	for {
		select {
		case item := <-s.inbox:
			if item.resize != nil {
				s.doResize(ctx, *item.resize)
			} else {
				s.writeInputItem(item)
			}
		case <-s.done:
			return
		}
	}
}

func (s *ExecSession) writeInputItem(item execItem) {
	defer s.releaseInputBytes(item.reservedBytes)
	s.writeInput(item.data)
}

// writeInput writes one chunk to the exec connection, retrying transient
// failures (up to 10 attempts, 50ms apart). A session that can't be written to
// is closed.
func (s *ExecSession) writeInput(data []byte) {
	for attempt := 0; attempt < 10; attempt++ {
		if _, err := s.conn.Write(data); err == nil {
			return
		} else {
			slog.Debug("exec write retry", "execID", applog.Sanitize(s.execID), "attempt", attempt+1, "error", applog.Sanitize(err.Error()))
		}
		select {
		case <-s.done:
			return
		case <-time.After(50 * time.Millisecond):
		}
	}

	slog.Warn("failed to write exec input after retries", "execID", applog.Sanitize(s.execID))
	s.Close()
}

// HandleResize enqueues a TTY resize for ordered delivery. Like HandleInput the
// enqueue is non-blocking, so the read pump keeps servicing pings and other
// sessions: the actual ResizeExec round-trip (and its retries) runs on the
// session's single inputWriter goroutine, never on the read pump. The ctx param
// is unused — the drainer carries the session's ctx from StartExec.
func (c *Client) HandleResize(_ context.Context, msg protocol.ExecResizeMessage) {
	val, ok := c.execSessions.Load(msg.ExecID)
	if !ok {
		slog.Debug("exec session not found for resize", "execID", applog.Sanitize(msg.ExecID))
		return
	}

	session := val.(*ExecSession)

	select {
	case session.inbox <- execItem{resize: &resizeOp{cols: msg.Cols, rows: msg.Rows}}:
	case <-session.done:
		slog.Debug("exec resize for closed session", "execID", applog.Sanitize(msg.ExecID))
	default:
		slog.Warn("exec resize queue full, dropping", "execID", applog.Sanitize(msg.ExecID))
	}
}

// doResize performs the Docker resize round-trip on the inputWriter goroutine.
// It targets dockerExecID (the Docker-assigned instance ID, not the controller
// execID), retrying transient failures while respecting session/connection
// teardown so a failing resize can't pin the drainer indefinitely.
func (s *ExecSession) doResize(ctx context.Context, op resizeOp) {
	for attempt := 0; attempt < 10; attempt++ {
		if err := s.client.dockerClient.ResizeExec(ctx, s.dockerExecID, op.cols, op.rows); err == nil {
			return
		} else {
			slog.Debug("exec resize retry", "execID", applog.Sanitize(s.execID), "attempt", attempt+1, "error", applog.Sanitize(err.Error()))
		}
		select {
		case <-s.done:
			return
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
	slog.Warn("failed to resize exec after retries", "execID", applog.Sanitize(s.execID))
}

// EndExec closes an active exec session.
func (c *Client) EndExec(msg protocol.ExecEndMessage) {
	val, ok := c.execSessions.Load(msg.ExecID)
	if !ok {
		slog.Debug("exec session not found for end", "execID", applog.Sanitize(msg.ExecID))
		return
	}

	session := val.(*ExecSession)
	session.Close()
}

// activate wires the live connection and unblocks inputWriter. It returns false
// if the session was already closed during bring-up, in which case the caller
// must not start the read loop and activate has closed the orphaned conn.
func (s *ExecSession) activate(conn net.Conn) bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		if err := conn.Close(); err != nil {
			slog.Debug("closing orphaned exec conn", "exec_id", applog.Sanitize(s.execID), "error", applog.Sanitize(err.Error()))
		}
		return false
	}
	s.conn = conn
	s.mu.Unlock()

	close(s.connReady)
	return true
}

func (s *ExecSession) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// auditStartError records that an admitted typed exec did not come up, as the
// api_request error record under the exec_start message type. Each bringUpExec
// exit that gives up calls it once.
func (s *ExecSession) auditStartError() {
	s.client.auditor.APIRequest(s.client.cfg.DrydockURL, protocol.TypeExecStart, s.containerID, audit.OutcomeError, 0, 0)
}

// abortStart records the failed start and tears the session down for a
// bring-up that was cancelled or whose session was closed before it came up.
// No exec_end is sent: whoever closed the session already owns that.
func (s *ExecSession) abortStart() {
	s.auditStartError()
	s.Close()
}

// failStart tears the session down and reports a terminal exec_end. It closes
// first so the session is deregistered before the controller sees the failure.
// A bring-up whose context has ended or whose session is already closed gets
// the record and no exec_end, as in abortStart: the controller's exec_end or a
// tunnel drop landing mid round trip fails the round trip too, and whoever
// ended the session owns the exec_end. The context is checked as well as the
// closed flag because Close cancels it before it sets the flag.
func (s *ExecSession) failStart(ctx context.Context, reason string) {
	if ctx.Err() != nil || s.isClosed() {
		s.abortStart()
		return
	}
	s.auditStartError()
	s.Close()
	// Best-effort error reply; connection loss will surface on the read pump.
	_ = s.client.sendTypedMessageTo(s.target, protocol.TypeExecEnd, protocol.ExecEndMessage{
		ExecID: s.execID,
		Reason: reason,
	})
}

// execFrameHeaderLen is the size of Docker's stream-multiplexing frame header
// on a non-TTY exec: a stream-type byte, three zero bytes, then the payload
// length as a 4-byte big-endian integer.
const execFrameHeaderLen = 8

// execStreamSystemErr is the highest stream type Docker emits. 0, 1 and 2 are
// stdin, stdout and stderr; 3 carries a daemon-side error.
const execStreamSystemErr = 3

// execMaxFrameBytes bounds the payload length a single frame header may claim.
// The daemon copies an attached exec through a 32 KiB buffer, so a real frame
// never approaches this; a header claiming more is corrupt or hostile, and
// honouring it would make the demuxer swallow up to 4 GiB of the stream as one
// frame's payload. It matches the cap the container-log decoders already use
// (internal/docker's maxLogFrameSize).
const execMaxFrameBytes = 256 << 10 // 256 KiB

// execDemuxer strips those headers off a non-TTY exec stream. It keeps the
// header bytes seen so far and the payload bytes still outstanding across
// calls because the hijacked connection is read into a 4 KiB pooled buffer
// while Docker writes frames of up to 32 KiB: a header can straddle two reads
// and a payload routinely spans several.
type execDemuxer struct {
	header    [execFrameHeaderLen]byte
	headerLen int
	// remaining is an int, not the uint32 the header carries, because it is
	// only ever compared and decremented against slice lengths. The width
	// conversion happens once, after the header's length has been bounded by
	// execMaxFrameBytes, instead of on every payload chunk.
	remaining int
}

// decode compacts chunk in place and returns the prefix holding only payload
// bytes. Overwriting the input is safe because a payload byte's destination
// index is never past its source index — every header consumed only widens the
// gap — and copy is defined for overlapping slices. stdout and stderr payloads
// are merged in arrival order: exec_output carries no stream identifier, so the
// controller receives them the way a terminal would.
func (d *execDemuxer) decode(chunk []byte) ([]byte, error) {
	w := 0
	for r := 0; r < len(chunk); {
		if d.remaining == 0 {
			n := copy(d.header[d.headerLen:], chunk[r:])
			d.headerLen += n
			r += n
			if d.headerLen < execFrameHeaderLen {
				break
			}
			if d.header[0] > execStreamSystemErr || d.header[1] != 0 || d.header[2] != 0 || d.header[3] != 0 {
				return chunk[:w], fmt.Errorf("exec stream desynchronized: bad frame header %x", d.header[:4])
			}
			size := binary.BigEndian.Uint32(d.header[4:])
			if size > execMaxFrameBytes {
				return chunk[:w], fmt.Errorf("exec stream desynchronized: frame length %d exceeds %d bytes", size, execMaxFrameBytes)
			}
			// Narrowed only after the bound above, so the conversion cannot
			// overflow int on any platform Go builds for.
			d.remaining = int(size)
			d.headerLen = 0
			continue
		}

		n := len(chunk) - r
		if n > d.remaining {
			n = d.remaining
		}
		w += copy(chunk[w:w+n], chunk[r:r+n])
		d.remaining -= n
		r += n
	}
	return chunk[:w], nil
}

// readLoop reads output from the exec session's connection and sends it back
// as exec_output messages. On error or EOF, it sends exec_end and cleans up.
func (s *ExecSession) readLoop() {
	defer s.Close()
	defer recoverSession("readLoop", s.execID)

	// Docker only writes a raw stream when the exec has a PTY. Without one it
	// multiplexes stdout and stderr behind 8-byte frame headers, which used to
	// be forwarded verbatim and rendered as garbage in the middle of the
	// command's own output.
	var demux *execDemuxer
	if !s.tty {
		demux = &execDemuxer{}
	}

	for {
		buf := pool.GetBuffer()

		n, err := s.conn.Read(buf)
		out := buf[:n]
		if demux != nil && n > 0 {
			decoded, decodeErr := demux.decode(out)
			out = decoded
			if decodeErr != nil {
				// A desynchronized stream can't be trusted past this point, so
				// the decode failure replaces any read error: reporting the
				// io.EOF that came with it would end the session as a clean
				// "exited".
				err = decodeErr
			}
		}
		if len(out) > 0 {
			encoded := base64.StdEncoding.EncodeToString(out)

			data, marshalErr := json.Marshal(protocol.ExecOutputMessage{
				ExecID: s.execID,
				Data:   encoded,
			})
			if marshalErr == nil {
				s.client.sendMessageTo(s.target, protocol.Envelope{
					Type: protocol.TypeExecOutput,
					Data: json.RawMessage(data),
				})
			}
		}

		pool.PutBuffer(buf)

		if err != nil {
			slog.Debug("exec read ended", "execID", applog.Sanitize(s.execID), "error", applog.Sanitize(err.Error()))

			// Send exec_end.
			reason := "exited"
			if !errors.Is(err, io.EOF) {
				reason = err.Error()
			}

			endData, marshalErr := json.Marshal(protocol.ExecEndMessage{
				ExecID: s.execID,
				Reason: reason,
			})
			if marshalErr == nil {
				s.client.sendMessageTo(s.target, protocol.Envelope{
					Type: protocol.TypeExecEnd,
					Data: json.RawMessage(endData),
				})
			}
			return
		}
	}
}

// Close shuts down the exec session. It is safe to call multiple times and
// safe to race against bring-up: it records the closed state under mu and
// closes whatever connection is currently wired (none, if the exec never went
// live).
func (s *ExecSession) Close() {
	s.once.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		s.mu.Lock()
		s.closed = true
		conn := s.conn
		// Unregister before done is observable. A waiter that sees done closed
		// and then checks the registry (the write-failure teardown test does
		// exactly that) must not find this session, and unregistering only
		// after the conn close and inbox drain below left a window where it
		// did. Once closed is set HandleInput can no longer enqueue. A racing
		// HandleResize may still land a zero-reservation item in the inbox and
		// inputWriter may pick it over done; doResize then runs against the
		// already-cancelled session context and aborts, so nothing leaks. The
		// maxExecSessions slot and the exec ID are released at closed=true
		// rather than at the end of the drain: a dead session should not hold
		// a slot while its conn finishes closing, at the cost of a slightly
		// wider window in which a controller reusing the same exec ID could
		// see the old readLoop's last frames.
		s.client.execSessions.CompareAndDelete(s.execID, s)
		close(s.done)
		s.mu.Unlock()

		if conn != nil {
			if err := conn.Close(); err != nil {
				slog.Debug("closing exec session", "exec_id", applog.Sanitize(s.execID), "error", applog.Sanitize(err.Error()))
			}
		}
		for {
			select {
			case item := <-s.inbox:
				s.releaseInputBytes(item.reservedBytes)
			default:
				return
			}
		}
	})
}

// recoverSession swallows and logs a panic in a per-session goroutine so one
// bad exec stream can't take down the whole agent process. Deferred at the
// entry of each per-session goroutine (bringUpExec, inputWriter, readLoop).
func recoverSession(where, execID string) {
	if r := recover(); r != nil {
		slog.Error("recovered from panic in exec session goroutine",
			"where", where, "execID", applog.Sanitize(execID), "panic", applog.Sanitize(fmt.Sprint(r)))
	}
}

// execSlotsFullLocked reports whether typed exec sessions plus raw exec starts
// have used every maxExecSessions slot. The caller holds execAdmissionMu.
func (c *Client) execSlotsFullLocked() bool {
	count := c.rawExecStarts
	c.execSessions.Range(func(_, _ any) bool {
		count++
		return count < maxExecSessions
	})
	return count >= maxExecSessions
}

// acquireRawExecSlot takes a slot for a raw exec start, or reports false when
// the cap shared with typed exec sessions is full.
func (c *Client) acquireRawExecSlot() bool {
	c.execAdmissionMu.Lock()
	defer c.execAdmissionMu.Unlock()
	if c.execSlotsFullLocked() {
		return false
	}
	c.rawExecStarts++
	return true
}

// releaseRawExecSlot returns a slot taken by acquireRawExecSlot.
func (c *Client) releaseRawExecSlot() {
	c.execAdmissionMu.Lock()
	c.rawExecStarts--
	c.execAdmissionMu.Unlock()
}

// rawExecStart reports whether a request message is an exec start sent as
// plain HTTP rather than as a typed exec_start. It returns the exec ID and the
// decoded path, both parsed the way the standalone proxy does. The path may
// carry a query and is classified percent-decoded, as the daemon routes it.
func rawExecStart(method, path string) (execID, routePath string, ok bool) {
	if method != http.MethodPost {
		return "", "", false
	}
	pathOnly, _, _ := strings.Cut(path, "?")
	if decoded, err := url.PathUnescape(pathOnly); err == nil {
		pathOnly = decoded
	}
	if !docker.IsResourceRoute(pathOnly, "exec", "start") {
		return "", "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(docker.StripAPIVersion(pathOnly), "/exec/"), "/start")
	return id, pathOnly, true
}

// admitRawExecStart takes a slot for a raw exec start and writes its exec_start
// record with the admission result, before anything is forwarded to the daemon.
// isExec is false for any other request, which needs no slot and is always
// admitted. The caller releases the slot with releaseRawExecSlot when isExec and
// admitted are both true.
func (c *Client) admitRawExecStart(method, path string) (isExec, admitted bool) {
	id, routePath, ok := rawExecStart(method, path)
	if !ok {
		return false, true
	}
	admitted = c.acquireRawExecSlot()
	c.auditor.ExecStart(c.cfg.DrydockURL, routePath, id, audit.ExecOutcome(admitted))
	return true, admitted
}

// auditRefusedRawExecStart records, as denied, a raw exec start a cap turned
// away before it reached handleRequestTo, which would otherwise record it. It
// applies the path gate handleRequestTo applies, so a path the handler rejects
// as invalid is not recorded as an exec start. A request admitted past the caps
// is recorded by handleRequestTo instead, so none is recorded twice.
func (c *Client) auditRefusedRawExecStart(method, path string) {
	if docker.ValidateAPIPath(path) != nil {
		return
	}
	id, routePath, ok := rawExecStart(method, path)
	if ok {
		c.auditor.ExecStart(c.cfg.DrydockURL, routePath, id, audit.OutcomeDenied)
	}
}
