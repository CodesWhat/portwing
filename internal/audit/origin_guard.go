package audit

import (
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/codeswhat/portwing/internal/config"
	applog "github.com/codeswhat/portwing/internal/log"
)

// maxLoggedOrigin bounds how much of a rejected Origin or Host value reaches
// the log.
const maxLoggedOrigin = 128

const (
	// warnBurst is how many rejection WARN lines one guard writes per
	// window of warnWindowSeconds. A page can fire rejected requests as fast as the browser
	// allows, so the log line is sampled; the metric still counts every one.
	warnBurst = 5
	// warnWindowSeconds is multiplied by time.Second inside allow: an operator
	// in a package-level constant sits outside every coverage block, so
	// mutation testing scores it as not covered whatever the tests do.
	warnWindowSeconds = 10

	// rejectBody is the one body every guard rejection sends, whichever check
	// fired, so a probe cannot tell the Host check from the Origin check.
	rejectBody = "forbidden origin"
)

// RequestCounter is the request-metrics surface OriginGuard uses.
// *metrics.Registry satisfies it without coupling this package to it.
type RequestCounter interface {
	IncRequest(method string, code int)
}

// OriginGuard is the admission middleware that runs before authentication. It
// turns away a request whose Origin header is not allowlisted and, when
// CheckHost is set, one whose Host header is not an acceptable name (see
// config.HostAllowlist). Every field except Allow is optional.
type OriginGuard struct {
	Allow *config.OriginAllowlist
	// CheckHost turns on the Host check. Set it only on a surface with no
	// authentication of its own: with auth on, a rebinding page cannot present
	// the credential, so auth is the control.
	CheckHost bool
	Hosts     *config.HostAllowlist
	// Auditor receives a denied api_request record per rejection. Leave it nil
	// on a listener whose records would evict the audit ring's real history.
	Auditor  *Logger
	Requests RequestCounter
	// Actor names the caller in the audit record and log. It defaults to the
	// peer address of the connection.
	Actor func(*http.Request) string
}

// Wrap returns next guarded by g. It runs before authentication and before any
// handler, including WebSocket and hijack upgrades.
func (g OriginGuard) Wrap(next http.Handler) http.Handler {
	warn := &warnLimiter{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.CheckHost && !g.Hosts.Admits(r.Host) {
			g.reject(w, r, "host", r.Host, warn)
			return
		}
		if ok, offending := g.Allow.Admits(r); !ok {
			g.reject(w, r, "origin", offending, warn)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (g OriginGuard) reject(w http.ResponseWriter, r *http.Request, kind, offending string, warn *warnLimiter) {
	start := time.Now()
	actor := g.actor(r)
	if emit, suppressed := warn.allow(start); emit {
		slog.Warn("request rejected: "+kind+" not allowed",
			"ip", applog.Sanitize(actor),
			kind, boundedOrigin(offending),
			"method", applog.Sanitize(r.Method),
			"suppressed_since_last", suppressed)
	}
	if g.Auditor != nil {
		g.Auditor.APIRequest(actor, r.Method, r.URL.Path, OutcomeDenied, http.StatusForbidden, elapsedMs(start))
	}
	if g.Requests != nil {
		g.Requests.IncRequest(r.Method, http.StatusForbidden)
	}
	forbid(w)
}

func (g OriginGuard) actor(r *http.Request) string {
	if g.Actor != nil {
		return g.Actor(r)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// forbid answers 403 before any body is read. Like the server's other
// admission rejections it aborts the drain of an unread body and closes the
// connection, so a declared-but-never-sent body cannot pin the handler.
func forbid(w http.ResponseWriter) {
	// Best effort: a writer with no connection behind it reports
	// ErrNotSupported and has no drain to abort.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now())
	w.Header().Set("Connection", "close")
	http.Error(w, rejectBody, http.StatusForbidden)
}

// boundedOrigin makes an attacker-supplied Origin or Host safe and short enough
// to log.
func boundedOrigin(value string) string {
	if len(value) > maxLoggedOrigin {
		value = value[:maxLoggedOrigin] + "..."
	}
	return applog.Sanitize(value)
}

func elapsedMs(start time.Time) float64 {
	return float64(time.Since(start).Nanoseconds()) / 1e6
}

// warnLimiter is a fixed-window sampler for the rejection WARN line: the first
// warnBurst rejections in each window of warnWindowSeconds log, the rest are counted and
// reported on the first line of the next window.
type warnLimiter struct {
	mu         sync.Mutex
	windowEnd  time.Time
	logged     int
	suppressed int
}

// allow reports whether to log now and how many rejections were skipped since
// the previous window's last line.
func (l *warnLimiter) allow(now time.Time) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !now.Before(l.windowEnd) {
		skipped := l.suppressed
		l.windowEnd = now.Add(warnWindowSeconds * time.Second)
		l.logged = 1
		l.suppressed = 0
		return true, skipped
	}
	if l.logged < warnBurst {
		l.logged++
		return true, 0
	}
	l.suppressed++
	return false, 0
}
