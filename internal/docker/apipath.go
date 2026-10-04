package docker

import (
	"fmt"
	"net/url"
	"strings"
)

// StripAPIVersion removes the API version prefix the Docker daemon's router
// accepts and returns the route path that follows it, still starting with "/".
// A path without a recognised prefix is returned unchanged.
//
// The daemon registers every route twice, bare and under the gorilla/mux
// matcher "/v{version:[0-9.]+}" (moby daemon/server/server.go, versionMatcher).
// The version is therefore "v" followed by one or more digits and dots, not
// only MAJOR.MINOR: the router accepts "/v1.47.0/", "/v01.47/", "/v1/" and
// "/v./" in front of any route. Whether that version is supported is decided
// after routing, so the daemon may still answer 400 for some of them. The "v"
// is case-sensitive. Portwing matches the same shape wherever it recognises a
// Docker route, so a spelling the router would accept can never classify
// differently from its canonical form.
//
// Paths are matched decoded and uncleaned, as the daemon sees them: mux routes
// on URL.Path and redirects an unclean path rather than serving it.
func StripAPIVersion(path string) string {
	rest, ok := strings.CutPrefix(path, "/v")
	if !ok {
		return path
	}
	n := 0
	for n < len(rest) && isVersionByte(rest[n]) {
		n++
	}
	if n == 0 || n == len(rest) || rest[n] != '/' {
		return path
	}
	return rest[n:]
}

// IsResourceRoute reports whether path, after an optional API version prefix,
// has the shape of the daemon's route /{resource}/{name}/{action}. The daemon
// registers these as "/{resource}/{name:.*}/{action}", so the name is any
// non-empty string and may itself contain slashes: a container linked as
// "webapp/db" is addressed as /containers/webapp/db/stats. The action may span
// segments ("attach/ws"). Over-matching an unclean name is harmless because the
// daemon redirects unclean paths and the proxy returns that redirect rather
// than following it. The path must be decoded.
func IsResourceRoute(path, resource, action string) bool {
	rest, ok := strings.CutPrefix(StripAPIVersion(path), "/")
	if !ok {
		return false
	}
	rest, ok = strings.CutPrefix(rest, resource)
	if !ok {
		return false
	}
	name, ok := strings.CutPrefix(rest, "/")
	if !ok {
		return false
	}
	i := len(name) - len(action) - 1
	return i >= 1 && name[i] == '/' && name[i+1:] == action
}

func isVersionByte(c byte) bool {
	return c == '.' || (c >= '0' && c <= '9')
}

// decodePath returns the percent-decoded form of a request path, which is what
// the daemon's router matches. A path that is not valid percent-encoding is
// returned as is; a real server never delivers one.
func decodePath(path string) string {
	decoded, err := url.PathUnescape(path)
	if err != nil {
		return path
	}
	return decoded
}

// ValidateAPIPath rejects a caller-supplied API path that does not begin with
// "/". The client joins the API version prefix and the path with no separator
// of its own, so ".0/exec/x/start" would reach the daemon as
// "/v1.44.0/exec/x/start", a route no classifier saw as an exec start.
func ValidateAPIPath(path string) error {
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("invalid Docker API path %q: must begin with \"/\"", path)
	}
	return nil
}
