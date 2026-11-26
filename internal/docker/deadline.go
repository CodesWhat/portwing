// SPDX-License-Identifier: MIT

package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// MinRequestTimeout and MaxRequestTimeout bound a controller's timeout
	// hint and the auto-deadline rules.
	MinRequestTimeout = time.Second
	MaxRequestTimeout = 15 * time.Minute

	// stopTimeoutMargin is added to a stop/restart grace period so the
	// daemon's own SIGKILL and cleanup fit inside the deadline.
	stopTimeoutMargin = 15 * time.Second

	// maxTimeoutHeaderLen is the longest accepted X-Portwing-Timeout-Ms value.
	maxTimeoutHeaderLen = 7
)

// ParseTimeoutHeader parses the Standard-mode hint header. Only ASCII decimal
// digits, at most seven of them, count; anything else is "no hint".
func ParseTimeoutHeader(v string) (ms int64, ok bool) {
	if v == "" || len(v) > maxTimeoutHeaderLen {
		return 0, false
	}
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

var (
	stopRestartPath = regexp.MustCompile(`^(?:/v[0-9.]+)?/containers/[^/]+/(?:stop|restart)$`)
	waitPath        = regexp.MustCompile(`^(?:/v[0-9.]+)?/containers/[^/]+/wait$`)
)

// RequestDeadline computes the deadline for one non-stream Docker round trip.
//
// A hint (haveHint with hintMs above zero) wins and is clamped to
// [MinRequestTimeout, MaxRequestTimeout]; a zero or negative hint is "no
// hint". Without one, POST stop/restart with t=N gets max(def, N+15s) capped at
// MaxRequestTimeout (t<0 waits forever on the daemon, so it gets the cap), and
// POST wait gets the cap. Everything else gets def. The bool reports whether a
// hint or an auto-deadline rule decided the result, which is what lets Standard
// mode answer a coded 504 only to callers that opted in.
func RequestDeadline(method, path string, hintMs int64, haveHint bool, def time.Duration) (time.Duration, bool) {
	if haveHint && hintMs > 0 {
		d := MaxRequestTimeout
		if hintMs < int64(MaxRequestTimeout/time.Millisecond) {
			d = time.Duration(hintMs) * time.Millisecond
		}
		return max(d, MinRequestTimeout), true
	}
	// REQUEST_TIMEOUT=0 disables the total timeout, so the auto rules (which only
	// ever extend a bounded default) leave the call unbounded.
	if def <= 0 || method != http.MethodPost {
		return def, false
	}
	pathOnly, query, _ := strings.Cut(path, "?")
	pathOnly = decodePath(pathOnly)
	if waitPath.MatchString(pathOnly) {
		return MaxRequestTimeout, true
	}
	if stopRestartPath.MatchString(pathOnly) {
		values, _ := url.ParseQuery(query)
		t, err := strconv.ParseInt(strings.TrimSpace(values.Get("t")), 10, 64)
		if err != nil {
			return def, false
		}
		if t < 0 || t > int64(MaxRequestTimeout/time.Second) {
			return MaxRequestTimeout, true
		}
		return min(max(def, time.Duration(t)*time.Second+stopTimeoutMargin), MaxRequestTimeout), true
	}
	return def, false
}

// TimeoutError reports that a non-stream Docker round trip exceeded the
// deadline Portwing gave it. The operation may still finish on the daemon.
type TimeoutError struct {
	After time.Duration
	Err   error
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("docker request exceeded %s: %v", e.After, e.Err)
}

func (e *TimeoutError) Unwrap() error { return e.Err }

// AsTimeout reports whether err is, or wraps, a TimeoutError.
func AsTimeout(err error) (*TimeoutError, bool) {
	var te *TimeoutError
	if errors.As(err, &te) {
		return te, true
	}
	return nil, false
}

// DefaultTimeout returns the client's normal per-request timeout. Zero means
// REQUEST_TIMEOUT=0 disabled it.
func (c *Client) DefaultTimeout() time.Duration {
	if c.httpClient == nil {
		return defaultDialTimeout
	}
	return c.httpClient.Timeout
}

// deadlineClient is the non-stream client without its total Timeout, so a
// context deadline decides how long the call may run.
func (c *Client) deadlineClient() *http.Client {
	cp := *c.httpClient
	cp.Timeout = 0
	return &cp
}

// DoWithDeadline is DoWithHeaders with a per-call deadline that covers header
// and body read, as Client.Timeout does. The deadline's cancel runs when the
// returned body is closed, so the caller must close it.
func (c *Client) DoWithDeadline(ctx context.Context, method, path string, headers http.Header, body io.Reader, d time.Duration) (*http.Response, error) {
	dctx, cancel := withOptionalTimeout(ctx, d)
	resp, err := c.do(dctx, method, path, headers, body, c.deadlineClient())
	if err != nil {
		cancel()
		return nil, wrapDeadline(ctx, dctx, d, err)
	}
	resp.Body = &deadlineBody{ReadCloser: resp.Body, parent: ctx, ctx: dctx, cancel: cancel, d: d}
	return resp, nil
}

// DoRawWithDeadline is DoRaw with a per-call deadline; see DoWithDeadline.
func (c *Client) DoRawWithDeadline(req *http.Request, d time.Duration) (*http.Response, error) {
	parent := req.Context()
	dctx, cancel := withOptionalTimeout(parent, d)
	// #nosec G704 -- caller rewrites requests to the fixed local Docker endpoint before forwarding.
	resp, err := c.deadlineClient().Do(req.WithContext(dctx))
	if err != nil {
		cancel()
		return nil, wrapDeadline(parent, dctx, d, err)
	}
	resp.Body = &deadlineBody{ReadCloser: resp.Body, parent: parent, ctx: dctx, cancel: cancel, d: d}
	return resp, nil
}

// withOptionalTimeout bounds ctx by d, or only makes it cancelable when d is
// not positive (REQUEST_TIMEOUT=0).
func withOptionalTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, d)
}

func wrapDeadline(parent, dctx context.Context, d time.Duration, err error) error {
	if parent.Err() == nil && errors.Is(dctx.Err(), context.DeadlineExceeded) {
		return &TimeoutError{After: d, Err: err}
	}
	return err
}

type deadlineBody struct {
	io.ReadCloser
	parent context.Context
	ctx    context.Context
	cancel context.CancelFunc
	d      time.Duration
}

func (b *deadlineBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		err = wrapDeadline(b.parent, b.ctx, b.d, err)
	}
	return n, err
}

func (b *deadlineBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}
