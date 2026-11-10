// SPDX-License-Identifier: MIT

package log

import (
	"errors"
	"net/url"
	"strings"
)

// RedactURL removes credential-bearing components from diagnostic addresses.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "[invalid URL]"
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	u.RawFragment = ""
	return Sanitize(u.String())
}

// SanitizeURLError retains error context without exposing the failing URL's credentials.
func SanitizeURLError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		safe := *urlErr
		safe.URL = RedactURL(urlErr.URL)
		message = strings.ReplaceAll(message, urlErr.Error(), safe.Error())
	}
	return Sanitize(message)
}
