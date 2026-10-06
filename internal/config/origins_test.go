package config

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseOriginAllowlist(t *testing.T) {
	t.Parallel()

	valid := []struct {
		name    string
		entries []string
		want    int
	}{
		{"nil", nil, 0},
		{"https", []string{"https://good.example"}, 1},
		{"http with port", []string{"http://localhost:8080"}, 1},
		{"ipv4", []string{"http://127.0.0.1:3000"}, 1},
		{"ipv6", []string{"http://[::1]:3000"}, 1},
		{"mixed case", []string{"HTTPS://Good.Example"}, 1},
		{"explicit default port collapses with implicit", []string{"https://good.example", "https://good.example:443"}, 1},
		{"two origins", []string{"https://a.example", "https://b.example"}, 2},
		{"lowest port", []string{"http://good.example:1"}, 1},
		{"highest port", []string{"http://good.example:65535"}, 1},
		{"host spans a to z and 0 to 9", []string{"http://a0z9.example", "http://z.example", "http://a.example", "http://9.example", "http://0.example"}, 5},
		{"hyphen and underscore", []string{"http://my-host_1.example"}, 1},
		{"canonical ipv4", []string{"http://127.0.0.1"}, 1},
		{"canonical ipv6", []string{"http://[::1]", "http://[2001:db8::1]:3000"}, 2},
	}
	for _, tc := range valid {
		t.Run("valid/"+tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseOriginAllowlist(tc.entries)
			if err != nil {
				t.Fatalf("ParseOriginAllowlist(%q): %v", tc.entries, err)
			}
			if len(got.allowed) != tc.want {
				t.Fatalf("Len = %d, want %d", len(got.allowed), tc.want)
			}
		})
	}

	invalid := []string{
		"*",
		"",
		"null",
		"good.example",
		"//good.example",
		"https://",
		"https:good.example",
		"ftp://good.example",
		"ws://good.example",
		"https://good.example/",
		"https://good.example/path",
		"https://good.example?x=1",
		"https://good.example?",
		"https://good.example#frag",
		"https://user@good.example",
		"https://user:pw@good.example",
		"https://good.example:0",
		"https://good.example:65536",
		"https://good.example:http",
		"https://good.example:-1",
		"https://good .example",
		"https://*.good.example",
		"https://good`.example",
		"https://good{.example",
		"https://good/.example",
		"https://good@.example",
		"http://good.example:",
		"http://127.1:3000",
		"http://127.1.9",
		"http://127.0.1",
		"http://2130706433",
		"http://0x7f.0.0.1",
		"http://127.000.0.1",
		"http://[0:0:0:0:0:0:0:1]:3000",
		"http://[::FFFF:1.2.3.4]",
		"http://[2001:DB8::1]",
		"http://[::1",
	}
	for _, entry := range invalid {
		t.Run("invalid/"+entry, func(t *testing.T) {
			t.Parallel()
			_, err := ParseOriginAllowlist([]string{entry})
			if err == nil {
				t.Fatalf("ParseOriginAllowlist(%q) succeeded, want error", entry)
			}
			if !strings.Contains(err.Error(), "invalid origin") {
				t.Fatalf("error %q does not name the problem", err)
			}
		})
	}
}

func TestCanonicalOrigin(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"https://Good.Example":        "https://good.example:443",
		"HTTP://Good.Example":         "http://good.example:80",
		"https://good.example:443":    "https://good.example:443",
		"http://good.example:80":      "http://good.example:80",
		"https://good.example:80":     "https://good.example:80",
		"http://good.example:443":     "http://good.example:443",
		"http://[::1]:3000":           "http://[::1]:3000",
		"http://[::1]":                "http://[::1]:80",
		"https://good.example:008443": "https://good.example:8443",
	}
	for in, want := range cases {
		got, err := canonical(in)
		if err != nil {
			t.Fatalf("canonical(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("canonical(%q) = %q, want %q", in, got, want)
		}
	}
}

func newOriginReq(origins ...string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/containers", nil)
	r.RemoteAddr = "192.0.2.10:40000"
	for _, o := range origins {
		r.Header.Add("Origin", o)
	}
	return r
}

func TestOriginAdmits(t *testing.T) {
	t.Parallel()

	allow, err := ParseOriginAllowlist([]string{"https://good.example", "http://localhost:3000"})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		origins []string
		want    bool
	}{
		{"no header", nil, true},
		{"exact", []string{"https://good.example"}, true},
		{"case variant", []string{"HTTPS://GOOD.Example"}, true},
		{"default port explicit", []string{"https://good.example:443"}, true},
		{"non-default port allowed entry", []string{"http://localhost:3000"}, true},
		{"other origin", []string{"https://evil.test"}, false},
		{"subdomain lookalike prefix", []string{"https://good.example.evil.test"}, false},
		{"port lookalike", []string{"https://good.example:444"}, false},
		{"scheme mismatch", []string{"http://good.example"}, false},
		{"suffix lookalike", []string{"https://evilgood.example"}, false},
		{"userinfo trick", []string{"https://good.example@evil.test"}, false},
		{"trailing slash", []string{"https://good.example/"}, false},
		{"with path", []string{"https://good.example/x"}, false},
		{"null", []string{"null"}, false},
		{"garbage", []string{"%%%not an origin"}, false},
		{"empty value", []string{""}, false},
		{"duplicate identical", []string{"https://good.example", "https://good.example"}, false},
		{"duplicate one bad", []string{"https://good.example", "https://evil.test"}, false},
		{"localhost wrong port", []string{"http://localhost:3001"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, _ := allow.Admits(newOriginReq(tc.origins...))
			if got != tc.want {
				t.Fatalf("admits(%q) = %v, want %v", tc.origins, got, tc.want)
			}
		})
	}
}

func TestEmptyOriginAllowlistRejectsAnyOrigin(t *testing.T) {
	t.Parallel()

	for _, allow := range []*OriginAllowlist{nil, {}} {
		if ok, _ := allow.Admits(newOriginReq("https://good.example")); ok {
			t.Fatal("empty allowlist admitted an Origin")
		}
		if ok, _ := allow.Admits(newOriginReq()); !ok {
			t.Fatal("empty allowlist rejected a request with no Origin")
		}
	}
}
