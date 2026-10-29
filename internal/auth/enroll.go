// SPDX-License-Identifier: MIT

package auth

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxEnrollmentBodyBytes int64 = 64 * 1024

// enrollmentBodyReadDeadline bounds how long an unauthenticated enrollment
// request may occupy a handler while supplying its body. A var, not a const,
// lets tests exercise the timeout without waiting for the production value.
var enrollmentBodyReadDeadline = 10 * time.Second

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

	// ActorResolver, when non-nil, resolves the client recorded in enrollment
	// audit events. The default uses the direct peer from RemoteAddr.
	ActorResolver func(*http.Request) string
}

// NewEnroller creates an Enroller. token is the pre-configured enrollment
// secret. authorizedFile is the path to the authorized_keys file.
// registry is the live key registry to reload after enrollment.
//
// The burn is durable: a successful enrollment writes a marker file next to
// authorized_keys (see enrollmentMarkerPath), and NewEnroller stays disarmed
// when that marker matches the configured token.
func NewEnroller(token, authorizedFile string, registry *KeyRegistry) *Enroller {
	e := &Enroller{
		tokenDigest:    sha256.Sum256([]byte(token)),
		tokenAvailable: token != "",
		authorizedFile: authorizedFile,
		registry:       registry,
	}
	if token != "" && alreadyEnrolled(e.tokenDigest, authorizedFile) {
		e.tokenDigest = [sha256.Size]byte{}
		e.tokenAvailable = false
		e.burned = true
		slog.Warn("enrollment already happened for this agent; enrollment is disabled until a new token is configured, remove ENROLLMENT_TOKEN",
			"marker", enrollmentMarkerPath(authorizedFile))
	}
	return e
}

// enrollmentMarkerPath is where the durable token burn is recorded.
func enrollmentMarkerPath(authorizedFile string) string {
	return authorizedFile + ".enrolled"
}

// alreadyEnrolled reports whether the configured token has already been used.
// It fails closed: an unreadable marker counts as used.
func alreadyEnrolled(digest [sha256.Size]byte, authorizedFile string) bool {
	marker := enrollmentMarkerPath(authorizedFile)
	// #nosec G304 -- marker path derives from explicit operator configuration.
	data, err := os.ReadFile(marker)
	if errors.Is(err, os.ErrNotExist) {
		// No marker: builds that burned only in memory left an enrolled: key line.
		return hasEnrolledKeyLine(authorizedFile)
	}
	if err != nil {
		slog.Error("reading enrollment marker; treating token as used", "marker", marker, "error", err)
		return true
	}
	got := []byte(strings.TrimSpace(string(data)))
	if !validEnrollmentDigest(got) {
		slog.Error("enrollment marker is not a SHA-256 hex digest; treating token as used", "marker", marker)
		return true
	}
	want := []byte(hex.EncodeToString(digest[:]))
	// A different digest means the operator rotated to a new token.
	return subtle.ConstantTimeCompare(got, want) == 1
}

// validEnrollmentDigest reports whether b is exactly 64 lowercase hex chars.
func validEnrollmentDigest(b []byte) bool {
	if len(b) != hex.EncodedLen(sha256.Size) {
		return false
	}
	for _, c := range b {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// hasEnrolledKeyLine reports whether authorized_keys holds a key written by
// enrollment (comment starting with "enrolled:").
func hasEnrolledKeyLine(path string) bool {
	// #nosec G304 -- authorized_keys path is explicit operator configuration.
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && !strings.HasPrefix(fields[0], "#") && strings.HasPrefix(fields[2], "enrolled:") {
			return true
		}
	}
	return false
}

// writeEnrollmentMarker atomically records the burned token digest (temp file
// in the same directory, fsync, rename), mode 0600.
func writeEnrollmentMarker(authorizedFile string, digest [sha256.Size]byte) error {
	return writeEnrollmentMarkerContent(authorizedFile, []byte(hex.EncodeToString(digest[:])+"\n"))
}

// writeEnrollmentMarkerContent atomically installs content as the marker, then
// fsyncs the parent directory so the rename survives a crash.
func writeEnrollmentMarkerContent(authorizedFile string, content []byte) (err error) {
	marker := enrollmentMarkerPath(authorizedFile)
	tmp, err := os.CreateTemp(filepath.Dir(marker), filepath.Base(marker)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating enrollment marker: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	// os.CreateTemp already creates the file with mode 0600.
	_, writeErr := tmp.Write(content)
	if err = errors.Join(writeErr, tmp.Sync(), tmp.Close()); err != nil {
		return fmt.Errorf("writing enrollment marker: %w", err)
	}
	if err = os.Rename(tmpName, marker); err != nil {
		return fmt.Errorf("installing enrollment marker: %w", err)
	}
	if err = syncRotationStateDirectory(filepath.Dir(marker)); err != nil {
		return fmt.Errorf("syncing enrollment marker directory: %w", err)
	}
	return nil
}

// ServeHTTP implements http.Handler. It accepts POST /api/portwing/enroll,
// validates the enrollment token, appends the public key, reloads the registry,
// and burns the token.
//
// The handler must be registered OUTSIDE the auth middleware so it is
// reachable without a prior credential.
func (e *Enroller) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		rejectEnrollmentBody(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	bodyConsumed := false
	rc := http.NewResponseController(w)
	if err := rc.SetReadDeadline(time.Now().Add(enrollmentBodyReadDeadline)); err != nil {
		slog.Warn("setting enrollment body read deadline", "error", err)
	} else {
		defer func() {
			if !bodyConsumed {
				return
			}
			if err := rc.SetReadDeadline(time.Time{}); err != nil {
				slog.Warn("clearing enrollment body read deadline", "error", err)
			}
		}()
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxEnrollmentBodyBytes)
	decoder := json.NewDecoder(r.Body)
	var req enrollRequest
	if err := decoder.Decode(&req); err != nil {
		writeEnrollmentDecodeError(w, err)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeEnrollmentDecodeError(w, err)
		return
	}
	bodyConsumed = true

	actor := remoteHost(r)
	if e.ActorResolver != nil {
		actor = e.ActorResolver(r)
	}

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

	keyID := KeyIDForPublicKey(ed25519.PublicKey(rawPub))
	comment := fmt.Sprintf("enrolled:%s", keyID)
	line := AuthorizedKeyLine(ed25519.PublicKey(rawPub), comment)

	// Keep the previous marker so a failed rotated enrollment can restore it.
	// #nosec G304 -- marker path derives from explicit operator configuration.
	prevMarker, prevErr := os.ReadFile(enrollmentMarkerPath(e.authorizedFile))
	hadPrevMarker := prevErr == nil

	// Record the burn durably before the key lands, so a crash or restart
	// between the two cannot leave a usable token. Fail closed.
	if err := writeEnrollmentMarker(e.authorizedFile, e.tokenDigest); err != nil {
		slog.Error("enrollment failed: cannot write enrollment marker", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Append to the authorized_keys file.
	if err := appendKeyLine(e.authorizedFile, line); err != nil {
		if hadPrevMarker {
			if rerr := writeEnrollmentMarkerContent(e.authorizedFile, prevMarker); rerr != nil {
				slog.Error("enrollment failed: cannot restore previous enrollment marker", "error", rerr)
			}
		} else if rerr := os.Remove(enrollmentMarkerPath(e.authorizedFile)); rerr != nil {
			slog.Error("enrollment failed: cannot remove enrollment marker", "error", rerr)
		}
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

func writeEnrollmentDecodeError(w http.ResponseWriter, err error) {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		rejectEnrollmentBody(w, "request body read timed out", http.StatusRequestTimeout)
		return
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		rejectEnrollmentBody(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	rejectEnrollmentBody(w, "invalid JSON", http.StatusBadRequest)
}

func rejectEnrollmentBody(w http.ResponseWriter, message string, status int) {
	// Bound net/http's final unread-body drain before writing the rejection.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now())
	w.Header().Set("Connection", "close")
	http.Error(w, message, status)
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
