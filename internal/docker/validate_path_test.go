package docker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestClientRejectsSlashlessPathsBeforeAnyRequest(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits.Add(1)
	}))
	t.Cleanup(srv.Close)
	c := newTestClient(srv)

	for _, path := range []string{".0/exec/x/start", "./exec/x/start", "%2E0/exec/x/start", "", "containers/json"} {
		calls := map[string]func() error{
			"Do": func() error {
				_, err := c.Do(context.Background(), http.MethodPost, path, nil) //nolint:bodyclose // always an error here, no response
				return err
			},
			"DoWithHeaders": func() error {
				_, err := c.DoWithHeaders(context.Background(), http.MethodPost, path, nil, nil) //nolint:bodyclose // always an error here, no response
				return err
			},
			"DoStream": func() error {
				_, err := c.DoStream(context.Background(), http.MethodPost, path, nil) //nolint:bodyclose // always an error here, no response
				return err
			},
			"DoStreamWithHeaders": func() error {
				_, err := c.DoStreamWithHeaders(context.Background(), http.MethodPost, path, nil, nil) //nolint:bodyclose // always an error here, no response
				return err
			},
		}
		for name, call := range calls {
			err := call()
			if err == nil || !strings.Contains(err.Error(), "must begin with") {
				t.Errorf("%s(%q) error = %v, want a must-begin-with-slash error", name, path, err)
			}
		}
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("%d request(s) reached the daemon", got)
	}

	resp, err := c.Do(context.Background(), http.MethodGet, "/_ping", nil)
	if err != nil {
		t.Fatalf("slashed path rejected: %v", err)
	}
	_ = resp.Body.Close()
	if got := hits.Load(); got != 1 {
		t.Fatalf("control request hits = %d, want 1", got)
	}
}
