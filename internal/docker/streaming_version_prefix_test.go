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
		{"prefix without dot is stripped like the daemon", "/v1/containers/abc/stats", true, false},
		{"prefix with empty minor is stripped like the daemon", "/v1./containers/abc/stats", true, false},
		{"three-part prefix is stripped like the daemon", "/v1.47.0/containers/abc/stats", true, false},
		{"dots-only prefix is stripped like the daemon", "/v./containers/abc/stats", true, false},
		{"prefix with non-digit minor is not stripped", "/v1.x/containers/abc/stats", false, false},
		{"prefix with no rest is not stripped", "/v1.47", false, false},
		{"uppercase V is not stripped", "/V1.47/containers/abc/stats", false, false},
		{"prefix then slash then stats is not stripped", "/v1.47//containers/abc/stats", false, false},
		{"bare v prefix is not stripped", "/v/containers/abc/stats", false, false},
		{"doubled prefix strips once", "/v1.47/v1.47/containers/abc/stats", false, false},
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

func TestStripAPIVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want string
	}{
		// The daemon matches "/v" + [0-9.]+ and then the route path, which starts with "/".
		{"/v1.47/containers/json", "/containers/json"},
		{"/v01.47/containers/json", "/containers/json"},
		{"/v1.47.0/containers/json", "/containers/json"},
		{"/v1.47.0.1/containers/json", "/containers/json"},
		{"/v1/containers/json", "/containers/json"},
		{"/v./containers/json", "/containers/json"},
		{"/v1../containers/json", "/containers/json"},
		{"/v0/containers/json", "/containers/json"},
		{"/v9.9/", "/"},
		// Not a daemon version prefix, so returned unchanged.
		{"/containers/json", "/containers/json"},
		{"/V1.47/containers/json", "/V1.47/containers/json"},
		{"/v/containers/json", "/v/containers/json"},
		{"/v1.x/containers/json", "/v1.x/containers/json"},
		{"/v1.47", "/v1.47"},
		{"/v1.47x/containers/json", "/v1.47x/containers/json"},
		{"/v:/containers/json", "/v:/containers/json"},
		{"/v1.47//containers/json", "//containers/json"},
		{"v1.47/containers/json", "v1.47/containers/json"},
		{"", ""},
		{"/", "/"},
		{"/v", "/v"},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			if got := StripAPIVersion(tc.path); got != tc.want {
				t.Fatalf("StripAPIVersion(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// TestIsVersionByteBoundaries pins the digit range at '0' and '9' and the
// bytes just outside it, plus the dot.
func TestIsVersionByteBoundaries(t *testing.T) {
	t.Parallel()

	for c, want := range map[byte]bool{
		'.': true, '0': true, '5': true, '9': true,
		'/': false, ':': false, '-': false, 'v': false, 'a': false, 0: false, 255: false,
	} {
		if got := isVersionByte(c); got != want {
			t.Errorf("isVersionByte(%q) = %v, want %v", c, got, want)
		}
	}
}

// Classification runs on the decoded path, as the daemon routes it, and
// decodes exactly once.
func TestIsStreamingRequestDecodesOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{"encoded letters in a route word stream", http.MethodGet, "/v1.47/containers/abc/%73tats", true},
		{"encoded letters in a push route stream", http.MethodPost, "/v1.47/images/nginx/pus%68", true},
		{"encoded slash in the prefix position streams", http.MethodGet, "/v1.47%2Fcontainers/abc/stats", true},
		{"encoded suffix word streams", http.MethodGet, "/v1.47/containers/abc/%6Cogs", true},
		{"encoded exec start streams", http.MethodPost, "/v1.47/exec/abc/%73tart", true},
		{"encoded slash in a stats id is decoded to a non-route", http.MethodGet, "/v1.47/containers/a%2Fb/stats", false},
		{"double-encoded letters are decoded once and stay a non-route", http.MethodGet, "/v1.47/containers/abc/%2573tats", false},
		{"invalid escape is classified as written", http.MethodGet, "/v1.47/containers/abc/stats%zz", false},
		{"invalid escape does not hide a suffix", http.MethodGet, "/v1.47/containers/abc%zz/logs", true},
		{"encoded question mark stays in the path", http.MethodGet, "/v1.47/containers/abc/stats%3Fx", false},
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

func TestIsStreamingPathDecodesAndDropsQuery(t *testing.T) {
	t.Parallel()

	for path, want := range map[string]bool{
		"/v1.47.0/containers/abc/%73tats?stream=1": true,
		"/v1/images/nginx/push":                    true,
		"/v1.47/containers/abc/%6Cogs?follow=1":    true,
		"/v1.47/containers/abc/json?x=/logs":       false,
	} {
		if got := IsStreamingPath(path); got != want {
			t.Errorf("IsStreamingPath(%q) = %v, want %v", path, got, want)
		}
	}
}
