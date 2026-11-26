// SPDX-License-Identifier: MIT

package edge

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync/atomic"

	"github.com/codeswhat/portwing/internal/adapter"
	"github.com/codeswhat/portwing/internal/audit"
	applog "github.com/codeswhat/portwing/internal/log"
	"github.com/codeswhat/portwing/internal/protocol"
)

// controllerCancel is the context cause a dd:cancel frame cancels a request
// with. Telling it apart from context.Canceled (connection loss, shutdown) and
// a deadline is what lets a handler report "cancelled" instead of "error".
type controllerCancel struct{ reason string }

func (e *controllerCancel) Error() string { return "request cancelled by controller" }

// inflightRequest is one tracked request goroutine. The handler records on it
// what the terminal frame owes the controller, and serveTracked sends that
// frame only after the request's slots are released.
type inflightRequest struct {
	cancel context.CancelCauseFunc
	// headerSent is set once the stream response header went out, which
	// switches the terminal frame from error{cancelled} to stream_end.
	headerSent atomic.Bool
	// owed is set by the handler when it ended because of a controller
	// cancel and suppressed its own terminal frame.
	owed atomic.Bool
}

type inflightKey struct{}

func inflightFrom(ctx context.Context) *inflightRequest {
	e, _ := ctx.Value(inflightKey{}).(*inflightRequest)
	return e
}

// cancelledByController reports whether ctx was cancelled by a dd:cancel
// frame and, if so, marks the request as owing a cancelled terminal frame and
// returns the controller's reason. Handlers call it where a Docker call or
// stream read failed, before sending their own terminal frame.
func cancelledByController(ctx context.Context) (reason string, ok bool) {
	reason, ok = controllerCancelReason(ctx)
	if !ok {
		return "", false
	}
	if e := inflightFrom(ctx); e != nil {
		e.owed.Store(true)
	}
	return reason, true
}

// controllerCancelReason reports whether ctx was cancelled by a dd:cancel
// frame without marking the request as owing a terminal frame. A handler that
// still has to decide whether it ends as cancelled uses this, then calls
// cancelledByController once it commits to suppressing its own frame.
func controllerCancelReason(ctx context.Context) (string, bool) {
	var cc *controllerCancel
	if !errors.As(context.Cause(ctx), &cc) {
		return "", false
	}
	return cc.reason, true
}

// trackRequest derives a cancellable context for one request and registers it
// under requestID so dd:cancel can find it. A second in-flight request with
// the same id is still cancellable by connection loss but not by id: the first
// registration keeps the id, so a duplicate can't hijack or evict it. The
// registry is bounded by maxStreams because it is only filled inside an
// admitted slot.
func (c *Client) trackRequest(ctx context.Context, requestID string) (context.Context, *inflightRequest, func()) {
	rctx, cancel := context.WithCancelCause(ctx)
	e := &inflightRequest{cancel: cancel}
	rctx = context.WithValue(rctx, inflightKey{}, e)

	c.inflightMu.Lock()
	if c.inflight == nil {
		c.inflight = make(map[string]*inflightRequest)
	}
	if _, dup := c.inflight[requestID]; !dup {
		c.inflight[requestID] = e
	}
	c.inflightMu.Unlock()

	return rctx, e, func() {
		c.inflightMu.Lock()
		if c.inflight[requestID] == e {
			delete(c.inflight, requestID)
		}
		c.inflightMu.Unlock()
		cancel(nil)
	}
}

// cancelInflight cancels the tracked request under requestID. It reports
// false for an unknown or already finished id.
func (c *Client) cancelInflight(requestID, reason string) bool {
	c.inflightMu.Lock()
	e := c.inflight[requestID]
	c.inflightMu.Unlock()
	if e == nil {
		return false
	}
	e.cancel(&controllerCancel{reason: reason})
	return true
}

// serveTracked runs one request handler under a cancellable, registered
// context. release frees the caller's admission slot (and any budget); it is
// called before the terminal cancelled frame so seeing that frame means
// capacity is free. release must be idempotent: callers also defer it.
func (c *Client) serveTracked(ctx context.Context, req protocol.RequestMessage, target outboundTarget, release func()) {
	tr := c.registerRequest(ctx, req.RequestID)
	c.serveRegistered(tr, req, target, release)
}

// trackedRequest is a request already registered in the inflight registry but
// not yet running.
type trackedRequest struct {
	ctx     context.Context
	e       *inflightRequest
	untrack func()
}

// registerRequest registers requestID synchronously. Callers on the read pump
// use it before spawning the handler goroutine, so a dd:cancel that follows on
// the wire always finds the id instead of racing the goroutine's start.
func (c *Client) registerRequest(ctx context.Context, requestID string) *trackedRequest {
	rctx, e, untrack := c.trackRequest(ctx, requestID)
	return &trackedRequest{ctx: rctx, e: e, untrack: untrack}
}

// clearInflight empties the registry. Connection teardown calls it: every
// tracked handler's context derives from the connection and is already
// cancelled, and a lingering handler must not hold its id against a retry on
// the next connection. untrack compares by pointer, so a lingering handler's
// own cleanup can't evict a successor.
func (c *Client) clearInflight() {
	c.inflightMu.Lock()
	clear(c.inflight)
	c.inflightMu.Unlock()
}

// serveRegistered runs a handler for a request registered by registerRequest.
func (c *Client) serveRegistered(tr *trackedRequest, req protocol.RequestMessage, target outboundTarget, release func()) {
	rctx, e, untrack := tr.ctx, tr.e, tr.untrack
	func() {
		defer untrack()
		c.handleRequestTo(rctx, req, target)
	}()
	release()
	if !e.owed.Load() {
		return
	}
	if e.headerSent.Load() {
		_ = c.sendTypedMessageTo(target, protocol.TypeStreamEnd, protocol.StreamEndMessage{
			RequestID: req.RequestID,
			Reason:    "cancelled",
		})
		return
	}
	_ = c.sendTypedMessageTo(target, protocol.TypeError, protocol.ErrorMessage{
		Message:   "request cancelled",
		Code:      protocol.ErrorCodeCancelled,
		RequestID: req.RequestID,
	})
}

// handleCancelFrame processes a dd:cancel frame inline on the read pump. It
// only does map lookups and a context cancel, so it needs no admission.
func (c *Client) handleCancelFrame(ctx context.Context, sender adapter.MessageSender, env protocol.Envelope) {
	var msg protocol.DDCancelMessage
	if err := json.Unmarshal(env.Data, &msg); err != nil {
		slog.Debug("invalid dd:cancel message", "error", err)
		return
	}
	if msg.RequestID == "" || len(msg.RequestID) > protocol.MaxCancelRequestIDLen {
		slog.Debug("ignoring dd:cancel with invalid requestId", "length", len(msg.RequestID))
		return
	}
	reason := msg.Reason
	if len(reason) > protocol.MaxCancelReasonLen {
		reason = reason[:protocol.MaxCancelReasonLen]
	}

	// A pending upload is the existing request-cancelled behavior, with no
	// frame, so dd:cancel is a strict superset of it.
	if c.cancelPendingBody(msg.RequestID) {
		slog.Debug("cancelled pending request body", "requestId", applog.Sanitize(msg.RequestID), "reason", applog.Sanitize(reason))
		return
	}
	if c.cancelInflight(msg.RequestID, reason) {
		slog.Debug("cancelled in-flight request", "requestId", applog.Sanitize(msg.RequestID), "reason", applog.Sanitize(reason))
		return
	}
	// Not ours: the adapter owns container log stream ids. An unknown id is
	// a silent no-op there too.
	if !c.adapter.HandleMessage(ctx, sender, env.Type, env.Data) {
		slog.Debug("unhandled message type", "type", applog.Sanitize(env.Type))
	}
}

// auditCancelledAPI records a request ended by dd:cancel.
func (c *Client) auditCancelledAPI(req protocol.RequestMessage, status int, durationMs float64, target outboundTarget, reason string) {
	c.auditor.APIRequest(c.redactedControllerURL(), req.Method, auditPath(req.Path), audit.OutcomeCancelled, status, durationMs, target.auditKeyID(), audit.WithReason(reason))
}
