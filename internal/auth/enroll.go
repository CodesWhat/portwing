package auth

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
)

const maxEnrollmentBodyBytes int64 = 64 * 1024

// enrollRequest is the JSON body for POST /api/portwing/enroll.
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
	tokenDigest    [sha256.Size]byte
	tokenAvailable bool
	authorizedFile string // path to the authorized_keys file to append to
	registry       *KeyRegistry
	burned         bool

	// OnResult, when non-nil, is invoked after every enrollment attempt with
	// the client address, the derived key ID ("" when unavailable), and the
	// outcome ("allowed" or "denied"). Used for audit logging.
	OnResult func(actor, keyID, outcome string)
}

// NewEnroller creates an Enroller. token is the pre-configured enrollment
// secret. authorizedFile is the path to the authorized_keys file.
// registry is the live key registry to reload after enrollment.
func NewEnroller(token, authorizedFile string, registry *KeyRegistry) *Enroller {
	return &Enroller{
		tokenDigest:    sha256.Sum256([]byte(token)),
		tokenAvailable: token != "",
		authorizedFile: authorizedFile,
		registry:       registry,
	}
}

// ServeHTTP implements http.Handler. It accepts POST /api/portwing/enroll,
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

	r.Body = http.MaxBytesReader(w, r.Body, maxEnrollmentBodyBytes)
	decoder := json.NewDecoder(r.Body)
	var req enrollRequest
	if err := decoder.Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	actor := remoteHost(r)

	e.mu.Lock()
	defer e.mu.Unlock()

	// Refuse if token already burned.
	if e.burned || !e.tokenAvailable {
		slog.Warn("enrollment attempt after token burned", "actor", actor)
		e.notify(actor, "", "denied")
		http.Error(w, "enrollment token already used", http.StatusUnauthorized)
		return
	}

	// Hash both sides before the timing-safe comparison so submitted token
	// length cannot change the comparison duration.
	presentedTokenDigest := sha256.Sum256([]byte(req.EnrollmentToken))
	if subtle.ConstantTimeCompare(presentedTokenDigest[:], e.tokenDigest[:]) != 1 {
		slog.Warn("enrollment failed: wrong token", "actor", actor)
		e.notify(actor, "", "denied")
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

	// Burn the token digest after its one successful use.
	e.tokenDigest = [sha256.Size]byte{}
	e.tokenAvailable = false
	e.burned = true

	slog.Info("enrollment successful", "key_id", keyID, "actor", actor)
	e.notify(actor, keyID, "allowed")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(enrollResponse{
		KeyID:   keyID,
		Comment: comment,
	})
}

// notify invokes the OnResult callback if configured.
func (e *Enroller) notify(actor, keyID, outcome string) {
	if e.OnResult != nil {
		e.OnResult(actor, keyID, outcome)
	}
}

// remoteHost extracts the host portion of the request's remote address.
func remoteHost(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// appendKeyLine appends a single key line to the authorized_keys file,
// creating it (mode 0600) if it does not exist.
func appendKeyLine(path, line string) (err error) {
	// #nosec G304 -- authorized_keys path is explicit operator configuration.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening authorized_keys for append: %w", err)
	}
	defer func() {
		// Surface a deferred close error (e.g. a flush failure on the write)
		// so a partially written key line isn't reported as success.
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("closing authorized_keys: %w", cerr)
		}
	}()
	if _, err = fmt.Fprintln(f, line); err != nil {
		return fmt.Errorf("writing authorized_keys: %w", err)
	}
	return nil
}
