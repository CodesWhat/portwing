package server

import (
	"testing"
)

// FuzzParseTrustedProxies verifies that ParseTrustedProxies never panics on
// arbitrary CIDR/IP input and that successfully-parsed entries are usable
// (non-nil network objects).
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

	f.Fuzz(func(t *testing.T, input string) {
		if input == "" {
			return
		}
		entries := []string{input}
		nets, err := ParseTrustedProxies(entries)
		if err != nil {
			// Parse errors are expected for most fuzz inputs.
			return
		}
		// If no error, the returned slice must be non-nil and each element valid.
		for i, n := range nets {
			if n == nil {
				t.Errorf("ParseTrustedProxies returned nil network at index %d", i)
			}
		}
	})
}
