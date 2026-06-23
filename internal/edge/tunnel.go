package edge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/codeswhat/portwing/internal/pool"
	"github.com/codeswhat/portwing/internal/protocol"
)

// execInputQueue bounds the per-session input backlog. Input and resizes are
// decoded on the read loop and handed to a single writer goroutine, so this
// buffers the burst that can arrive before the Docker exec is live (and any
// momentary write stall) without ever blocking the read pump.
const execInputQueue = 256

// execItem is one ordered unit drained by inputWriter: either stdin bytes to
// write to the exec, or a TTY resize. Routing both through the single drainer
// keeps them in arrival order and — critically — off the read pump, so a slow
// or failing resize can never stall ping/exec dispatch.
type execItem struct {
	data   []byte    // stdin bytes; nil for a resize
	resize *resizeOp // non-nil for a resize
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

	done chan struct{}
	once sync.Once

	mu     sync.Mutex
	closed bool
}

// StartExec registers the exec session synchronously, then brings the Docker
// exec up asynchronously. Registering up front is what makes input ordered:
// exec_input that arrives immediately after exec_start finds the session and is
// queued, instead of racing the bring-up and being dropped.
func (c *Client) StartExec(ctx context.Context, msg protocol.ExecStartMessage) {
	// Check concurrent session limit.
	var count int
	c.execSessions.Range(func(_, _ any) bool {
		count++
		return count < maxExecSessions
	})
	if count >= maxExecSessions {
		slog.Warn("exec session limit reached", "max", maxExecSessions)
		// Best-effort error reply; connection loss will surface on the read pump.
		_ = c.sendTypedMessage(protocol.TypeExecEnd, protocol.ExecEndMessage{
			ExecID: msg.ExecID,
			Reason: "session limit reached",
		})
		return
	}

	session := &ExecSession{
		execID:      msg.ExecID,
		containerID: msg.ContainerID,
		client:      c,
		connReady:   make(chan struct{}),
		inbox:       make(chan execItem, execInputQueue),
		done:        make(chan struct{}),
	}
	c.execSessions.Store(msg.ExecID, session)

	go session.inputWriter(ctx)
	go c.bringUpExec(ctx, msg, session)
}

// bringUpExec performs the Docker round-trips for an already-registered session
// and, on success, wires the live connection and starts streaming.
func (c *Client) bringUpExec(ctx context.Context, msg protocol.ExecStartMessage, session *ExecSession) {
	defer recoverSession("bringUpExec", msg.ExecID)

	// Create exec instance.
	execID, err := c.dockerClient.CreateExec(ctx, msg.ContainerID, msg.Cmd, msg.User, true)
	if err != nil {
		slog.Error("failed to create exec", "container", msg.ContainerID, "error", err)
		session.failStart(fmt.Sprintf("create exec failed: %v", err))
		return
	}

	// Record the Docker exec ID so post-startup resizes target the instance
	// Docker actually knows about (not the controller's execID). Safe without a
	// lock: this write happens-before activate closes connReady, and the only
	// reader (inputWriter, via doResize) reads it only after <-connReady.
	session.dockerExecID = execID

	// Start exec and get hijacked connection.
	conn, err := c.dockerClient.StartExec(ctx, execID, true)
	if err != nil {
		slog.Error("failed to start exec", "execID", execID, "error", err)
		session.failStart(fmt.Sprintf("start exec failed: %v", err))
		return
	}

	// Resize terminal to requested dimensions.
	if msg.Cols > 0 && msg.Rows > 0 {
		if err := c.dockerClient.ResizeExec(ctx, execID, msg.Cols, msg.Rows); err != nil {
			slog.Warn("initial resize failed", "execID", execID, "error", err)
		}
	}

	// Wire the connection. If the session was already torn down while we were
	// bringing the exec up, activate closes the orphaned conn and we stop here.
	if !session.activate(conn) {
		return
	}

	// Announce readiness; best-effort — connection loss surfaces on the read pump.
	_ = c.sendTypedMessage(protocol.TypeExecReady, protocol.ExecReadyMessage{
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
		slog.Debug("exec session not found for input", "execID", msg.ExecID)
		return
	}

	session := val.(*ExecSession)

	data, err := base64.StdEncoding.DecodeString(msg.Data)
	if err != nil {
		slog.Warn("failed to decode exec input", "execID", msg.ExecID, "error", err)
		return
	}

	select {
	case session.inbox <- execItem{data: data}:
	case <-session.done:
		slog.Debug("exec input for closed session", "execID", msg.ExecID)
	default:
		slog.Warn("exec input queue full, dropping", "execID", msg.ExecID)
	}
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
				s.writeInput(item.data)
			}
		case <-s.done:
			return
		}
	}
}

// writeInput writes one chunk to the exec connection, retrying transient
// failures (up to 10 attempts, 50ms apart). A session that can't be written to
// is closed.
func (s *ExecSession) writeInput(data []byte) {
	for attempt := 0; attempt < 10; attempt++ {
		if _, err := s.conn.Write(data); err == nil {
			return
		} else {
			slog.Debug("exec write retry", "execID", s.execID, "attempt", attempt+1, "error", err)
		}
		select {
		case <-s.done:
			return
		case <-time.After(50 * time.Millisecond):
		}
	}

	slog.Warn("failed to write exec input after retries", "execID", s.execID)
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
		slog.Debug("exec session not found for resize", "execID", msg.ExecID)
		return
	}

	session := val.(*ExecSession)

	select {
	case session.inbox <- execItem{resize: &resizeOp{cols: msg.Cols, rows: msg.Rows}}:
	case <-session.done:
		slog.Debug("exec resize for closed session", "execID", msg.ExecID)
	default:
		slog.Warn("exec resize queue full, dropping", "execID", msg.ExecID)
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
			slog.Debug("exec resize retry", "execID", s.execID, "attempt", attempt+1, "error", err)
		}
		select {
		case <-s.done:
			return
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
	slog.Warn("failed to resize exec after retries", "execID", s.execID)
}

// EndExec closes an active exec session.
func (c *Client) EndExec(msg protocol.ExecEndMessage) {
	val, ok := c.execSessions.Load(msg.ExecID)
	if !ok {
		slog.Debug("exec session not found for end", "execID", msg.ExecID)
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
			slog.Debug("closing orphaned exec conn", "exec_id", s.execID, "error", err)
		}
		return false
	}
	s.conn = conn
	s.mu.Unlock()

	close(s.connReady)
	return true
}

// failStart tears the session down and reports a terminal exec_end. It closes
// first so the session is deregistered before the controller sees the failure.
func (s *ExecSession) failStart(reason string) {
	s.Close()
	// Best-effort error reply; connection loss will surface on the read pump.
	_ = s.client.sendTypedMessage(protocol.TypeExecEnd, protocol.ExecEndMessage{
		ExecID: s.execID,
		Reason: reason,
	})
}

// readLoop reads output from the exec session's connection and sends it back
// as exec_output messages. On error or EOF, it sends exec_end and cleans up.
func (s *ExecSession) readLoop() {
	defer s.Close()
	defer recoverSession("readLoop", s.execID)

	for {
		buf := pool.GetBuffer()

		n, err := s.conn.Read(buf)
		if n > 0 {
			encoded := base64.StdEncoding.EncodeToString(buf[:n])

			data, marshalErr := json.Marshal(protocol.ExecOutputMessage{
				ExecID: s.execID,
				Data:   encoded,
			})
			if marshalErr == nil {
				s.client.sendMessage(protocol.Envelope{
					Type: protocol.TypeExecOutput,
					Data: json.RawMessage(data),
				})
			}
		}

		pool.PutBuffer(buf)

		if err != nil {
			slog.Debug("exec read ended", "execID", s.execID, "error", err)

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
				s.client.sendMessage(protocol.Envelope{
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
		s.mu.Lock()
		s.closed = true
		conn := s.conn
		s.mu.Unlock()

		if conn != nil {
			if err := conn.Close(); err != nil {
				slog.Debug("closing exec session", "exec_id", s.execID, "error", err)
			}
		}
		close(s.done)
		s.client.execSessions.Delete(s.execID)
	})
}

// recoverSession swallows and logs a panic in a per-session goroutine so one
// bad exec stream can't take down the whole agent process. Deferred at the
// entry of each per-session goroutine (bringUpExec, inputWriter, readLoop).
func recoverSession(where, execID string) {
	if r := recover(); r != nil {
		slog.Error("recovered from panic in exec session goroutine",
			"where", where, "execID", execID, "panic", r)
	}
}
