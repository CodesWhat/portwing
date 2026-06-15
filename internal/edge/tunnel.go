package edge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/codeswhat/portwing/internal/pool"
	"github.com/codeswhat/portwing/internal/protocol"
)

// ExecSession represents an active exec session tunneled over WebSocket.
type ExecSession struct {
	execID      string
	containerID string
	conn        net.Conn
	client      *Client
	done        chan struct{}
	once        sync.Once
}

// StartExec creates and starts a Docker exec session, then begins streaming
// output back over the WebSocket.
func (c *Client) StartExec(ctx context.Context, msg protocol.ExecStartMessage) {
	// Check concurrent session limit.
	var count int
	c.execSessions.Range(func(_, _ interface{}) bool {
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

	// Create exec instance.
	execID, err := c.dockerClient.CreateExec(ctx, msg.ContainerID, msg.Cmd, msg.User, true)
	if err != nil {
		slog.Error("failed to create exec", "container", msg.ContainerID, "error", err)
		// Best-effort error reply; connection loss will surface on the read pump.
		_ = c.sendTypedMessage(protocol.TypeExecEnd, protocol.ExecEndMessage{
			ExecID: msg.ExecID,
			Reason: fmt.Sprintf("create exec failed: %v", err),
		})
		return
	}

	// Start exec and get hijacked connection.
	conn, err := c.dockerClient.StartExec(ctx, execID, true)
	if err != nil {
		slog.Error("failed to start exec", "execID", execID, "error", err)
		// Best-effort error reply; connection loss will surface on the read pump.
		_ = c.sendTypedMessage(protocol.TypeExecEnd, protocol.ExecEndMessage{
			ExecID: msg.ExecID,
			Reason: fmt.Sprintf("start exec failed: %v", err),
		})
		return
	}

	// Resize terminal to requested dimensions.
	if msg.Cols > 0 && msg.Rows > 0 {
		if err := c.dockerClient.ResizeExec(ctx, execID, msg.Cols, msg.Rows); err != nil {
			slog.Warn("initial resize failed", "execID", execID, "error", err)
		}
	}

	session := &ExecSession{
		execID:      msg.ExecID,
		containerID: msg.ContainerID,
		conn:        conn,
		client:      c,
		done:        make(chan struct{}),
	}

	c.execSessions.Store(msg.ExecID, session)

	// Send exec_ready; best-effort — connection loss will surface on the read pump.
	_ = c.sendTypedMessage(protocol.TypeExecReady, protocol.ExecReadyMessage{
		ExecID: msg.ExecID,
	})

	// Start reading output from the exec session.
	go session.readLoop()
}

// HandleInput writes decoded input data to an active exec session.
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

	// Write with retry (up to 10 attempts, 50ms intervals).
	for attempt := 0; attempt < 10; attempt++ {
		_, err := session.conn.Write(data)
		if err == nil {
			return
		}
		slog.Debug("exec write retry", "execID", msg.ExecID, "attempt", attempt+1, "error", err)
		time.Sleep(50 * time.Millisecond)
	}

	slog.Warn("failed to write exec input after retries", "execID", msg.ExecID)
	session.Close()
}

// HandleResize changes the TTY dimensions for an active exec session.
func (c *Client) HandleResize(ctx context.Context, msg protocol.ExecResizeMessage) {
	val, ok := c.execSessions.Load(msg.ExecID)
	if !ok {
		slog.Debug("exec session not found for resize", "execID", msg.ExecID)
		return
	}

	session := val.(*ExecSession)
	_ = session // verify session exists

	// Resize with retry (up to 10 attempts, 50ms intervals).
	for attempt := 0; attempt < 10; attempt++ {
		err := c.dockerClient.ResizeExec(ctx, msg.ExecID, msg.Cols, msg.Rows)
		if err == nil {
			return
		}
		slog.Debug("exec resize retry", "execID", msg.ExecID, "attempt", attempt+1, "error", err)
		time.Sleep(50 * time.Millisecond)
	}

	slog.Warn("failed to resize exec after retries", "execID", msg.ExecID)
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

// readLoop reads output from the exec session's connection and sends it back
// as exec_output messages. On error or EOF, it sends exec_end and cleans up.
func (s *ExecSession) readLoop() {
	defer s.Close()

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
			if err.Error() != "EOF" {
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

// Close shuts down the exec session. It is safe to call multiple times.
func (s *ExecSession) Close() {
	s.once.Do(func() {
		if err := s.conn.Close(); err != nil {
			slog.Debug("closing exec session", "exec_id", s.execID, "error", err)
		}
		close(s.done)
		s.client.execSessions.Delete(s.execID)
	})
}
