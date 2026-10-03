package docker

import (
	"net/http"
	"net/url"
	"strings"
)

// IsStreamingRequest returns true when the Docker request method and path
// produce a streaming response. Container archive paths are method-sensitive:
// GET downloads a tar stream, while PUT uploads one and returns no tar body.
//
// The path is classified percent-decoded, as the daemon routes it, so an
// encoded spelling of a streaming route cannot dodge streaming treatment.
func IsStreamingRequest(method, path string) bool {
	pathOnly, query, _ := strings.Cut(path, "?")
	pathOnly = decodePath(pathOnly)
	stats, push := streamingRouteFamily(pathOnly)
	if stats {
		if method != http.MethodGet {
			return false
		}
		values, _ := url.ParseQuery(query)
		if _, present := values["stream"]; present {
			switch strings.ToLower(strings.TrimSpace(values.Get("stream"))) {
			case "", "0", "no", "false", "none":
				return false
			}
		}
		return true
	}
	if push {
		return method == http.MethodPost
	}
	if strings.Contains(pathOnly, "/containers/") && strings.HasSuffix(pathOnly, "/archive") {
		return method == http.MethodGet
	}
	return isStreamingPath(pathOnly)
}

// IsStreamingPath returns true if the path corresponds to a Docker API
// endpoint that produces a streaming response. The path is classified
// percent-decoded, as the daemon routes it.
func IsStreamingPath(path string) bool {
	path, _, _ = strings.Cut(path, "?")
	return isStreamingPath(decodePath(path))
}

// isStreamingPath classifies an already decoded path with no query.
func isStreamingPath(path string) bool {
	if stats, push := streamingRouteFamily(path); stats || push {
		return true
	}

	streamSuffixes := []string{
		"/logs",
		"/attach",
		"/events",
		"/build",
		"/images/create",
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

// streamingRouteFamily matches the stats and named-image push routes after an
// optional Docker API version prefix (see StripAPIVersion).
func streamingRouteFamily(path string) (stats, push bool) {
	path = StripAPIVersion(path)
	if name, ok := strings.CutPrefix(path, "/containers/"); ok {
		if id, matched := strings.CutSuffix(name, "/stats"); matched && id != "" && !strings.Contains(id, "/") {
			stats = true
		}
	}
	if name, ok := strings.CutPrefix(path, "/images/"); ok {
		if image, matched := strings.CutSuffix(name, "/push"); matched && image != "" {
			push = true
		}
	}
	return stats, push
}
