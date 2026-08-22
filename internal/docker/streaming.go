package docker

import "strings"

// IsStreamingPath returns true if the path corresponds to a Docker API
// endpoint that produces a streaming response.
func IsStreamingPath(path string) bool {
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
		if strings.HasSuffix(path, suffix) || strings.Contains(path, suffix+"?") {
			return true
		}
	}
	if strings.Contains(path, "/exec/") && strings.HasSuffix(path, "/start") {
		return true
	}
	// GET /images/get (docker save, multi-image) and GET /images/{name}/get
	// (docker save, single image) both stream a tar of image layers,
	// routinely >100MB. The image name segment is arbitrary — and may itself
	// contain slashes for a namespaced repo — so it can't be matched as a
	// literal suffix the way the endpoints above are. Anchor on "/images/"
	// so this doesn't overmatch unrelated paths that happen to end in "/get".
	if strings.Contains(path, "/images/") && (strings.HasSuffix(path, "/get") || strings.Contains(path, "/get?")) {
		return true
	}
	return false
}
