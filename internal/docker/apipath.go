package docker

import (
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
// only MAJOR.MINOR: "/v1.47.0/", "/v01.47/", "/v1/" and "/v./" all select the
// same handlers as the bare route. The "v" is case-sensitive. Portwing matches
// the same shape wherever it recognises a Docker route, so a spelling the
// daemon would route can never classify differently from its canonical form.
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
