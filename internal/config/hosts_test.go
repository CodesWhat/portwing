package config

import (
	"strings"
	"testing"
)

func TestParseHostAllowlist(t *testing.T) {
	t.Parallel()

	got, err := ParseHostAllowlist([]string{"Ops.Example.COM", "api.example.com.", "a0z9.example"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.allowed) != 3 {
		t.Fatalf("allowed = %v", got.allowed)
	}
	for _, host := range []string{"ops.example.com", "api.example.com", "a0z9.example"} {
		if !got.contains(host) {
			t.Errorf("missing %q in %v", host, got.allowed)
		}
	}
	if empty, err := ParseHostAllowlist(nil); err != nil || empty.contains("x") {
		t.Fatalf("empty list = %v, %v", empty, err)
	}

	invalid := []string{
		"",
		".",
		"*",
		"*.example.com",
		"https://ops.example.com",
		"ops.example.com:3000",
		"ops.example.com:",
		"[::1]",
		"::1",
		"ops.example.com/path",
		"ops.example.com?x=1",
		"ops.example.com#x",
		"user@ops.example.com",
		"ops example.com",
		"ops`.example.com",
		"ops{.example.com",
		"opsé.example.com",
	}
	for _, entry := range invalid {
		_, err := ParseHostAllowlist([]string{entry})
		if err == nil {
			t.Errorf("ParseHostAllowlist(%q) succeeded, want error", entry)
			continue
		}
		if !strings.Contains(err.Error(), "invalid host") {
			t.Errorf("error %q does not name the problem", err)
		}
	}
}

func TestHostAdmits(t *testing.T) {
	t.Parallel()

	allow, err := ParseHostAllowlist([]string{"ops.example.com"})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		host string
		want bool
	}{
		// IP literals, with and without a port.
		{"127.0.0.1", true},
		{"127.0.0.1:3000", true},
		{"10.20.30.40:3000", true},
		{"[::1]:3000", true},
		{"[::1]", true},
		{"[2001:db8::1]:3000", true},
		{"[fe80::1%eth0]:3000", true},
		// localhost, case and trailing dot.
		{"localhost", true},
		{"localhost:3000", true},
		{"localhost:9", true},
		{"localhost:3009", true},
		{"10.0.0.9", true},
		{"192.168.1.9:3000", true},
		{"Z", true},
		{"PORTWINZ:3000", true},
		{"LOCALHOST:3000", true},
		{"localhost.:3000", true},
		// Single-label names: Compose service names.
		{"portwing", true},
		{"portwing:3000", true},
		{"Portwing-Agent_1:3000", true},
		{"portwing.", true},
		{"a", true},
		{"z", true},
		{"0", true},
		{"9", true},
		{strings.Repeat("a", 63), true},
		// ALLOWED_HOSTS: case, port and trailing-dot variants.
		{"ops.example.com", true},
		{"OPS.Example.COM", true},
		{"ops.example.com:3000", true},
		{"ops.example.com.", true},
		{"ops.example.com.:3000", true},
		// Rebinding lookalikes and junk.
		{"rebind.attacker.example:3000", false},
		{"attacker.example", false},
		{"attacker.example.", false},
		{"localhost.attacker.example", false},
		{"127.0.0.1.attacker.example", false},
		{"127.0.0.1.attacker.example:3000", false},
		{"ops.example.com.attacker.example", false},
		{"xops.example.com", false},
		{"ops.example.com@attacker.example", false},
		{"user@localhost", false},
		{"localhost@attacker.example", false},
		{"localhost/path", false},
		{"localhost?x", false},
		{"attacker.example/localhost", false},
		{"", false},
		{".", false},
		{":3000", false},
		{"[]:3000", false},
		{"[::1", false},
		{"[::1]x", false},
		{"[::1]:port", false},
		{"[::1]:1234567", false},
		{"::1", false},
		{"localhost:80x", false},
		{"localhost:1234567", false},
		{"127.1", false},
		{"0x7f.1", false},
		{"2130706433", true}, // all digits is one valid label, not a dotted name
		{strings.Repeat("a", 64), false},
		{"local host", false},
		{"local`host", false},
		{"local{host", false},
		{"local/host", false},
		{"local:host", false},
	}
	for _, tc := range cases {
		if got := allow.Admits(tc.host); got != tc.want {
			t.Errorf("Admits(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestHostAdmitsNilAllowlistKeepsBuiltInRules(t *testing.T) {
	t.Parallel()

	var allow *HostAllowlist
	if !allow.Admits("localhost:3000") || !allow.Admits("portwing") || !allow.Admits("[::1]:3000") {
		t.Fatal("built-in hosts rejected by a nil allowlist")
	}
	if allow.Admits("ops.example.com") {
		t.Fatal("dotted name admitted by a nil allowlist")
	}
}

func TestRequestHost(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"Example.COM:80": "Example.COM",
		"example.com.":   "example.com",
		"[::1]:3000":     "::1",
		"[::1]":          "::1",
		"127.0.0.1:0":    "127.0.0.1",
		"host:":          "host",
		"host:65535":     "host",
	}
	for in, want := range cases {
		got, ok := requestHost(in)
		if !ok || got != want {
			t.Errorf("requestHost(%q) = %q, %v, want %q", in, got, ok, want)
		}
	}
}
