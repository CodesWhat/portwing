package docker

import (
	"testing"
)

// newTestComposeManager creates a ComposeManager with a temp stacksDir for
// validation tests. It mirrors the approach in compose_test.go (direct struct
// literal) — the compose binary detection is irrelevant for validateRequest.
func newTestComposeManager(t *testing.T) *ComposeManager {
	t.Helper()
	return &ComposeManager{stacksDir: t.TempDir()}
}

// TestValidateRequest exercises the security-critical validation paths in
// ComposeManager.validateRequest.
func TestValidateRequest(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		req     ComposeRequest
		wantErr bool
	}{
		// ---- Baseline: a well-formed request must be accepted. ----
		{
			name: "valid request is accepted",
			req: ComposeRequest{
				StackName: "myapp",
				Services:  []string{"web", "db"},
				EnvVars: map[string]string{
					"MY_VAR":   "value",
					"MY_VAR_2": "another",
				},
				RegistryAuth: &RegistryAuth{
					Server:   "https://registry.example.com",
					Username: "user",
					Password: "pass",
				},
			},
			wantErr: false,
		},
		{
			name: "request without registry auth is accepted",
			req: ComposeRequest{
				StackName: "myapp",
			},
			wantErr: false,
		},

		// ---- Stack name required. ----
		{
			name:    "empty stack name is rejected",
			req:     ComposeRequest{},
			wantErr: true,
		},

		// ---- Env var value injection. ----
		{
			name: "env var value with newline is rejected",
			req: ComposeRequest{
				StackName: "myapp",
				EnvVars: map[string]string{
					"MY_VAR": "value\nLD_PRELOAD=/evil.so",
				},
			},
			wantErr: true,
		},
		{
			name: "env var value with carriage return is rejected",
			req: ComposeRequest{
				StackName: "myapp",
				EnvVars: map[string]string{
					"MY_VAR": "val\rue",
				},
			},
			wantErr: true,
		},
		{
			name: "env var value with null byte is rejected",
			req: ComposeRequest{
				StackName: "myapp",
				EnvVars: map[string]string{
					"MY_VAR": "val\x00ue",
				},
			},
			wantErr: true,
		},

		// ---- Env var key validation. ----
		{
			name: "env var key with leading digit fails pattern",
			req: ComposeRequest{
				StackName: "myapp",
				EnvVars: map[string]string{
					"1INVALID": "value",
				},
			},
			wantErr: true,
		},
		{
			name: "env var key with hyphen fails pattern",
			req: ComposeRequest{
				StackName: "myapp",
				EnvVars: map[string]string{
					"MY-VAR": "value",
				},
			},
			wantErr: true,
		},
		{
			name: "env var key with space fails pattern",
			req: ComposeRequest{
				StackName: "myapp",
				EnvVars: map[string]string{
					"MY VAR": "value",
				},
			},
			wantErr: true,
		},

		// ---- Env var denylist. ----
		{
			name: "denied key LD_PRELOAD is rejected",
			req: ComposeRequest{
				StackName: "myapp",
				EnvVars: map[string]string{
					"LD_PRELOAD": "/evil.so",
				},
			},
			wantErr: true,
		},
		{
			name: "denied key PATH is rejected",
			req: ComposeRequest{
				StackName: "myapp",
				EnvVars: map[string]string{
					"PATH": "/evil/bin:/usr/bin",
				},
			},
			wantErr: true,
		},
		{
			name: "denied key DOCKER_HOST is rejected",
			req: ComposeRequest{
				StackName: "myapp",
				EnvVars: map[string]string{
					"DOCKER_HOST": "tcp://attacker:2375",
				},
			},
			wantErr: true,
		},
		{
			name: "denied key LD_LIBRARY_PATH is rejected",
			req: ComposeRequest{
				StackName: "myapp",
				EnvVars: map[string]string{
					"LD_LIBRARY_PATH": "/evil/lib",
				},
			},
			wantErr: true,
		},
		{
			name: "denied key BASH_ENV is rejected",
			req: ComposeRequest{
				StackName: "myapp",
				EnvVars: map[string]string{
					"BASH_ENV": "/tmp/evil",
				},
			},
			wantErr: true,
		},

		// ---- Service name validation. ----
		{
			name: "service name beginning with hyphen is rejected",
			req: ComposeRequest{
				StackName: "myapp",
				Services:  []string{"-bad-service"},
			},
			wantErr: true,
		},
		{
			name: "service name beginning with double hyphen is rejected",
			req: ComposeRequest{
				StackName: "myapp",
				Services:  []string{"--rm"},
			},
			wantErr: true,
		},
		{
			name: "normal service name is accepted",
			req: ComposeRequest{
				StackName: "myapp",
				Services:  []string{"web", "db", "cache"},
			},
			wantErr: false,
		},

		// ---- RegistryAuth.Server validation. ----
		{
			name: "registry auth with empty server is rejected",
			req: ComposeRequest{
				StackName: "myapp",
				RegistryAuth: &RegistryAuth{
					Server:   "",
					Username: "user",
					Password: "pass",
				},
			},
			wantErr: true,
		},
		{
			name: "registry auth with http (non-https) server is rejected",
			req: ComposeRequest{
				StackName: "myapp",
				RegistryAuth: &RegistryAuth{
					Server:   "http://registry.example.com",
					Username: "user",
					Password: "pass",
				},
			},
			wantErr: true,
		},
		{
			name: "registry auth with unparseable server URI is rejected",
			req: ComposeRequest{
				StackName: "myapp",
				RegistryAuth: &RegistryAuth{
					Server:   "://not-a-uri",
					Username: "user",
					Password: "pass",
				},
			},
			wantErr: true,
		},
		{
			name: "registry auth with valid https server is accepted",
			req: ComposeRequest{
				StackName: "myapp",
				RegistryAuth: &RegistryAuth{
					Server:   "https://registry.example.com",
					Username: "user",
					Password: "pass",
				},
			},
			wantErr: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cm := newTestComposeManager(t)
			err := cm.validateRequest(tc.req)

			if tc.wantErr && err == nil {
				t.Errorf("validateRequest(): expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validateRequest(): unexpected error: %v", err)
			}
		})
	}
}
