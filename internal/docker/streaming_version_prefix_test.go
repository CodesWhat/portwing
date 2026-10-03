package docker

import (
	"net/http"
	"testing"
)

// Docker's own router accepts a version prefix of "/v" followed by digits and
// dots, and matches the literal "v" case-sensitively. Portainer's 2.39.7 and
// 2.45.0 authorization bypass came from a proxy that classified paths only for
// the canonical "/vMAJOR.MINOR/" prefix while the daemon accepted more. Every
// case below pins whether Portwing's streaming classification follows the
// daemon (the request is routed like its canonical form) or leaves a path the
// daemon would reject unclassified, so a forwarded request is never
// reclassified into a less-guarded family.

func TestIsStreamingRequestVersionPrefixes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		// Stats family (GET, streaming by default).
		{"stats canonical prefix streams", http.MethodGet, "/v1.47/containers/abc/stats", true},
		{"stats unversioned streams", http.MethodGet, "/containers/abc/stats", true},
		{"stats leading-zero prefix streams like the daemon", http.MethodGet, "/v01.47/containers/abc/stats", true},
		{"stats stream=0 opts out under canonical prefix", http.MethodGet, "/v1.47/containers/abc/stats?stream=0", false},
		{"stats stream=0 opts out under leading-zero prefix", http.MethodGet, "/v01.47/containers/abc/stats?stream=false", false},
		{"stats non-GET never streams", http.MethodPost, "/v1.47/containers/abc/stats", false},
		{"stats uppercase V is not a version; daemon 404s it", http.MethodGet, "/V1.47/containers/abc/stats", false},
		{"stats trailing slash is not the route; daemon 404s it", http.MethodGet, "/v1.47/containers/abc/stats/", false},
		{"stats doubled slash after prefix is not the route", http.MethodGet, "/v1.47//containers/abc/stats", false},
		{"stats id containing a slash is not the route", http.MethodGet, "/v1.47/containers/a/b/stats", false},
		{"stats dot-dot id errs toward streaming, the guarded side", http.MethodGet, "/v1.47/containers/../stats", true},

		// Push family (POST only).
		{"push canonical prefix streams", http.MethodPost, "/v1.47/images/nginx/push", true},
		{"push unversioned streams", http.MethodPost, "/images/nginx/push", true},
		{"push leading-zero prefix streams like the daemon", http.MethodPost, "/v01.47/images/nginx/push", true},
		{"push namespaced image streams", http.MethodPost, "/v1.47/images/registry.example/team/app/push", true},
		{"push GET never streams", http.MethodGet, "/v1.47/images/nginx/push", false},
		{"push uppercase V is not a version; daemon 404s it", http.MethodPost, "/V1.47/images/nginx/push", false},
		{"push empty image name is not the route", http.MethodPost, "/v1.47/images//push", false},
		{"push trailing slash is not the route", http.MethodPost, "/v1.47/images/nginx/push/", false},

		// Suffix-matched families do not depend on the prefix at all, so an odd
		// prefix cannot move them out of streaming.
		{"logs under odd prefix still streams", http.MethodGet, "/v1.47.0/containers/abc/logs", true},
		{"build under odd prefix still streams", http.MethodPost, "/v1.47.0/build", true},
		{"exec start under odd prefix still streams", http.MethodPost, "/v1.47.0/exec/abc/start", true},
		{"events under uppercase V still streams", http.MethodGet, "/V1.47/events", true},
		{"archive GET under odd prefix still streams", http.MethodGet, "/v1.47.0/containers/abc/archive", true},
		{"archive PUT under odd prefix does not stream", http.MethodPut, "/v1.47.0/containers/abc/archive", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsStreamingRequest(tc.method, tc.path); got != tc.want {
				t.Fatalf("IsStreamingRequest(%s, %q) = %v, want %v", tc.method, tc.path, got, tc.want)
			}
		})
	}
}

func TestStreamingRouteFamilyVersionPrefixes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		path      string
		wantStats bool
		wantPush  bool
	}{
		{"canonical stats", "/v1.47/containers/abc/stats", true, false},
		{"canonical push", "/v1.47/images/nginx/push", false, true},
		{"unversioned stats", "/containers/abc/stats", true, false},
		{"leading zero major and minor", "/v01.047/containers/abc/stats", true, false},
		{"prefix without dot is not stripped", "/v1/containers/abc/stats", false, false},
		{"prefix with empty minor is not stripped", "/v1./containers/abc/stats", false, false},
		{"prefix with non-digit minor is not stripped", "/v1.x/containers/abc/stats", false, false},
		{"prefix with no rest is not stripped", "/v1.47", false, false},
		{"uppercase V is not stripped", "/V1.47/containers/abc/stats", false, false},
		{"prefix then slash then stats is not stripped", "/v1.47//containers/abc/stats", false, false},
		{"bare v prefix is not stripped", "/v/containers/abc/stats", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stats, push := streamingRouteFamily(tc.path)
			if stats != tc.wantStats || push != tc.wantPush {
				t.Fatalf("streamingRouteFamily(%q) = (%v, %v), want (%v, %v)", tc.path, stats, push, tc.wantStats, tc.wantPush)
			}
		})
	}
}
