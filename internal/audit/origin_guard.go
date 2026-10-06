package audit

import (
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/codeswhat/portwing/internal/config"
	applog "github.com/codeswhat/portwing/internal/log"
)

// maxLoggedOrigin bounds how much of a rejected Origin value reaches the log.
const maxLoggedOrigin = 128

// RequestCounter is the request-metrics surface OriginGuard uses.
// *metrics.Registry satisfies it without coupling this package to it.
type RequestCounter interface {
	IncRequest(method string, code int)
}

// OriginGuard is the middleware that turns away a request whose Origin is not
// allowed. Auditor, Metrics and Actor are optional.
type OriginGuard struct {
	Allow    *config.OriginAllowlist
	Auditor  *Logger
	Requests RequestCounter
	// Actor names the caller in the audit record. It defaults to the peer
	// address of the connection.
	Actor func(*http.Request) string
}

// Wrap returns next guarded by g. It runs before authentication and before any
// handler, including WebSocket and hijack upgrades.
func (g OriginGuard) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ok, offending := g.Allow.Admits(r)
		if ok {
			next.ServeHTTP(w, r)
			return
		}
		g.reject(w, r, offending, start)
	})
}

func (g OriginGuard) reject(w http.ResponseWriter, r *http.Request, offending string, start time.Time) {
	actor := g.actor(r)
	slog.Warn("request rejected: origin not allowed",
		"ip", applog.Sanitize(actor),
		"origin", boundedOrigin(offending),
		"method", applog.Sanitize(r.Method))
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
	http.Error(w, "forbidden origin", http.StatusForbidden)
}

// boundedOrigin makes an attacker-supplied Origin safe and short enough to log.
func boundedOrigin(value string) string {
	if len(value) > maxLoggedOrigin {
		value = value[:maxLoggedOrigin] + "..."
	}
	return applog.Sanitize(value)
}

func elapsedMs(start time.Time) float64 {
	return float64(time.Since(start).Nanoseconds()) / 1e6
}
