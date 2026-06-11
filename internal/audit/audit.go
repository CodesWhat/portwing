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
	EventEnrollment  = "enrollment"   // Ed25519 key enrolled via /api/lookout/enroll
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
	log *slog.Logger // nil when auditing is disabled
}

// New creates a Logger that writes to the sink indicated by dest.
// dest == "" disables auditing. The returned closer should be called on
// shutdown; it is a no-op for stdout/stderr sinks.
func New(dest string) (*Logger, func(), error) {
	if dest == "" {
		return &Logger{}, func() {}, nil
	}

	var w io.Writer
	closer := func() {}

	switch dest {
	case "stdout":
		w = os.Stdout
	case "stderr":
		w = os.Stderr
	default:
		f, err := os.OpenFile(dest, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, func() {}, err
		}
		w = f
		closer = func() { _ = f.Close() }
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

	return &Logger{log: slog.New(h)}, closer, nil
}

// Enabled reports whether auditing is active.
func (l *Logger) Enabled() bool { return l.log != nil }

// APIRequest records an authenticated (or rejected) HTTP API call.
func (l *Logger) APIRequest(actor, method, path, outcome string, status int, durationMs float64) {
	if l.log == nil {
		return
	}
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

// AuthFailure records a failed authentication attempt.
func (l *Logger) AuthFailure(actor, method, path string) {
	if l.log == nil {
		return
	}
	l.log.Info("",
		slog.String("event", EventAuthFailure),
		slog.String("actor", actor),
		slog.String("method", method),
		slog.String("path", path),
		slog.String("outcome", OutcomeDenied),
	)
}

// RateLimited records a request blocked by the rate limiter.
func (l *Logger) RateLimited(actor, method, path string) {
	if l.log == nil {
		return
	}
	l.log.Info("",
		slog.String("event", EventRateLimited),
		slog.String("actor", actor),
		slog.String("method", method),
		slog.String("path", path),
		slog.String("outcome", OutcomeDenied),
	)
}

// ComposeOp records a Docker Compose lifecycle operation.
func (l *Logger) ComposeOp(actor, operation, stack, outcome string) {
	if l.log == nil {
		return
	}
	l.log.Info("",
		slog.String("event", EventComposeOp),
		slog.String("actor", actor),
		slog.String("operation", operation),
		slog.String("stack", stack),
		slog.String("outcome", outcome),
	)
}

// Enrollment records an Ed25519 key enrollment event.
// outcome is OutcomeAllowed on success, OutcomeDenied on failure.
func (l *Logger) Enrollment(actor, keyID, outcome string) {
	if l.log == nil {
		return
	}
	l.log.Info("",
		slog.String("event", EventEnrollment),
		slog.String("actor", actor),
		slog.String("key_id", keyID),
		slog.String("outcome", outcome),
	)
}

// ExecStart records the start of an interactive exec tunnel.
func (l *Logger) ExecStart(actor, container, execID string) {
	if l.log == nil {
		return
	}
	l.log.Info("",
		slog.String("event", EventExecStart),
		slog.String("actor", actor),
		slog.String("container", container),
		slog.String("exec_id", execID),
		slog.String("outcome", OutcomeAllowed),
	)
}
