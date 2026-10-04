package docker

import (
	"io"
	"net/http"
	"sync"
	"testing"
)

// redirectingDaemon answers every request the way the daemon's router answers
// an unclean path: a 301 to the cleaned path. It records every request line.
func redirectingDaemon(t *testing.T) (socket string, seen func() []string) {
	t.Helper()
	var mu sync.Mutex
	var uris []string
	socket = startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			_, _ = io.WriteString(w, `{"Version":"26.0.0","ApiVersion":"1.44"}`)
			return
		}
		mu.Lock()
		uris = append(uris, r.RequestURI)
		mu.Unlock()
		if r.URL.Path == "/containers/abc/stats" {
			_, _ = io.WriteString(w, "streamed")
			return
		}
		http.Redirect(w, r, "/containers/abc/stats", http.StatusMovedPermanently)
	}))
	return socket, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), uris...)
	}
}

// A redirect from the daemon goes back to the caller on every request path,
// and is never followed: the cleaned request would run after the proxy already
// classified the unclean one.
func TestClientReturnsDaemonRedirectWithoutFollowing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		do   func(c *Client) (*http.Response, error)
	}{
		{"Do", func(c *Client) (*http.Response, error) {
			return c.Do(t.Context(), http.MethodGet, "/containers/abc//stats", nil)
		}},
		{"DoStream", func(c *Client) (*http.Response, error) {
			return c.DoStream(t.Context(), http.MethodGet, "/containers/abc//stats", nil)
		}},
		{"DoRaw", func(c *Client) (*http.Response, error) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost/containers/abc//stats", nil)
			if err != nil {
				t.Fatal(err)
			}
			return c.DoRaw(req)
		}},
		{"DoStreamRaw", func(c *Client) (*http.Response, error) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost/containers/abc//stats", nil)
			if err != nil {
				t.Fatal(err)
			}
			return c.DoStreamRaw(req)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			socket, seen := redirectingDaemon(t)
			c, err := NewClient(socket, 5)
			if err != nil {
				t.Fatal(err)
			}

			resp, err := tc.do(c)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusMovedPermanently {
				t.Fatalf("status = %d, want the daemon's 301 returned to the caller", resp.StatusCode)
			}
			if got := seen(); len(got) != 1 {
				t.Fatalf("daemon saw %v, want exactly the one unclean request", got)
			}
		})
	}
}

// A '#' in the query is query text on the wire. Parsing the outbound URL as a
// string would read it as a fragment and drop the rest.
func TestClientKeepsHashInQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		do   func(c *Client, path string) (*http.Response, error)
	}{
		{"Do", func(c *Client, path string) (*http.Response, error) {
			return c.Do(t.Context(), http.MethodGet, path, nil)
		}},
		{"DoStreamWithHeaders", func(c *Client, path string) (*http.Response, error) {
			return c.DoStreamWithHeaders(t.Context(), http.MethodGet, path, nil, nil)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var got string
			socket := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/version" {
					_, _ = io.WriteString(w, `{"Version":"26.0.0","ApiVersion":"1.44"}`)
					return
				}
				mu.Lock()
				got = r.RequestURI
				mu.Unlock()
			}))
			c, err := NewClient(socket, 5)
			if err != nil {
				t.Fatal(err)
			}

			resp, err := tc.do(c, "/containers/abc/stats?a=b#&stream=0")
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()

			mu.Lock()
			defer mu.Unlock()
			if want := "/v1.44/containers/abc/stats?a=b#&stream=0"; got != want {
				t.Fatalf("daemon saw %q, want %q", got, want)
			}
		})
	}
}
