// SPDX-License-Identifier: MIT

package auth

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// setupEnroller creates a temporary authorized_keys file and returns
// a fresh Enroller + registry.
func setupEnroller(t *testing.T, token string) (*Enroller, *KeyRegistry, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "authorized_keys")
	// Create empty file with correct perms.
	if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	reg := NewKeyRegistry(path)
	if err := reg.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return NewEnroller(token, path, reg), reg, path
}

func enrollBody(t *testing.T, token, pubKeyB64 string) *bytes.Buffer {
	t.Helper()
	body, err := json.Marshal(map[string]string{
		"enrollment_token": token,
		"public_key":       pubKeyB64,
	})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return bytes.NewBuffer(body)
}

func TestEnrollerArmedLifecycle(t *testing.T) {
	empty, _, _ := setupEnroller(t, "")
	if empty.Armed() {
		t.Fatal("enrollment without a credential is armed")
	}
	e, registry, path := setupEnroller(t, "bootstrap-secret")
	if !e.Armed() {
		t.Fatal("fresh enrollment is not armed")
	}
	if code := enrollOnce(t, e, "bootstrap-secret"); code != http.StatusOK {
		t.Fatalf("enrollment status = %d, want 200", code)
	}
	if e.Armed() {
		t.Fatal("used enrollment remains armed")
	}
	if NewEnroller("bootstrap-secret", path, registry).Armed() {
		t.Fatal("used enrollment is armed after restart")
	}
	if !NewEnroller("replacement-secret", path, registry).Armed() {
		t.Fatal("replacement enrollment credential is not armed")
	}
}

func TestEnroller_Success(t *testing.T) {
	t.Parallel()
	e, reg, _ := setupEnroller(t, "secrettok")
	_, b64 := genPubKeyB64(t)

	req := httptest.NewRequest(http.MethodPost, "/api/portwing/enroll", enrollBody(t, "secrettok", b64))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Registry should now have the key.
	if reg.Len() != 1 {
		t.Errorf("expected 1 registered key after enrollment, got %d", reg.Len())
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["key_id"] == "" {
		t.Error("response missing key_id")
	}
}

func TestEnroller_WrongToken(t *testing.T) {
	t.Parallel()
	e, _, _ := setupEnroller(t, "secrettok")
	_, b64 := genPubKeyB64(t)

	req := httptest.NewRequest(http.MethodPost, "/api/portwing/enroll", enrollBody(t, "wrongtoken", b64))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong token, got %d", rec.Code)
	}
}

func TestEnroller_TokenBurnedAfterSuccess(t *testing.T) {
	t.Parallel()
	e, _, _ := setupEnroller(t, "secrettok")
	_, b64 := genPubKeyB64(t)
	_, b64b := genPubKeyB64(t)

	// First call succeeds.
	req1 := httptest.NewRequest(http.MethodPost, "/api/portwing/enroll", enrollBody(t, "secrettok", b64))
	rec1 := httptest.NewRecorder()
	e.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first enroll: expected 200, got %d", rec1.Code)
	}

	// Second call with the same token must be rejected (burned).
	req2 := httptest.NewRequest(http.MethodPost, "/api/portwing/enroll", enrollBody(t, "secrettok", b64b))
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("second enroll: expected 401 (burned), got %d", rec2.Code)
	}
	if !strings.Contains(rec2.Body.String(), "already used") {
		t.Errorf("expected 'already used' in body, got: %s", rec2.Body.String())
	}
}

func TestEnroller_InvalidBase64PublicKey(t *testing.T) {
	t.Parallel()
	e, _, _ := setupEnroller(t, "secrettok")

	body, _ := json.Marshal(map[string]string{
		"enrollment_token": "secrettok",
		"public_key":       "not!valid!base64",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/portwing/enroll", bytes.NewBuffer(body))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad base64, got %d", rec.Code)
	}
}

func TestEnroller_WrongKeyLength(t *testing.T) {
	t.Parallel()
	e, _, _ := setupEnroller(t, "secrettok")

	// 10 bytes is wrong (needs 32).
	short := base64.StdEncoding.EncodeToString(make([]byte, 10))
	body, _ := json.Marshal(map[string]string{
		"enrollment_token": "secrettok",
		"public_key":       short,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/portwing/enroll", bytes.NewBuffer(body))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for wrong key length, got %d", rec.Code)
	}
}

func TestEnroller_InvalidJSON(t *testing.T) {
	t.Parallel()
	e, _, _ := setupEnroller(t, "secrettok")

	req := httptest.NewRequest(http.MethodPost, "/api/portwing/enroll", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid JSON, got %d", rec.Code)
	}
}

func TestEnroller_OversizedBodyRejected(t *testing.T) {
	t.Parallel()
	e, _, _ := setupEnroller(t, "secrettok")
	body := `{"enrollment_token":"` + strings.Repeat("x", 70*1024) + `","public_key":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/portwing/enroll", strings.NewReader(body))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for oversized enrollment body, got %d", rec.Code)
	}
}

func TestEnroller_OversizedTrailingBodyRejected(t *testing.T) {
	e, _, _ := setupEnroller(t, "secrettok")
	firstValue := `{"enrollment_token":"wrong","public_key":""}`
	body := firstValue + strings.Repeat(" ", int(maxEnrollmentBodyBytes)-len(firstValue)+1)
	req := httptest.NewRequest(http.MethodPost, "/api/portwing/enroll", strings.NewReader(body))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for oversized trailing enrollment body, got %d", rec.Code)
	}
}

func TestEnrollerStoresFixedLengthTokenDigest(t *testing.T) {
	e := NewEnroller("a variable-length bootstrap secret", "/unused", nil)
	if e.tokenDigest == ([32]byte{}) {
		t.Fatal("expected enrollment token digest to be initialized")
	}
	if !e.tokenAvailable {
		t.Fatal("expected non-empty enrollment token to be available")
	}
}

func TestEnroller_TrailingJSONRejected(t *testing.T) {
	t.Parallel()
	e, reg, _ := setupEnroller(t, "secrettok")
	_, b64 := genPubKeyB64(t)
	body := enrollBody(t, "secrettok", b64).String() + `{}`
	req := httptest.NewRequest(http.MethodPost, "/api/portwing/enroll", strings.NewReader(body))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for trailing JSON value, got %d", rec.Code)
	}
	if reg.Len() != 0 {
		t.Fatal("trailing JSON request must not enroll a key")
	}
}

func TestEnroller_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	e, _, _ := setupEnroller(t, "secrettok")

	req := httptest.NewRequest(http.MethodGet, "/api/portwing/enroll", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET, got %d", rec.Code)
	}
}

func enrollOnce(t *testing.T, e *Enroller, token string) int {
	t.Helper()
	_, b64 := genPubKeyB64(t)
	req := httptest.NewRequest(http.MethodPost, "/api/portwing/enroll", enrollBody(t, token, b64))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec.Code
}

func TestEnroller_BurnPersistsAcrossRestart(t *testing.T) {
	t.Parallel()
	e, reg, path := setupEnroller(t, "secrettok")
	if code := enrollOnce(t, e, "secrettok"); code != http.StatusOK {
		t.Fatalf("first enroll: got %d", code)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(before)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected burn + key lines, got %q", before)
	}
	if want := enrollmentBurnLine(sha256.Sum256([]byte("secrettok"))); lines[0] != want {
		t.Errorf("first line = %q, want %q", lines[0], want)
	}
	if !strings.Contains(lines[1], "enrolled:") {
		t.Errorf("second line is not the enrolled key: %q", lines[1])
	}

	e2 := NewEnroller("secrettok", path, reg)
	if code := enrollOnce(t, e2, "secrettok"); code != http.StatusUnauthorized {
		t.Fatalf("after restart: expected 401, got %d", code)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("authorized_keys changed after burned enroll:\n%s\n---\n%s", before, after)
	}
	e3 := NewEnroller("secrettok", path, reg)
	_, b64 := genPubKeyB64(t)
	req := httptest.NewRequest(http.MethodPost, "/api/portwing/enroll", enrollBody(t, "wrong", b64))
	rec := httptest.NewRecorder()
	e3.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token after restart: expected 401, got %d", rec.Code)
	}
}

func TestEnroller_ReadOnlyDirectoryWritableFile(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("read-only directory not enforced on Windows or as root")
	}
	e, reg, path := setupEnroller(t, "secrettok")
	dir := filepath.Dir(path)
	// #nosec G302 -- read-only private test directory: only the keys file is writable.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	// #nosec G302 -- restore owner write permission on a private test directory for cleanup.
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if code := enrollOnce(t, e, "secrettok"); code != http.StatusOK {
		t.Fatalf("expected 200 with writable file in read-only dir, got %d", code)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# portwing-enrollment-burned sha256:") || !strings.Contains(string(data), "enrolled:") {
		t.Errorf("burn and key lines missing: %q", data)
	}
	e2 := NewEnroller("secrettok", path, reg)
	if code := enrollOnce(t, e2, "secrettok"); code != http.StatusUnauthorized {
		t.Fatalf("after restart: expected 401, got %d", code)
	}
}

func TestEnroller_RotationBurnsEachToken(t *testing.T) {
	t.Parallel()
	e, reg, path := setupEnroller(t, "tokA")
	if code := enrollOnce(t, e, "tokA"); code != http.StatusOK {
		t.Fatalf("A: got %d", code)
	}
	eB := NewEnroller("tokB", path, reg)
	if code := enrollOnce(t, eB, "tokB"); code != http.StatusOK {
		t.Fatalf("B should arm after A's burn: got %d", code)
	}
	if got := strings.Count(readFile(t, path), enrollmentBurnMarker); got != 2 {
		t.Fatalf("expected 2 burn lines, got %d", got)
	}
	if code := enrollOnce(t, NewEnroller("tokB", path, reg), "tokB"); code != http.StatusUnauthorized {
		t.Fatalf("restart with B: expected 401, got %d", code)
	}
	if code := enrollOnce(t, NewEnroller("tokC", path, reg), "tokC"); code != http.StatusOK {
		t.Fatalf("restart with C should arm: got %d", code)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestEnroller_MalformedBurnLineDisarms(t *testing.T) {
	t.Parallel()
	for name, line := range map[string]string{
		"no prefix":     "# portwing-enrollment-burned " + strings.Repeat("a", 64),
		"short":         "# portwing-enrollment-burned sha256:abc",
		"uppercase":     "# portwing-enrollment-burned sha256:" + strings.Repeat("A", 64),
		"missing value": "# portwing-enrollment-burned",
		"extra field":   "# portwing-enrollment-burned sha256:" + strings.Repeat("a", 64) + " x",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, _, path := setupEnroller(t, "secrettok")
			if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if code := enrollOnce(t, NewEnroller("secrettok", path, NewKeyRegistry(path)), "secrettok"); code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", code)
			}
		})
	}
}

func TestEnroller_EnrolledKeyLineWithoutBurnDisarms(t *testing.T) {
	t.Parallel()
	_, reg, path := setupEnroller(t, "secrettok")
	pub, _ := genPubKeyB64(t)
	line := AuthorizedKeyLine(pub, "enrolled:"+KeyIDForPublicKey(pub))
	if err := os.WriteFile(path, []byte("# note\n\n"+line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := enrollOnce(t, NewEnroller("secrettok", path, reg), "secrettok"); code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", code)
	}
}

func TestEnroller_EnrolledKeyWithOtherBurnArms(t *testing.T) {
	t.Parallel()
	_, reg, path := setupEnroller(t, "secrettok")
	pub, _ := genPubKeyB64(t)
	line := AuthorizedKeyLine(pub, "enrolled:"+KeyIDForPublicKey(pub))
	content := enrollmentBurnLine(sha256.Sum256([]byte("old"))) + "\n" + line + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := enrollOnce(t, NewEnroller("secrettok", path, reg), "secrettok"); code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
}

func TestEnroller_LegacyMarker(t *testing.T) {
	t.Parallel()
	digest := func(s string) string {
		d := sha256.Sum256([]byte(s))
		return hex.EncodeToString(d[:]) + "\n"
	}
	cases := map[string]struct {
		content string
		want    int
	}{
		"matching":  {digest("secrettok"), http.StatusUnauthorized},
		"invalid":   {"not-a-digest\n", http.StatusUnauthorized},
		"other tok": {digest("other"), http.StatusOK},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, reg, path := setupEnroller(t, "secrettok")
			if err := os.WriteFile(path+".enrolled", []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if code := enrollOnce(t, NewEnroller("secrettok", path, reg), "secrettok"); code != tc.want {
				t.Fatalf("expected %d, got %d", tc.want, code)
			}
		})
	}
}

func TestEnroller_UnreadableLegacyMarkerDisarms(t *testing.T) {
	t.Parallel()
	_, reg, path := setupEnroller(t, "secrettok")
	// A directory at the marker path makes ReadFile fail with a non-ENOENT error.
	if err := os.Mkdir(path+".enrolled", 0o700); err != nil {
		t.Fatal(err)
	}
	if code := enrollOnce(t, NewEnroller("secrettok", path, reg), "secrettok"); code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", code)
	}
}

func TestEnroller_UnreadableAuthorizedKeysDisarms(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// A directory at the keys path makes ReadFile fail with a non-ENOENT error.
	path := filepath.Join(dir, "authorized_keys")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	e := NewEnroller("secrettok", path, NewKeyRegistry(path))
	if code := enrollOnce(t, e, "secrettok"); code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", code)
	}
}

func TestEnroller_MissingAuthorizedKeysArms(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "authorized_keys")
	e := NewEnroller("secrettok", path, NewKeyRegistry(path))
	if code := enrollOnce(t, e, "secrettok"); code != http.StatusOK {
		t.Fatalf("expected 200 (file created), got %d", code)
	}
}

func TestKeyRegistry_LoadIgnoresBurnLine(t *testing.T) {
	t.Parallel()
	e, reg, path := setupEnroller(t, "secrettok")
	if code := enrollOnce(t, e, "secrettok"); code != http.StatusOK {
		t.Fatalf("enroll: got %d", code)
	}
	reg2 := NewKeyRegistry(path)
	if err := reg2.Load(); err != nil {
		t.Fatal(err)
	}
	if reg2.Len() != 1 || reg.Len() != 1 {
		t.Fatalf("expected 1 key, got %d / %d", reg2.Len(), reg.Len())
	}
}

func makeAuthorizedKeysReadOnly(t *testing.T, path string) {
	t.Helper()
	// #nosec G302 -- read-only private test file to force the key append to fail.
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	// #nosec G302 -- restore owner write permission on a private test file for cleanup.
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
}

func TestEnroller_AppendFailureBeforeOpenStaysArmed(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions not enforced on Windows or as root")
	}
	e, _, path := setupEnroller(t, "secrettok")
	makeAuthorizedKeysReadOnly(t, path)
	if code := enrollOnce(t, e, "secrettok"); code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", code)
	}
	if data := readFile(t, path); data != "" {
		t.Errorf("bytes written despite open failure: %q", data)
	}
	// #nosec G302 -- restore owner write permission on a private test file.
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if code := enrollOnce(t, e, "secrettok"); code != http.StatusOK {
		t.Fatalf("retry after fixing permissions: expected 200, got %d", code)
	}
}

func TestEnroller_AppendFailureMissingDirectoryStaysArmed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "authorized_keys")
	e := NewEnroller("secrettok", path, NewKeyRegistry(path))
	if code := enrollOnce(t, e, "secrettok"); code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", code)
	}
	if err := os.Mkdir(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if code := enrollOnce(t, e, "secrettok"); code != http.StatusOK {
		t.Fatalf("retry: expected 200, got %d", code)
	}
}

// Not parallel: swaps the package-level appendAuthorizedKey seam.
func TestEnroller_AppendFailureAfterOpenKeepsBurn(t *testing.T) {
	e, _, _ := setupEnroller(t, "secrettok")
	prev := appendAuthorizedKey
	t.Cleanup(func() { appendAuthorizedKey = prev })
	appendAuthorizedKey = func(string, string) error {
		return errors.New("closing authorized_keys: simulated flush failure")
	}
	if code := enrollOnce(t, e, "secrettok"); code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", code)
	}
	if code := enrollOnce(t, e, "secrettok"); code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on retry, got %d", code)
	}
}

func TestEnroller_UnterminatedKeysFileGetsNewline(t *testing.T) {
	t.Parallel()
	_, reg, path := setupEnroller(t, "secrettok")
	pub, _ := genPubKeyB64(t)
	existing := AuthorizedKeyLine(pub, "operator")
	// No trailing newline on the operator's last line.
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	e := NewEnroller("secrettok", path, reg)
	if code := enrollOnce(t, e, "secrettok"); code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 3 || lines[0] != existing || !strings.HasPrefix(lines[1], "# portwing-enrollment-burned ") {
		t.Fatalf("unexpected authorized_keys layout:\n%s", data)
	}
	// The burn survives, so a restart stays disarmed.
	if code := enrollOnce(t, NewEnroller("secrettok", path, reg), "secrettok"); code != http.StatusUnauthorized {
		t.Fatalf("expected 401 after restart, got %d", code)
	}
}

// A normal v1.0.0 enrollment leaves an enrolled: key and a marker for the old
// token. After upgrading, a fresh token is a rotation and must arm, while the
// old token stays burned.
func TestEnroller_UpgradedV100RotationArms(t *testing.T) {
	t.Parallel()
	_, reg, path := setupEnroller(t, "newtok")
	pub, _ := genPubKeyB64(t)
	line := AuthorizedKeyLine(pub, "enrolled:"+KeyIDForPublicKey(pub))
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := sha256.Sum256([]byte("oldtok"))
	if err := os.WriteFile(path+".enrolled", []byte(hex.EncodeToString(old[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := enrollOnce(t, NewEnroller("oldtok", path, reg), "oldtok"); code != http.StatusUnauthorized {
		t.Fatalf("old token: expected 401, got %d", code)
	}
	if code := enrollOnce(t, NewEnroller("newtok", path, reg), "newtok"); code != http.StatusOK {
		t.Fatalf("new token: expected 200, got %d", code)
	}
	if code := enrollOnce(t, NewEnroller("newtok", path, reg), "newtok"); code != http.StatusUnauthorized {
		t.Fatalf("new token after restart: expected 401, got %d", code)
	}
}
