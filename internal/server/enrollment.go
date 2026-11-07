// SPDX-License-Identifier: MIT

package server

import (
	"net"
	"net/http"
	"strings"
)

func (s *Server) enrollmentTransport(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && !s.cfg.AllowInsecureEnrollment && !s.secureEnrollment(r) {
			s.auditor.Enrollment(s.rateLimiter.clientIP(r), "", "denied")
			rejectUnadmitted(w, r, "enrollment requires TLS or HTTPS reported by a trusted proxy", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) secureEnrollment(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	remote := r.RemoteAddr
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	// Trust the direct peer, never an address supplied in a forwarding header.
	if !ipInNets(net.ParseIP(remote), s.rateLimiter.trustedProxies) {
		return false
	}
	proto := r.Header.Values("X-Forwarded-Proto")
	return len(proto) == 1 && strings.EqualFold(strings.TrimSpace(proto[0]), "https")
}
