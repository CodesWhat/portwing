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
	}
	for _, suffix := range streamSuffixes {
		if strings.HasSuffix(path, suffix) || strings.Contains(path, suffix+"?") {
			return true
		}
	}
	if strings.Contains(path, "/exec/") && strings.HasSuffix(path, "/start") {
		return true
	}
	return false
}
