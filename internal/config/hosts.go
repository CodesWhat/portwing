package config

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

// Host validation for an unauthenticated listener. After DNS rebinding a page's
// GET and HEAD requests are same-origin, so the browser sends no Origin header
// and the Origin check alone stops nothing. What the page cannot change is the
// Host header: it is the attacker's own domain. A request is admitted only when
// its Host is an IP literal, a single-label name (this covers "localhost" and
// Compose service names such as "portwing"), or an entry in ALLOWED_HOSTS. A
// remote page with only public DNS needs a dotted name it controls, so none of
// those can be its own.
//
// Residuals, none fixable here: single-label names pass, so an attacker who
// controls the victim's DNS search suffix or local name resolution (hostile
// DHCP, LLMNR, NBNS) can still rebind. A reverse proxy that rewrites Host to
// the upstream address (nginx and Apache defaults) hides the browser's Host
// from this check, and X-Forwarded-Host is not consulted; the proxy must
// restrict its own server names. And with authentication on this check is off.

// maxHostLabel is the longest single DNS label.
const maxHostLabel = 63

// HostAllowlist is a set of extra hostnames, lowercase with no trailing dot and
// no port. The zero value adds none; the built-in rules still apply.
type HostAllowlist struct {
	allowed map[string]struct{}
}

// ParseHostAllowlist validates ALLOWED_HOSTS entries. Each must be a bare
// hostname: no scheme, port, path, userinfo or wildcard. A port is refused
// rather than ignored so an entry never reads as narrower than it is; the
// request's port is never compared. Case and one trailing dot are normalised.
func ParseHostAllowlist(entries []string) (*HostAllowlist, error) {
	allowed := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		host, err := canonicalAllowedHost(entry)
		if err != nil {
			return nil, fmt.Errorf("invalid host %q: %w", entry, err)
		}
		allowed[host] = struct{}{}
	}
	return &HostAllowlist{allowed: allowed}, nil
}

func canonicalAllowedHost(entry string) (string, error) {
	host := strings.TrimSuffix(strings.ToLower(entry), ".")
	if host == "" {
		return "", errors.New("empty host")
	}
	if strings.Contains(host, ":") {
		return "", errors.New("must be a bare hostname: no scheme, port or IPv6 literal (IP literals are always allowed)")
	}
	if strings.ContainsAny(host, "/?#@ \t*") {
		return "", errors.New("must be a bare hostname: no path, userinfo or wildcard")
	}
	if !validHost(host) {
		return "", errors.New("host must be a DNS name")
	}
	return host, nil
}

// Admits reports whether a request's Host header names an acceptable host.
//
// An empty Host is admitted. A browser always sends one, so a rebinding page
// can't omit it, while HTTP/1.0 health checkers that probe by IP (HAProxy
// httpchk, Nagios check_http) often do. A malformed value is still rejected.
func (a *HostAllowlist) Admits(hostport string) bool {
	if hostport == "" {
		return true
	}
	host, ok := requestHost(hostport)
	if !ok {
		return false
	}
	// "localhost" is a single label, so isSingleLabel admits it.
	if isSingleLabel(host) || isIPLiteral(host) {
		return true
	}
	return a.contains(strings.ToLower(host))
}

func (a *HostAllowlist) contains(host string) bool {
	if a == nil {
		return false
	}
	_, ok := a.allowed[host]
	return ok
}

// requestHost reduces a Host header to its host: the port is removed, IPv6
// brackets are stripped, and one trailing dot is dropped. Case is left as sent
// so the common names need no allocation; callers compare case-insensitively. It
// reports false for an empty or malformed value; Admits treats an empty Host as
// a separate, allowed case before it gets here.
func requestHost(hostport string) (string, bool) {
	if hostport == "" {
		return "", false
	}
	host := hostport
	if strings.HasPrefix(host, "[") {
		end := strings.IndexByte(host, ']')
		if end < 0 || !validPortSuffix(host[end+1:]) {
			return "", false
		}
		return host[1:end], host[1:end] != ""
	}
	if i := strings.IndexByte(host, ':'); i >= 0 {
		if !validPortSuffix(host[i:]) {
			return "", false
		}
		host = host[:i]
	}
	host = strings.TrimSuffix(host, ".")
	return host, host != ""
}

// validPortSuffix accepts "" or ":" followed by up to five digits.
func validPortSuffix(s string) bool {
	if s == "" {
		return true
	}
	if s[0] != ':' || len(s) > 6 {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// isIPLiteral parses host as an IPv4 or IPv6 address. netip allocates an error
// for input it rejects, so a name that cannot be an address (no colon, and not
// ending in a digit) is turned away first and a hostname costs nothing here.
func isIPLiteral(host string) bool {
	if !strings.Contains(host, ":") && !endsInDigit(host) {
		return false
	}
	_, err := netip.ParseAddr(host)
	return err == nil
}

func endsInDigit(host string) bool {
	if host == "" {
		return false
	}
	last := host[len(host)-1]
	return last >= '0' && last <= '9'
}

// isSingleLabel reports whether host is one DNS label: letters of either case,
// digits, hyphen and underscore, no dot.
func isSingleLabel(host string) bool {
	if host == "" || len(host) > maxHostLabel {
		return false
	}
	for i := 0; i < len(host); i++ {
		c := host[i]
		if c == '.' || (!hostByte(c) && !upperByte(c)) {
			return false
		}
	}
	return true
}

func upperByte(c byte) bool {
	return c >= 'A' && c <= 'Z'
}
