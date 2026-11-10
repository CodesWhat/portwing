// SPDX-License-Identifier: MIT

package log

import (
	"errors"
	"fmt"
	"net/url"
	"testing"
)

func TestRedactURL(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, raw, want string }{
		{"credentials", "https://user:secret@controller.example/base?credential=secret#secret", "https://controller.example/base"},
		{"plain", "ws://controller.example:4100/base", "ws://controller.example:4100/base"},
		{"ipv6", "wss://user:secret@[::1]:4100/base?", "wss://[::1]:4100/base"},
		{"encoded path", "https://controller.example/a%2Fb?q=secret", "https://controller.example/a%2Fb"},
		{"invalid", "https://user:secret@host/%zz", "[invalid URL]"},
		{"missing host", "secret", "[invalid URL]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := RedactURL(tc.raw); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSanitizeURLError(t *testing.T) {
	t.Parallel()
	raw := "https://user:secret@controller.example/base?credential=secret#secret"
	urlErr := &url.Error{Op: "Get", URL: raw, Err: errors.New("connection refused")}
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"ordinary", errors.New("one\ntwo\r"), `one\ntwo\r`},
		{"URL", urlErr, `Get "https://controller.example/base": connection refused`},
		{"wrapped", fmt.Errorf("connect: %w", urlErr), `connect: Get "https://controller.example/base": connection refused`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SanitizeURLError(tc.err); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
	if urlErr.URL != raw {
		t.Fatal("sanitizing the error mutated its connection URL")
	}
}
