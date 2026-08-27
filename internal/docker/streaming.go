package docker

import (
	"net/http"
	"strings"
)

// IsStreamingRequest returns true when the Docker request method and path
// produce a streaming response. Container archive paths are method-sensitive:
// GET downloads a tar stream, while PUT uploads one and returns no tar body.
func IsStreamingRequest(method, path string) bool {
	pathOnly, _, _ := strings.Cut(path, "?")
	if strings.Contains(pathOnly, "/containers/") && strings.HasSuffix(pathOnly, "/archive") {
		return method == http.MethodGet
	}
	return IsStreamingPath(pathOnly)
}

// IsStreamingPath returns true if the path corresponds to a Docker API
// endpoint that produces a streaming response.
func IsStreamingPath(path string) bool {
	path, _, _ = strings.Cut(path, "?")

	streamSuffixes := []string{
		"/logs",
		"/attach",
		"/events",
		"/build",
		"/images/create",
		"/images/push",
		"/export", // GET /containers/{id}/export — container filesystem tar, routinely large
	}
	for _, suffix := range streamSuffixes {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	if strings.Contains(path, "/exec/") && strings.HasSuffix(path, "/start") {
		return true
	}
	// GET /containers/{id}/archive streams a filesystem tar. Match it within
	// the container namespace so unrelated endpoints ending in /archive do not
	// inherit streaming behavior.
	if strings.Contains(path, "/containers/") && strings.HasSuffix(path, "/archive") {
		return true
	}
	// GET /images/get (docker save, multi-image) and GET /images/{name}/get
	// (docker save, single image) both stream a tar of image layers,
	// routinely >100MB. The image name segment is arbitrary — and may itself
	// contain slashes for a namespaced repo — so it can't be matched as a
	// literal suffix the way the endpoints above are. Anchor on "/images/"
	// so this doesn't overmatch unrelated paths that happen to end in "/get".
	if strings.Contains(path, "/images/") && strings.HasSuffix(path, "/get") {
		return true
	}
	return false
}
