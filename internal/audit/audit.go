// Package audit provides structured JSON audit logging for security-relevant
// events. It is intentionally separate from the operational log stream so audit
// records can be routed to a dedicated sink (file, stdout, stderr) without
// mingling with debug/info noise.
//
// # Event schema (stable)
//
// Every audit record is a JSON object on a single line with at minimum:
//
//	{
//	  "ts":          "2006-01-02T15:04:05.999Z07:00",  // RFC 3339 nanosecond
//	  "event":       "<event_type>",                   // see Event* constants
//	  "actor":       "<client IP or edge peer URL>",
//	  "method":      "<HTTP method or message type>",
//	  "path":        "<HTTP path or message path>",
//	  "outcome":     "allowed | denied | error",
//	  "status":      123,                              // HTTP status where applicable (omitted if 0)
//	  "duration_ms": 1.23                              // optional, where measured
//	}
//
// Compose events add:
//
//	"operation": "<up|down|pull|...>",
//	"stack":     "<stack name>"
//
// Exec tunnel events add:
//
//	"container": "<exec path or container ID>",
//	"exec_id":   "<exec resource ID>"
//
// # Configuration
//
// Set AUDIT_LOG to one of:
//
//	""        – auditing disabled (default); zero overhead beyond a nil check
//	"stdout"  – write to os.Stdout
//	"stderr"  – write to os.Stderr
//	"<path>"  – write to a file; opened append-only, mode 0600
//
// Set AUDIT_BUFFER_SIZE to the number of recent audit records to keep in
// memory for pull-based export via GET /_portwing/audit. Default 256; 0
// disables the buffer. The buffer is independent of AUDIT_LOG — it works even
// when slog output is disabled.
package audit

import (
	"io"
	"log/slog"
	"os"
	"time"
)

// Event type constants — stable identifiers used in the "event" field.
const (
	EventAPIRequest  = "api_request"  // any authenticated HTTP request
	EventExecStart   = "exec_start"   // exec tunnel started
	EventComposeOp   = "compose_op"   // Docker Compose operation
	EventAuthFailure = "auth_failure" // invalid token presented
	EventRateLimited = "rate_limited" // request blocked by rate limiter
	EventEnrollment  = "enrollment"   // Ed25519 key enrolled via /api/portwing/enroll
)

// Outcome values for the "outcome" field.
const (
	OutcomeAllowed = "allowed"
	OutcomeDenied  = "denied"
	OutcomeError   = "error"
)

// Logger is a structured audit logger. The zero value is valid and emits
// nothing (auditing disabled). All methods are safe for concurrent use.
type Logger struct {
	log    *slog.Logger // nil when slog output is disabled
	ring   *ring        // nil when the in-memory buffer is disabled
	closer func()       // flushes/closes the underlying file; no-op for non-file sinks
}

// New creates a Logger that writes to the sink indicated by dest and keeps an
// in-memory ring buffer of the most recent bufferSize events.
// dest == "" disables slog output. bufferSize <= 0 disables the buffer.
// The two are independent: a buffer works even when dest is "".
// The returned closer is retained internally; callers should use Logger.Close()
// on shutdown instead.
func New(dest string, bufferSize int) (*Logger, func(), error) {
	l := &Logger{closer: func() {}}
	if bufferSize > 0 {
		l.ring = newRing(bufferSize)
	}
	if dest == "" {
		return l, func() {}, nil
	}

	var w io.Writer

	switch dest {
	case "stdout":
		w = os.Stdout
	case "stderr":
		w = os.Stderr
	default:
		// #nosec G304 -- AUDIT_LOG is an explicit operator-configured destination.
		f, err := os.OpenFile(dest, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, func() {}, err
		}
		w = f
		l.closer = func() { _ = f.Close() }
	}

	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		// Include time as the first attribute so audit lines sort naturally.
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				a.Value = slog.StringValue(a.Value.Time().UTC().Format(time.RFC3339Nano))
			}
			return a
		},
	})

	l.log = slog.New(h)
	return l, l.closer, nil
}

// Close flushes and closes the underlying file sink. It is a no-op for
// stdout, stderr, and disabled loggers, and is safe to call more than once.
func (l *Logger) Close() {
	if l.closer != nil {
		l.closer()
	}
}

// Enabled reports whether slog output is active.
func (l *Logger) Enabled() bool { return l.log != nil }

// Records returns the most recent audit records from the in-memory buffer,
// newest-first, capped at limit. limit <= 0 returns all buffered records.
// Returns an empty slice when the buffer is disabled or empty.
func (l *Logger) Records(limit int) []Record {
	if l.ring == nil {
		return []Record{}
	}
	return l.ring.records(limit)
}

// APIRequest records an authenticated (or rejected) HTTP API call.
func (l *Logger) APIRequest(actor, method, path, outcome string, status int, durationMs float64) {
	if l.log == nil && l.ring == nil {
		return
	}
	if l.log != nil {
		l.log.Info("",
			slog.String("event", EventAPIRequest),
			slog.String("actor", actor),
			slog.String("method", method),
			slog.String("path", path),
			slog.String("outcome", outcome),
			slog.Int("status", status),
			slog.Float64("duration_ms", durationMs),
		)
	}
	if l.ring != nil {
		l.ring.push(Record{
			Time:       time.Now().UTC(),
			Event:      EventAPIRequest,
			Actor:      actor,
			Method:     method,
			Path:       path,
			Outcome:    outcome,
			Status:     status,
			DurationMs: durationMs,
		})
	}
}

// AuthFailure records a failed authentication attempt.
func (l *Logger) AuthFailure(actor, method, path string) {
	if l.log == nil && l.ring == nil {
		return
	}
	if l.log != nil {
		l.log.Info("",
			slog.String("event", EventAuthFailure),
			slog.String("actor", actor),
			slog.String("method", method),
			slog.String("path", path),
			slog.String("outcome", OutcomeDenied),
		)
	}
	if l.ring != nil {
		l.ring.push(Record{
			Time:    time.Now().UTC(),
			Event:   EventAuthFailure,
			Actor:   actor,
			Method:  method,
			Path:    path,
			Outcome: OutcomeDenied,
		})
	}
}

// RateLimited records a request blocked by the rate limiter.
func (l *Logger) RateLimited(actor, method, path string) {
	if l.log == nil && l.ring == nil {
		return
	}
	if l.log != nil {
		l.log.Info("",
			slog.String("event", EventRateLimited),
			slog.String("actor", actor),
			slog.String("method", method),
			slog.String("path", path),
			slog.String("outcome", OutcomeDenied),
		)
	}
	if l.ring != nil {
		l.ring.push(Record{
			Time:    time.Now().UTC(),
			Event:   EventRateLimited,
			Actor:   actor,
			Method:  method,
			Path:    path,
			Outcome: OutcomeDenied,
		})
	}
}

// ComposeOp records a Docker Compose lifecycle operation.
func (l *Logger) ComposeOp(actor, operation, stack, outcome string) {
	if l.log == nil && l.ring == nil {
		return
	}
	if l.log != nil {
		l.log.Info("",
			slog.String("event", EventComposeOp),
			slog.String("actor", actor),
			slog.String("operation", operation),
			slog.String("stack", stack),
			slog.String("outcome", outcome),
		)
	}
	if l.ring != nil {
		l.ring.push(Record{
			Time:      time.Now().UTC(),
			Event:     EventComposeOp,
			Actor:     actor,
			Operation: operation,
			Stack:     stack,
			Outcome:   outcome,
		})
	}
}

// Enrollment records an Ed25519 key enrollment event.
// outcome is OutcomeAllowed on success, OutcomeDenied on failure.
func (l *Logger) Enrollment(actor, keyID, outcome string) {
	if l.log == nil && l.ring == nil {
		return
	}
	if l.log != nil {
		l.log.Info("",
			slog.String("event", EventEnrollment),
			slog.String("actor", actor),
			slog.String("key_id", keyID),
			slog.String("outcome", outcome),
		)
	}
	if l.ring != nil {
		l.ring.push(Record{
			Time:    time.Now().UTC(),
			Event:   EventEnrollment,
			Actor:   actor,
			KeyID:   keyID,
			Outcome: outcome,
		})
	}
}

// ExecStart records the start of an interactive exec tunnel.
func (l *Logger) ExecStart(actor, container, execID string) {
	if l.log == nil && l.ring == nil {
		return
	}
	if l.log != nil {
		l.log.Info("",
			slog.String("event", EventExecStart),
			slog.String("actor", actor),
			slog.String("container", container),
			slog.String("exec_id", execID),
			slog.String("outcome", OutcomeAllowed),
		)
	}
	if l.ring != nil {
		l.ring.push(Record{
			Time:      time.Now().UTC(),
			Event:     EventExecStart,
			Actor:     actor,
			Container: container,
			ExecID:    execID,
			Outcome:   OutcomeAllowed,
		})
	}
}
