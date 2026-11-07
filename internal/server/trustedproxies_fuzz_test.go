// SPDX-License-Identifier: MIT

package server

import (
	"bytes"
	"net"
	"strings"
	"testing"
)

// FuzzParseTrustedProxies verifies that ParseTrustedProxies never panics on
// arbitrary CIDR/IP input and preserves the configured trust boundary.
func FuzzParseTrustedProxies(f *testing.F) {
	// Seed: valid CIDR strings and bare IPs.
	f.Add("127.0.0.1")
	f.Add("10.0.0.0/8")
	f.Add("192.168.1.0/24")
	f.Add("::1")
	f.Add("fd00::/8")
	f.Add("172.16.0.0/12")
	// Seed: hostile inputs.
	f.Add("")
	f.Add("not-an-ip")
	f.Add("999.999.999.999")
	f.Add("10.0.0.0/99")
	f.Add("0.0.0.0/0")
	f.Add("::/0")
	f.Add("256.0.0.1/24")
	f.Add(" \t\n")
	f.Add(" 127.0.0.1 ")
	f.Add("::ffff:192.0.2.1")

	f.Fuzz(func(t *testing.T, input string) {
		entries := []string{input}
		nets, err := ParseTrustedProxies(entries)
		trimmed := strings.TrimSpace(input)
		if trimmed == "" {
			if err != nil || len(nets) != 0 {
				t.Fatalf("empty proxy entry: %d networks, error %v", len(nets), err)
			}
			return
		}
		var want *net.IPNet
		if ip := net.ParseIP(trimmed); ip != nil {
			bits := 128
			if ip.To4() != nil {
				bits = 32
			}
			want = &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}
		} else {
			_, want, _ = net.ParseCIDR(trimmed)
		}
		if want == nil {
			if err == nil {
				t.Fatal("invalid proxy entry accepted")
			}
			return
		}
		if err != nil {
			t.Fatalf("valid proxy entry rejected: %v", err)
		}
		if len(nets) != 1 || nets[0] == nil {
			t.Fatalf("valid proxy entry returned %d networks, want one nonnil network", len(nets))
		}
		if !nets[0].IP.Equal(want.IP) || !bytes.Equal(nets[0].Mask, want.Mask) {
			t.Fatalf("proxy trust boundary = %v, want %v", nets[0], want)
		}
	})
}
