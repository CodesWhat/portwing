package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

type Config struct {
	// Connection
	DrydockURL     string
	Token          string
	TokenHash      string
	CACert         string
	TLSSkipVerify  bool
	Port           string
	BindAddress    string
	TLSCert        string
	TLSKey         string
	TrustedProxies []string

	// Docker
	DockerSocket string
	StacksDir    string

	// Identity
	AgentID   string
	AgentName string

	// Operational
	HeartbeatInterval int
	RequestTimeout    int
	ReconnectDelay    int
	MaxReconnectDelay int
	WelcomeTimeout    int
	LogLevel          string
	SkipDFCollection  bool

	// Adapter
	Adapter string

	// Drydock compat
	DDPollInterval int

	// Audit
	AuditLog        string
	AuditBufferSize int

	// Ed25519 key authentication (Model B: operator-provisioned authorized_keys)
	AuthorizedKeysFile  string // AUTHORIZED_KEYS / AUTHORIZED_KEYS_FILE
	MaxClockSkewSeconds int    // MAX_CLOCK_SKEW_SECONDS (default 60)
	NonceLRUSize        int    // NONCE_LRU_SIZE (default 10000)

	// Model C enrollment token (consumed on first use)
	EnrollmentToken string // ENROLLMENT_TOKEN / ENROLLMENT_TOKEN_FILE

	// Edge-mode private key for signed hello (PRIVATE_KEY_FILE)
	PrivateKeyFile string
}

func Load() (*Config, error) {
	token := getEnv("TOKEN", "")
	if token == "" {
		token = getEnv("DD_AGENT_SECRET", "")
	}

	// Support TOKEN_FILE / DD_AGENT_SECRET_FILE
	if tokenFile := getEnv("TOKEN_FILE", ""); tokenFile != "" {
		t, err := loadTokenFile(tokenFile)
		if err != nil {
			return nil, fmt.Errorf("reading TOKEN_FILE: %w", err)
		}
		token = t
	} else if tokenFile := getEnv("DD_AGENT_SECRET_FILE", ""); tokenFile != "" {
		t, err := loadTokenFile(tokenFile)
		if err != nil {
			return nil, fmt.Errorf("reading DD_AGENT_SECRET_FILE: %w", err)
		}
		token = t
	}

	// Support TOKEN_HASH / TOKEN_HASH_FILE
	tokenHash := getEnv("TOKEN_HASH", "")
	if tokenHashFile := getEnv("TOKEN_HASH_FILE", ""); tokenHashFile != "" {
		h, err := loadTokenFile(tokenHashFile)
		if err != nil {
			return nil, fmt.Errorf("reading TOKEN_HASH_FILE: %w", err)
		}
		tokenHash = h
	}

	if token != "" && tokenHash != "" {
		return nil, fmt.Errorf("TOKEN and TOKEN_HASH are mutually exclusive: choose one")
	}

	// Support AUTHORIZED_KEYS / AUTHORIZED_KEYS_FILE (aliases).
	authorizedKeysFile := getEnv("AUTHORIZED_KEYS", "")
	if authorizedKeysFile == "" {
		authorizedKeysFile = getEnv("AUTHORIZED_KEYS_FILE", "")
	}

	// Support ENROLLMENT_TOKEN / ENROLLMENT_TOKEN_FILE.
	enrollmentToken := getEnv("ENROLLMENT_TOKEN", "")
	if enrollmentToken == "" {
		if etFile := getEnv("ENROLLMENT_TOKEN_FILE", ""); etFile != "" {
			t, err := loadTokenFile(etFile)
			if err != nil {
				return nil, fmt.Errorf("reading ENROLLMENT_TOKEN_FILE: %w", err)
			}
			enrollmentToken = t
		}
	}

	drydockURL := getEnv("DRYDOCK_URL", "")
	if drydockURL != "" && token == "" && tokenHash != "" && authorizedKeysFile == "" {
		return nil, fmt.Errorf("edge mode (DRYDOCK_URL) requires TOKEN or AUTHORIZED_KEYS, not TOKEN_HASH alone")
	}
	if drydockURL != "" && token == "" && tokenHash == "" && authorizedKeysFile == "" {
		privateKeyFile := getEnv("PRIVATE_KEY_FILE", "")
		if privateKeyFile == "" {
			return nil, fmt.Errorf("edge mode (DRYDOCK_URL) requires TOKEN, AUTHORIZED_KEYS, or PRIVATE_KEY_FILE")
		}
	}
	// Drydock categorically rejects token-only agents (ed25519-required); a
	// PRIVATE_KEY_FILE must always be present in edge mode so startup fails fast
	// with a clear message rather than looping forever on a rejected hello.
	if drydockURL != "" && getEnv("PRIVATE_KEY_FILE", "") == "" {
		return nil, fmt.Errorf("edge mode (DRYDOCK_URL) requires PRIVATE_KEY_FILE for Ed25519 authentication; drydock rejects token-only agents")
	}

	agentID := getEnv("AGENT_ID", "")
	if agentID == "" {
		agentID = uuid.New().String()
	}

	agentName := getEnv("AGENT_NAME", "")
	if agentName == "" {
		hostname, err := os.Hostname()
		if err != nil {
			agentName = "portwing"
		} else {
			agentName = hostname
		}
	}

	dockerSocket := getEnv("DOCKER_SOCKET", "")
	if dockerSocket == "" {
		dockerSocket = detectDockerSocket()
	}

	cfg := &Config{
		DrydockURL:     drydockURL,
		Token:          token,
		TokenHash:      tokenHash,
		CACert:         getEnv("CA_CERT", ""),
		TLSSkipVerify:  getEnvBool("TLS_SKIP_VERIFY", false),
		Port:           getEnv("PORT", "3000"),
		BindAddress:    getEnv("BIND_ADDRESS", "0.0.0.0"),
		TLSCert:        getEnv("TLS_CERT", ""),
		TLSKey:         getEnv("TLS_KEY", ""),
		TrustedProxies: splitCSV(getEnv("TRUSTED_PROXIES", "")),

		DockerSocket: dockerSocket,
		StacksDir:    getEnv("STACKS_DIR", "/data/stacks"),

		AgentID:   agentID,
		AgentName: agentName,

		HeartbeatInterval: getEnvInt("HEARTBEAT_INTERVAL", 30),
		RequestTimeout:    getEnvInt("REQUEST_TIMEOUT", 30),
		ReconnectDelay:    getEnvInt("RECONNECT_DELAY", 1),
		MaxReconnectDelay: getEnvInt("MAX_RECONNECT_DELAY", 60),
		WelcomeTimeout:    getEnvInt("WELCOME_TIMEOUT", 30),
		LogLevel:          getEnv("LOG_LEVEL", "info"),
		SkipDFCollection:  getEnvBool("SKIP_DF_COLLECTION", false),

		Adapter: getEnv("ADAPTER", "drydock"),

		DDPollInterval: getEnvInt("DD_POLL_INTERVAL", 300),

		AuditLog:        getEnv("AUDIT_LOG", ""),
		AuditBufferSize: getEnvInt("AUDIT_BUFFER_SIZE", 256),

		AuthorizedKeysFile:  authorizedKeysFile,
		MaxClockSkewSeconds: getEnvInt("MAX_CLOCK_SKEW_SECONDS", 60),
		NonceLRUSize:        getEnvInt("NONCE_LRU_SIZE", 10000),

		EnrollmentToken: enrollmentToken,

		PrivateKeyFile: getEnv("PRIVATE_KEY_FILE", ""),
	}

	return cfg, nil
}

func (c *Config) IsEdgeMode() bool {
	if c.DrydockURL == "" {
		return false
	}
	return c.Token != "" || c.AuthorizedKeysFile != "" || c.PrivateKeyFile != ""
}

func detectDockerSocket() string {
	candidates := []string{
		"/var/run/docker.sock",
	}

	home := os.Getenv("HOME")
	if home != "" {
		candidates = append(candidates,
			home+"/.docker/run/docker.sock",
			home+"/.orbstack/run/docker.sock",
		)
	}

	candidates = append(candidates, "/run/docker.sock")

	for _, path := range candidates {
		// #nosec G703 -- candidates are fixed well-known Docker socket paths plus HOME-derived local Docker paths.
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	return "/var/run/docker.sock"
}

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	val := os.Getenv(key)
	if val == "" {
		return fallback
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return fallback
	}
	return n
}

func getEnvBool(key string, fallback bool) bool {
	val := os.Getenv(key)
	if val == "" {
		return fallback
	}
	switch strings.ToLower(val) {
	case "1", "true", "yes":
		return true
	case "0", "false", "no":
		return false
	default:
		return fallback
	}
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func loadTokenFile(path string) (string, error) {
	// #nosec G304 -- token file path is explicit operator configuration.
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
