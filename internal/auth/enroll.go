package auth

import (
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
)

// enrollRequest is the JSON body for POST /api/lookout/enroll.
type enrollRequest struct {
	EnrollmentToken string `json:"enrollment_token"`
	PublicKey       string `json:"public_key"` // base64 standard, raw 32-byte Ed25519 pubkey
}

// enrollResponse is returned on success.
type enrollResponse struct {
	KeyID   string `json:"key_id"`
	Comment string `json:"comment,omitempty"`
}

// Enroller handles one-shot Model C enrollment. It is safe for concurrent use.
type Enroller struct {
	mu             sync.Mutex
	token          string // burned after first successful use; zero-valued when burned
	authorizedFile string // path to the authorized_keys file to append to
	registry       *KeyRegistry
	burned         bool
}

// NewEnroller creates an Enroller. token is the pre-configured enrollment
// secret. authorizedFile is the path to the authorized_keys file.
// registry is the live key registry to reload after enrollment.
func NewEnroller(token, authorizedFile string, registry *KeyRegistry) *Enroller {
	return &Enroller{
		token:          token,
		authorizedFile: authorizedFile,
		registry:       registry,
	}
}

// ServeHTTP implements http.Handler. It accepts POST /api/lookout/enroll,
// validates the enrollment token, appends the public key, reloads the registry,
// and burns the token.
//
// The handler must be registered OUTSIDE the auth middleware so it is
// reachable without a prior credential.
func (e *Enroller) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req enrollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Refuse if token already burned.
	if e.burned || e.token == "" {
		slog.Warn("enrollment attempt after token burned")
		http.Error(w, "enrollment token already used", http.StatusUnauthorized)
		return
	}

	// Constant-time token comparison.
	if subtle.ConstantTimeCompare([]byte(req.EnrollmentToken), []byte(e.token)) != 1 {
		slog.Warn("enrollment failed: wrong token")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Decode and validate the public key.
	rawPub, err := base64.StdEncoding.DecodeString(req.PublicKey)
	if err != nil {
		http.Error(w, "public_key: invalid base64", http.StatusBadRequest)
		return
	}
	if len(rawPub) != 32 {
		http.Error(w, fmt.Sprintf("public_key: expected 32 bytes, got %d", len(rawPub)), http.StatusBadRequest)
		return
	}

	keyID := deriveKeyID(rawPub)
	comment := fmt.Sprintf("enrolled:%s", keyID)
	line := AuthorizedKeyLine(ed25519.PublicKey(rawPub), comment)

	// Append to the authorized_keys file.
	if err := appendKeyLine(e.authorizedFile, line); err != nil {
		slog.Error("enrollment failed: cannot write authorized_keys", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Reload the registry so the new key is immediately active.
	if err := e.registry.Load(); err != nil {
		slog.Error("enrollment: registry reload failed", "error", err)
		// Key was written; continue — next SIGHUP will pick it up.
	}

	// Burn the token: zero it in memory and set burned flag.
	for i := range []byte(e.token) {
		_ = i // zero not possible on string; set to empty string
	}
	e.token = ""
	e.burned = true

	slog.Info("enrollment successful", "key_id", keyID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(enrollResponse{
		KeyID:   keyID,
		Comment: comment,
	})
}

// appendKeyLine appends a single key line to the authorized_keys file,
// creating it (mode 0600) if it does not exist.
func appendKeyLine(path, line string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening authorized_keys for append: %w", err)
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, line)
	return err
}
