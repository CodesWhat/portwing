package config

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Browser Origin validation for ALLOWED_ORIGINS. A request that carries no
// Origin header (the Docker CLI, Drydock's agent client, curl, MCP clients) is
// never touched. One that carries an Origin is admitted only when it is an
// exact match for an entry in the operator's allowlist, which is empty by
// default. The request Host is never consulted: a DNS-rebinding page presents
// an Origin equal to its Host, so "same origin" is exactly the case that must
// stay rejected.

// headerOrigin is the canonical key net/http stores the header under. Indexing
// the header map with it directly keeps the absent-header check allocation free.
const headerOrigin = "Origin"

// OriginAllowlist is a set of canonical origins. The zero value allows none.
type OriginAllowlist struct {
	allowed map[string]struct{}
}

// ParseOriginAllowlist validates entries and returns the allowlist they describe. Each entry
// must be an exact origin: scheme://host[:port] with an http or https scheme,
// a host, and no userinfo, path, query or fragment. Anything else, including
// "*", is an error naming the offending entry.
func ParseOriginAllowlist(entries []string) (*OriginAllowlist, error) {
	allowed := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		key, err := canonical(entry)
		if err != nil {
			return nil, fmt.Errorf("invalid origin %q: %w", entry, err)
		}
		allowed[key] = struct{}{}
	}
	return &OriginAllowlist{allowed: allowed}, nil
}

// Len reports how many origins the list holds.
func (a *OriginAllowlist) Len() int {
	if a == nil {
		return 0
	}
	return len(a.allowed)
}

func (a *OriginAllowlist) contains(key string) bool {
	if a == nil {
		return false
	}
	_, ok := a.allowed[key]
	return ok
}

// canonical parses raw as an origin and returns its comparison key:
// lowercase scheme and host, with the port always present (80 for http and 443
// for https when omitted). Comparison is on this parsed form, never on the raw
// string, so no prefix or substring of an allowed origin matches.
func canonical(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("empty origin")
	}
	if strings.ContainsAny(raw, "?#@ \t") {
		return "", errors.New("must be scheme://host[:port] with no userinfo, path, query or fragment")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("not a valid URL")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("scheme must be http or https")
	}
	if u.Opaque != "" || u.User != nil || u.Path != "" || u.RawPath != "" {
		return "", errors.New("must be scheme://host[:port] with no userinfo, path, query or fragment")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", errors.New("missing host")
	}
	if !validHost(host) {
		return "", errors.New("host must be a DNS name or an IP address")
	}
	port, err := normalizePort(scheme, u.Port())
	if err != nil {
		return "", err
	}
	return scheme + "://" + net.JoinHostPort(host, port), nil
}

// validHost accepts an IP literal or a name made of ASCII letters, digits,
// hyphens, underscores and dots. A browser sends internationalised names as
// punycode, so non-ASCII never needs to match, and rejecting it keeps
// wildcards and other pattern syntax out of an exact-match list.
func validHost(host string) bool {
	if strings.Contains(host, ":") {
		return net.ParseIP(host) != nil
	}
	for i := 0; i < len(host); i++ {
		if !hostByte(host[i]) {
			return false
		}
	}
	return true
}

func hostByte(c byte) bool {
	if c >= 'a' && c <= 'z' {
		return true
	}
	if c >= '0' && c <= '9' {
		return true
	}
	return c == '-' || c == '.' || c == '_'
}

func normalizePort(scheme, port string) (string, error) {
	if port == "" {
		return defaultPort(scheme), nil
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", errors.New("port must be between 1 and 65535")
	}
	return strconv.Itoa(n), nil
}

func defaultPort(scheme string) string {
	if scheme == "https" {
		return "443"
	}
	return "80"
}

// Admits reports whether the request may proceed and, when it may not, the raw
// Origin value to log. A request with no Origin header is always admitted.
func (a *OriginAllowlist) Admits(r *http.Request) (ok bool, offending string) {
	values := r.Header[headerOrigin]
	if len(values) == 0 {
		return true, ""
	}
	if len(values) > 1 {
		return false, strings.Join(values, ",")
	}
	key, err := canonical(values[0])
	if err != nil {
		return false, values[0]
	}
	return a.contains(key), values[0]
}
