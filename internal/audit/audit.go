// SPDX-License-Identifier: MIT

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
// An exec_start record is written for each attempt that reaches the admission
// decision, before anything is forwarded to the Docker daemon. Its outcome is
// "allowed" when the exec was admitted and "denied" when it was refused: by an
// exec session or request limit, or on the typed edge path by a missing or
// duplicate exec ID. A streamed-body start dropped while its body was still
// being reassembled never reaches the decision and writes no record.
//
// Enrollment events add:
//
//	"key_id": "<enrolled key ID on allowed results; empty on denied results>"
//
// api_request, compose_op and exec_start records add "key_id" when the caller
// authenticated with an Ed25519 key (the verified key on the Standard path, the
// session signing key on the Edge path); it is omitted for shared-token and
// unauthenticated callers.
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
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"time"

	applog "github.com/codeswhat/portwing/internal/log"
)

// Event type constants — stable identifiers used in the "event" field.
const (
	EventAPIRequest  = "api_request"  // any authenticated HTTP request
	EventExecStart   = "exec_start"   // exec tunnel started
	EventComposeOp   = "compose_op"   // Docker Compose operation
	EventAuthFailure = "auth_failure" // invalid token presented
	EventRateLimited = "rate_limited" // request blocked by rate limiter
	EventEnrollment  = "enrollment"   // Ed25519 enrollment result via /api/portwing/enroll
	EventKeyRotation = "key_rotation"
	// EventSignatureLock records a key being locked to signature version 3
	// ("lock") or an operator clearing that lock ("clear", "clear_all").
	EventSignatureLock = "signature_lock"
)

// Outcome values for the "outcome" field.
const (
	OutcomeAllowed = "allowed"
	OutcomeDenied  = "denied"
	OutcomeError   = "error"
	// OutcomeCancelled marks an operation whose request ended before it
	// finished, such as a compose deploy stopped by a client disconnect.
	OutcomeCancelled = "cancelled"
)

// Option adjusts an audit record before it is written.
type Option func(*options)

type options struct{ keyID string }

// WithKeyID attributes a record to the Ed25519 key that authenticated the
// caller. An empty id leaves the record unattributed.
func WithKeyID(id string) Option { return func(o *options) { o.keyID = id } }

// keyAttr appends the key_id attribute to attrs when keyID is set.
func keyAttr(keyID string, attrs []slog.Attr) []slog.Attr {
	if keyID != "" {
		attrs = append(attrs, slog.String("key_id", applog.Sanitize(keyID)))
	}
	return attrs
}

func keyIDOf(opts []Option) string {
	var o options
	for _, f := range opts {
		f(&o)
	}
	return o.keyID
}

// Logger is a structured audit logger. The zero value is valid and emits
// nothing (auditing disabled). All methods are safe for concurrent use.
type Logger struct {
	log    *slog.Logger // nil when slog output is disabled
	ring   *ring        // nil when the in-memory buffer is disabled
	closer func()       // flushes/closes the underlying file; no-op for non-file sinks
	epoch  string       // random per-process generation embedded in export cursors
}

// Stats describes the current audit sink and in-memory export buffer.
type Stats struct {
	Records      int
	Capacity     int
	SinkEnabled  bool
	OldestCursor uint64
	LatestCursor uint64
	// Epoch identifies this Logger's buffer generation. Export cursors carry it
	// so a cursor minted by a previous process is never read as one of ours.
	Epoch string
}

// New creates a Logger that writes to the sink indicated by dest and keeps an
// in-memory ring buffer of the most recent bufferSize events.
// dest == "" disables slog output. bufferSize <= 0 disables the buffer.
// The two are independent: a buffer works even when dest is "".
// The returned closer is retained internally; callers should use Logger.Close()
// on shutdown instead.
func New(dest string, bufferSize int) (*Logger, func(), error) {
	var gen [8]byte
	_, _ = rand.Read(gen[:]) // never returns an error (crypto/rand docs)
	l := &Logger{closer: func() {}, epoch: hex.EncodeToString(gen[:])}
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

// RecordsAfter returns retained records with a cursor greater than after in
// oldest-first export order, along with the current buffer window.
func (l *Logger) RecordsAfter(after uint64, limit int) ([]Record, Stats) {
	stats := l.Stats()
	if l.ring == nil {
		return []Record{}, stats
	}
	records, oldest, latest := l.ring.recordsAfter(after, limit)
	stats.OldestCursor = oldest
	stats.LatestCursor = latest
	return records, stats
}

// Stats returns current audit sink and buffer state.
func (l *Logger) Stats() Stats {
	stats := Stats{SinkEnabled: l.log != nil, Epoch: l.epoch}
	if l.ring == nil {
		return stats
	}
	stats.Records, stats.Capacity, stats.OldestCursor, stats.LatestCursor = l.ring.stats()
	return stats
}

// APIRequest records an authenticated (or rejected) HTTP API call.
func (l *Logger) APIRequest(actor, method, path, outcome string, status int, durationMs float64, opts ...Option) {
	keyID := keyIDOf(opts)
	if l.log == nil && l.ring == nil {
		return
	}
	if l.log != nil {
		l.log.LogAttrs(context.Background(), slog.LevelInfo, "", keyAttr(
			keyID, []slog.Attr{
				slog.String("event", EventAPIRequest),
				slog.String("actor", applog.Sanitize(actor)),
				slog.String("method", applog.Sanitize(method)),
				slog.String("path", applog.Sanitize(path)),
				slog.String("outcome", outcome),
				slog.Int("status", status),
				slog.Float64("duration_ms", durationMs),
			})...)
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
			KeyID:      keyID,
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
			slog.String("actor", applog.Sanitize(actor)),
			slog.String("method", applog.Sanitize(method)),
			slog.String("path", applog.Sanitize(path)),
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
			slog.String("actor", applog.Sanitize(actor)),
			slog.String("method", applog.Sanitize(method)),
			slog.String("path", applog.Sanitize(path)),
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
func (l *Logger) ComposeOp(actor, operation, stack, outcome string, opts ...Option) {
	keyID := keyIDOf(opts)
	if l.log == nil && l.ring == nil {
		return
	}
	if l.log != nil {
		l.log.LogAttrs(context.Background(), slog.LevelInfo, "", keyAttr(
			keyID, []slog.Attr{
				slog.String("event", EventComposeOp),
				slog.String("actor", applog.Sanitize(actor)),
				slog.String("operation", applog.Sanitize(operation)),
				slog.String("stack", applog.Sanitize(stack)),
				slog.String("outcome", outcome),
			})...)
	}
	if l.ring != nil {
		l.ring.push(Record{
			Time:      time.Now().UTC(),
			Event:     EventComposeOp,
			Actor:     actor,
			Operation: operation,
			Stack:     stack,
			Outcome:   outcome,
			KeyID:     keyID,
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
			slog.String("actor", applog.Sanitize(actor)),
			slog.String("key_id", applog.Sanitize(keyID)),
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

func (l *Logger) KeyRotation(actor, operation, keyID, outcome string) {
	if l.log != nil {
		l.log.Info("", slog.String("event", EventKeyRotation),
			slog.String("actor", applog.Sanitize(actor)),
			slog.String("operation", applog.Sanitize(operation)),
			slog.String("key_id", applog.Sanitize(keyID)), slog.String("outcome", outcome))
	}
	if l.ring != nil {
		l.ring.push(Record{Time: time.Now().UTC(), Event: EventKeyRotation,
			Actor: actor, Operation: operation, KeyID: keyID, Outcome: outcome})
	}
}

// SignatureLock records a downgrade-lock change: operation is "lock" when a key
// first verifies a v3 request, "clear" or "clear_all" when an operator removes
// locks. keyID is empty for "clear_all".
func (l *Logger) SignatureLock(actor, operation, keyID, outcome string) {
	if l.log != nil {
		l.log.Info("", slog.String("event", EventSignatureLock),
			slog.String("actor", applog.Sanitize(actor)),
			slog.String("operation", applog.Sanitize(operation)),
			slog.String("key_id", applog.Sanitize(keyID)), slog.String("outcome", outcome))
	}
	if l.ring != nil {
		l.ring.push(Record{Time: time.Now().UTC(), Event: EventSignatureLock,
			Actor: actor, Operation: operation, KeyID: keyID, Outcome: outcome})
	}
}

// ExecOutcome maps an exec admission decision to the outcome an exec_start
// record carries: OutcomeAllowed when a session slot was taken, OutcomeDenied
// when the request was refused.
func ExecOutcome(admitted bool) string {
	if admitted {
		return OutcomeAllowed
	}
	return OutcomeDenied
}

// ExecStart records an exec start attempt at the admission decision, before
// anything reaches the Docker daemon. outcome is OutcomeAllowed when the exec
// was admitted and OutcomeDenied when a limit refused it.
func (l *Logger) ExecStart(actor, container, execID, outcome string, opts ...Option) {
	keyID := keyIDOf(opts)
	if l.log == nil && l.ring == nil {
		return
	}
	if l.log != nil {
		l.log.LogAttrs(context.Background(), slog.LevelInfo, "", keyAttr(
			keyID, []slog.Attr{
				slog.String("event", EventExecStart),
				slog.String("actor", applog.Sanitize(actor)),
				slog.String("container", applog.Sanitize(container)),
				slog.String("exec_id", applog.Sanitize(execID)),
				slog.String("outcome", outcome),
			})...)
	}
	if l.ring != nil {
		l.ring.push(Record{
			Time:      time.Now().UTC(),
			Event:     EventExecStart,
			Actor:     actor,
			Container: container,
			ExecID:    execID,
			Outcome:   outcome,
			KeyID:     keyID,
		})
	}
}
