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
// The burn is durable: a successful enrollment appends a comment line to
// authorized_keys (see enrollmentBurnLine), and NewEnroller stays disarmed when
// that file holds a burn for the configured token.
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
			"authorized_keys", authorizedFile)
	}
	return e
}

const (
	enrollmentBurnMarker = "portwing-enrollment-burned"
	enrollmentBurnPrefix = "sha256:"
)

// enrollmentBurnLine is the comment line recording a burned token digest. The
// key registry skips it like any other comment.
func enrollmentBurnLine(digest [sha256.Size]byte) string {
	return "# " + enrollmentBurnMarker + " " + enrollmentBurnPrefix + hex.EncodeToString(digest[:])
}

// legacyEnrollmentMarkerPath is where v1.0.0 wrote its burn. Read only.
func legacyEnrollmentMarkerPath(authorizedFile string) string {
	return authorizedFile + ".enrolled"
}

// burnScan summarises what authorized_keys says about past enrollments.
type burnScan struct {
	matched     bool // a burn line carries the configured digest
	malformed   bool // a burn line has an invalid value
	anyBurn     bool // at least one burn line exists
	enrolledKey bool // a key line has an "enrolled:" comment
}

func scanAuthorizedKeysForBurn(data string, digest [sha256.Size]byte) burnScan {
	var scan burnScan
	want := []byte(hex.EncodeToString(digest[:]))
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "#" && len(fields) >= 2 && fields[1] == enrollmentBurnMarker {
			scan.anyBurn = true
			if len(fields) != 3 || !strings.HasPrefix(fields[2], enrollmentBurnPrefix) {
				scan.malformed = true
				continue
			}
			got := []byte(strings.TrimPrefix(fields[2], enrollmentBurnPrefix))
			if !validEnrollmentDigest(got) {
				scan.malformed = true
				continue
			}
			if subtle.ConstantTimeCompare(got, want) == 1 {
				scan.matched = true
			}
			continue
		}
		if len(fields) >= 3 && !strings.HasPrefix(fields[0], "#") && strings.HasPrefix(fields[2], "enrolled:") {
			scan.enrolledKey = true
		}
	}
	return scan
}

// alreadyEnrolled reports whether the configured token has already been used.
// It fails closed: unreadable or malformed burn state counts as used.
func alreadyEnrolled(digest [sha256.Size]byte, authorizedFile string) bool {
	// #nosec G304 -- authorized_keys path is explicit operator configuration.
	data, err := os.ReadFile(authorizedFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Error("reading authorized_keys for enrollment state; treating token as used", "file", authorizedFile, "error", err)
		return true
	}
	scan := scanAuthorizedKeysForBurn(string(data), digest)
	if scan.matched {
		return true
	}
	if scan.malformed {
		slog.Error("authorized_keys has a malformed enrollment burn line; treating token as used", "file", authorizedFile)
		return true
	}
	// Builds before v1.0.0 left only an enrolled: key line.
	if !scan.anyBurn && scan.enrolledKey {
		return true
	}
	return legacyMarkerMatches(digest, authorizedFile)
}

// legacyMarkerMatches reads the v1.0.0 "<authorized_keys>.enrolled" marker.
// A missing marker is ignored; an unreadable or invalid one counts as used.
func legacyMarkerMatches(digest [sha256.Size]byte, authorizedFile string) bool {
	marker := legacyEnrollmentMarkerPath(authorizedFile)
	// #nosec G304 -- marker path derives from explicit operator configuration.
	data, err := os.ReadFile(marker)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		slog.Error("reading legacy enrollment marker; treating token as used", "marker", marker, "error", err)
		return true
	}
	got := []byte(strings.TrimSpace(string(data)))
	if !validEnrollmentDigest(got) {
		slog.Error("legacy enrollment marker is not a SHA-256 hex digest; treating token as used", "marker", marker)
		return true
	}
	want := []byte(hex.EncodeToString(digest[:]))
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

	// Append the burn comment and the key in one write, burn first, so the
	// two land together. Only the keys file is written, never its directory.
	burnLine := enrollmentBurnLine(e.tokenDigest)
	if err := appendAuthorizedKey(e.authorizedFile, burnLine+"\n"+line); err != nil {
		if errors.Is(err, errAppendNotStarted) {
			slog.Error("enrollment failed: cannot write authorized_keys", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		// Bytes may already be on disk, so the burn has to stand.
		e.tokenDigest = [sha256.Size]byte{}
		e.tokenAvailable = false
		e.burned = true
		slog.Error("enrollment failed after authorized_keys was opened; keeping the burn", "error", err)
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

// errAppendNotStarted marks an append that failed before any byte could reach
// authorized_keys, so the token can safely stay armed.
var errAppendNotStarted = errors.New("authorized_keys was not opened")

// appendAuthorizedKey is a seam so tests can force a failure after the key
// file was opened.
var appendAuthorizedKey = appendKeyLine

// appendKeyLine appends text plus a newline to the authorized_keys file in one
// write, creating it (mode 0600) if it does not exist.
func appendKeyLine(path, line string) (err error) {
	// #nosec G304 -- authorized_keys path is explicit operator configuration.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("opening authorized_keys for append: %w: %w", errAppendNotStarted, err)
	}
	if needsLeadingNewline(f) {
		// Never glue the new lines onto an unterminated last line.
		line = "\n" + line
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

// needsLeadingNewline reports whether f is non-empty and does not end in a
// newline. It answers true when it cannot tell: an extra blank line is
// harmless, a line glued onto the previous one is not.
func needsLeadingNewline(f *os.File) bool {
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return err != nil
	}
	last := make([]byte, 1)
	_, err = f.ReadAt(last, info.Size()-1)
	return err != nil || last[0] != '\n'
}
