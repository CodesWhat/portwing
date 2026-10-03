package edge

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestProxyFuncRejectsUnsupportedSchemes(t *testing.T) {
	t.Parallel()
	req, err := http.NewRequest(http.MethodGet, "https://controller.example/api/portwing/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		proxy   string
		wantErr bool
	}{
		{"http://proxy.example:3128", false},
		{"http://user:pass@proxy.example:3128", false},
		{"socks5://proxy.example:1080", false},
		{"https://proxy.example:3128", true},
		{"socks4://proxy.example:1080", true},
		{"ftp://proxy.example", true},
	}
	for _, tc := range cases {
		t.Run(tc.proxy, func(t *testing.T) {
			t.Parallel()
			want, err := url.Parse(tc.proxy)
			if err != nil {
				t.Fatal(err)
			}
			c := &Client{proxy: func(*http.Request) (*url.URL, error) { return want, nil }}
			got, err := c.proxyFunc()(req)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("proxy %s: want an error, got none", tc.proxy)
				}
				if !strings.Contains(err.Error(), "use an http:// or socks5:// proxy URL") {
					t.Fatalf("proxy %s: error %q does not say what to change", tc.proxy, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("proxy %s: unexpected error %v", tc.proxy, err)
			}
			if got != want {
				t.Fatalf("proxy %s: got %v, want %v", tc.proxy, got, want)
			}
		})
	}
}

func TestProxyFuncPassesThroughDirectAndSelectorErrors(t *testing.T) {
	t.Parallel()
	req, err := http.NewRequest(http.MethodGet, "https://controller.example/api/portwing/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	direct := &Client{proxy: func(*http.Request) (*url.URL, error) { return nil, nil }}
	if got, err := direct.proxyFunc()(req); got != nil || err != nil {
		t.Fatalf("direct: got (%v, %v), want (nil, nil)", got, err)
	}
	boom := errors.New("bad proxy env")
	failing := &Client{proxy: func(*http.Request) (*url.URL, error) { return nil, boom }}
	if _, err := failing.proxyFunc()(req); !errors.Is(err, boom) {
		t.Fatalf("selector error: got %v, want %v", err, boom)
	}
}
