package config

// config_mutant_test.go adds tests that target Gremlins mutants surviving in
// config.go: conditions existing tests reached but only checked loosely, by
// asserting "some error" where two different refusals are possible, or by
// accepting any non-empty result.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The three refusals Load can give for an edge-mode credential set. They all
// mention PRIVATE_KEY_FILE, so a test has to match on the part that differs.
const (
	refusalTokenHashAlone  = "requires TOKEN or AUTHORIZED_KEYS, not TOKEN_HASH alone"
	refusalNoCredentials   = "requires TOKEN, AUTHORIZED_KEYS, or PRIVATE_KEY_FILE"
	refusalNeedsPrivateKey = "requires PRIVATE_KEY_FILE for Ed25519 authentication"
)

// setEdgeCredentialEnv puts Load in edge mode with exactly the credentials in
// kv. It blanks every variable Load reads a credential from first, so a case
// states its whole credential set and nothing inherited from the environment
// the tests run in can widen it, and pins the bind to loopback so an accepted
// set is not then refused for an unrelated reason.
func setEdgeCredentialEnv(t *testing.T, kv ...string) {
	t.Helper()

	for _, name := range []string{
		"TOKEN", "DD_AGENT_SECRET", "TOKEN_FILE", "DD_AGENT_SECRET_FILE",
		"TOKEN_HASH", "TOKEN_HASH_FILE",
		"AUTHORIZED_KEYS", "AUTHORIZED_KEYS_FILE",
		"PRIVATE_KEY_FILE",
	} {
		t.Setenv(name, "")
	}
	setEnv(t, "DRYDOCK_URL", "https://drydock.example.com", "BIND_ADDRESS", "127.0.0.1")
	setEnv(t, kv...)
}

// TestLoadEdgeModeCredentialRefusals pins which refusal each edge-mode
// credential set gets, or that it gets none. It kills the
// CONDITIONALS_NEGATION mutants on the two four-way guards in Load:
//
//   - config.go:133:31 and :133:78 (the TOKEN_HASH-alone guard). With either
//     comparison negated the guard never fires for a hash-only agent that has
//     a private key, and Load returns a config instead of refusing it.
//   - config.go:136:31, :136:50 and :136:78 (the no-credentials guard). With
//     any of them negated the guard stops firing for an agent with no
//     credentials at all, which is then refused by the later private-key check
//     with the wrong advice; negating the first or third also makes it fire
//     for an agent that does have a TOKEN or AUTHORIZED_KEYS.
//
// The existing tests assert only that an error mentions PRIVATE_KEY_FILE,
// which every one of these refusals does.
func TestLoadEdgeModeCredentialRefusals(t *testing.T) {
	const (
		phc        = "$argon2id$v=19$m=19456,t=2,p=1$c29tZXNhbHQ$aGFzaGhhc2g"
		privateKey = "/etc/portwing/agent.key"
	)

	for _, tt := range []struct {
		name string
		env  []string
		want string // "" means Load must succeed
	}{
		{
			name: "hash alone is refused even with a private key",
			env:  []string{"TOKEN_HASH", phc, "PRIVATE_KEY_FILE", privateKey},
			want: refusalTokenHashAlone,
		},
		{
			name: "hash alone is refused without a private key",
			env:  []string{"TOKEN_HASH", phc},
			want: refusalTokenHashAlone,
		},
		{
			name: "hash with authorized keys and a private key is accepted",
			env:  []string{"TOKEN_HASH", phc, "AUTHORIZED_KEYS", "/etc/portwing/authorized_keys", "PRIVATE_KEY_FILE", privateKey},
		},
		{
			name: "no credentials at all",
			want: refusalNoCredentials,
		},
		{
			name: "token without a private key",
			env:  []string{"TOKEN", "rawtoken"},
			want: refusalNeedsPrivateKey,
		},
		{
			name: "authorized keys without a private key",
			env:  []string{"AUTHORIZED_KEYS", "/etc/portwing/authorized_keys"},
			want: refusalNeedsPrivateKey,
		},
		{
			name: "private key alone is accepted",
			env:  []string{"PRIVATE_KEY_FILE", privateKey},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setEdgeCredentialEnv(t, tt.env...)

			cfg, err := Load()
			if tt.want == "" {
				if err != nil {
					t.Fatalf("Load refused a valid credential set: %v", err)
				}
				if cfg.DrydockURL == "" {
					t.Fatal("Load returned a config that is not in edge mode")
				}
				return
			}
			if err == nil {
				t.Fatalf("Load accepted the credential set, want a refusal containing %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load refused with %q, want a refusal containing %q", err, tt.want)
			}
		})
	}
}

// TestLoadAgentNameDefaultsToTheHostname kills the CONDITIONALS_NEGATION
// mutant at config.go:175:10. With AGENT_NAME unset the agent is named after
// the host, and "portwing" is only the fallback for a host whose name can't be
// read; negated, every host that does have a name is called "portwing".
func TestLoadAgentNameDefaultsToTheHostname(t *testing.T) {
	hostname, err := os.Hostname()
	if err != nil {
		t.Skipf("os.Hostname fails on this host, so only the fallback is reachable: %v", err)
	}
	if hostname == "portwing" {
		t.Skip("this host is literally named portwing, so the fallback and the hostname are indistinguishable")
	}

	setEnv(t, "AGENT_NAME", "", "DRYDOCK_URL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AgentName != hostname {
		t.Fatalf("AgentName = %q, want the hostname %q", cfg.AgentName, hostname)
	}
}

// TestListenAddressStripsAnEmptyBracketPair pins the `len(host) >= 2` bound in
// ListenAddress at exactly two, killing the CONDITIONALS_BOUNDARY mutant at
// config.go:322:15. "[]" is the shortest host that is all brackets: stripped,
// it is the empty host and joins as the wildcard ":3000"; with `>` it is left
// alone and joins as "[]:3000", which net.Listen rejects.
func TestListenAddressStripsAnEmptyBracketPair(t *testing.T) {
	t.Parallel()

	if got, want := ListenAddress("[]", "3000"), ":3000"; got != want {
		t.Fatalf("ListenAddress(%q, %q) = %q, want %q", "[]", "3000", got, want)
	}
}

// TestDetectDockerSocketReturnsTheFirstCandidateThatExists pins the exact
// result of detectDockerSocket for a HOME that holds a Docker Desktop socket,
// on a host with or without a system socket.
//
// It kills the CONDITIONALS_NEGATION mutant at config.go:375:35 everywhere:
// with `err == nil` negated the function returns the first candidate that is
// missing, which is never the one wanted here. The existing test accepts any
// non-empty path once a system socket exists, which is the case on a CI
// runner, and the missing candidate is non-empty too.
//
// It kills the one at config.go:364:10 (`home != ""`) only where no system
// socket exists. /var/run/docker.sock is the first candidate and is not
// HOME-derived, so on a host that has it the HOME-derived candidates are never
// reached and dropping them changes nothing: there the mutant is equivalent.
func TestDetectDockerSocketReturnsTheFirstCandidateThatExists(t *testing.T) {
	home := t.TempDir()
	desktopSocket := filepath.Join(home, ".docker", "run", "docker.sock")
	if err := os.MkdirAll(filepath.Dir(desktopSocket), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(desktopSocket, nil, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("HOME", home)

	// /var/run/docker.sock is listed ahead of the HOME-derived candidates, so
	// it wins wherever it exists. /run/docker.sock is listed after them.
	want := desktopSocket
	if _, err := os.Stat("/var/run/docker.sock"); err == nil {
		want = "/var/run/docker.sock"
	}

	if got := detectDockerSocket(); got != want {
		t.Fatalf("detectDockerSocket() = %q, want %q", got, want)
	}
}
