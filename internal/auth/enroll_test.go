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
}

func TestEnroller_MarkerWithDifferentDigestArms(t *testing.T) {
	t.Parallel()
	e, reg, path := setupEnroller(t, "tok-one")
	if code := enrollOnce(t, e, "tok-one"); code != http.StatusOK {
		t.Fatalf("first enroll: got %d", code)
	}
	e2 := NewEnroller("tok-two", path, reg)
	if code := enrollOnce(t, e2, "tok-two"); code != http.StatusOK {
		t.Fatalf("rotated token: expected 200, got %d", code)
	}
}

func TestEnroller_EnrolledKeyLineWithoutMarkerDisarms(t *testing.T) {
	t.Parallel()
	_, reg, path := setupEnroller(t, "secrettok")
	pub, _ := genPubKeyB64(t)
	line := AuthorizedKeyLine(pub, "enrolled:"+KeyIDForPublicKey(pub))
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e2 := NewEnroller("secrettok", path, reg)
	if code := enrollOnce(t, e2, "secrettok"); code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", code)
	}
}

func TestEnroller_MarkerWriteFailureFailsClosed(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("read-only directory not enforced on Windows or as root")
	}
	e, _, path := setupEnroller(t, "secrettok")
	dir := filepath.Dir(path)
	// #nosec G302 -- read-only private test directory to force the marker write to fail.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	// #nosec G302 -- restore owner write permission on a private test directory for cleanup.
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if code := enrollOnce(t, e, "secrettok"); code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", code)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Errorf("key appended despite marker failure: %q", data)
	}
	// Token stays armed in memory.
	// #nosec G302 -- restore owner write permission on a private test directory.
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if code := enrollOnce(t, e, "secrettok"); code != http.StatusOK {
		t.Fatalf("retry after fixing dir: expected 200, got %d", code)
	}
}

func TestEnroller_MarkerModeAndContent(t *testing.T) {
	t.Parallel()
	e, _, path := setupEnroller(t, "secrettok")
	if code := enrollOnce(t, e, "secrettok"); code != http.StatusOK {
		t.Fatalf("enroll: got %d", code)
	}
	marker := path + ".enrolled"
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("secrettok"))
	if want := hex.EncodeToString(sum[:]) + "\n"; string(data) != want {
		t.Errorf("marker content = %q, want %q", data, want)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(marker)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("marker mode = %o, want 600", info.Mode().Perm())
		}
	}
}

func TestEnroller_InvalidMarkerContentsDisarm(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"garbage":   "not-a-digest\n",
		"empty":     "",
		"short":     strings.Repeat("a", 63) + "\n",
		"uppercase": strings.Repeat("A", 64) + "\n",
		"non-hex":   strings.Repeat("g", 64) + "\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, reg, path := setupEnroller(t, "secrettok")
			if err := os.WriteFile(path+".enrolled", []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			e := NewEnroller("secrettok", path, reg)
			if code := enrollOnce(t, e, "secrettok"); code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", code)
			}
		})
	}
}

func TestEnroller_ValidDifferentMarkerArms(t *testing.T) {
	t.Parallel()
	_, reg, path := setupEnroller(t, "tok-two")
	other := sha256.Sum256([]byte("tok-one"))
	if err := os.WriteFile(path+".enrolled", []byte(hex.EncodeToString(other[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := NewEnroller("tok-two", path, reg)
	if code := enrollOnce(t, e, "tok-two"); code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
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

func TestEnroller_FailedRotatedEnrollmentRestoresPreviousMarker(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("read-only file not enforced on Windows or as root")
	}
	_, reg, path := setupEnroller(t, "tok-two")
	other := sha256.Sum256([]byte("tok-one"))
	// Deliberately unusual bytes (no trailing newline) to prove an exact restore.
	prev := []byte("  " + hex.EncodeToString(other[:]))
	marker := path + ".enrolled"
	if err := os.WriteFile(marker, prev, 0o600); err != nil {
		t.Fatal(err)
	}
	e := NewEnroller("tok-two", path, reg)
	makeAuthorizedKeysReadOnly(t, path)

	if code := enrollOnce(t, e, "tok-two"); code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", code)
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, prev) {
		t.Errorf("marker = %q, want %q", got, prev)
	}
}

func TestEnroller_FailedFirstEnrollmentRemovesMarker(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("read-only file not enforced on Windows or as root")
	}
	e, _, path := setupEnroller(t, "secrettok")
	makeAuthorizedKeysReadOnly(t, path)

	if code := enrollOnce(t, e, "secrettok"); code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", code)
	}
	if _, err := os.Stat(path + ".enrolled"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("marker should be removed, stat err = %v", err)
	}
}

// Not parallel: swaps the package-level directory sync hook.
func TestEnroller_MarkerDirectorySyncFailureFailsClosed(t *testing.T) {
	e, _, path := setupEnroller(t, "secrettok")
	realSync := syncRotationDirectory
	syncRotationDirectory = func(*os.File) error { return errors.New("fixture directory sync failed") }
	t.Cleanup(func() { syncRotationDirectory = realSync })

	if code := enrollOnce(t, e, "secrettok"); code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", code)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Errorf("key appended despite directory sync failure: %q", data)
	}
}

// Not parallel: swaps the package-level directory sync hook.
func TestEnroller_RestoreFailureStillReturns500(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("read-only file not enforced on Windows or as root")
	}
	_, reg, path := setupEnroller(t, "tok-two")
	other := sha256.Sum256([]byte("tok-one"))
	if err := os.WriteFile(path+".enrolled", []byte(hex.EncodeToString(other[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := NewEnroller("tok-two", path, reg)
	makeAuthorizedKeysReadOnly(t, path)

	realSync := syncRotationDirectory
	calls := 0
	syncRotationDirectory = func(f *os.File) error {
		calls++
		if calls > 1 {
			return errors.New("fixture restore sync failed")
		}
		return realSync(f)
	}
	t.Cleanup(func() { syncRotationDirectory = realSync })

	if code := enrollOnce(t, e, "tok-two"); code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", code)
	}
	if calls != 2 {
		t.Errorf("directory sync calls = %d, want 2 (install + restore)", calls)
	}
}

// Not parallel: swaps the package-level directory sync hook.
func TestEnroller_MarkerRemoveFailureStillReturns500(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("read-only file not enforced on Windows or as root")
	}
	e, _, path := setupEnroller(t, "secrettok")
	makeAuthorizedKeysReadOnly(t, path)

	realSync := syncRotationDirectory
	// Remove the marker as it is installed so the rollback remove fails.
	syncRotationDirectory = func(f *os.File) error {
		_ = os.Remove(path + ".enrolled")
		return realSync(f)
	}
	t.Cleanup(func() { syncRotationDirectory = realSync })

	if code := enrollOnce(t, e, "secrettok"); code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", code)
	}
}

func TestEnroller_UnreadableMarkerDisarms(t *testing.T) {
	t.Parallel()
	_, reg, path := setupEnroller(t, "secrettok")
	// A directory at the marker path makes the read fail with something other
	// than not-exist, which must count as already used.
	if err := os.Mkdir(enrollmentMarkerPath(path), 0o700); err != nil {
		t.Fatal(err)
	}
	e2 := NewEnroller("secrettok", path, reg)
	if code := enrollOnce(t, e2, "secrettok"); code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", code)
	}
}

func TestEnroller_MarkerRenameFailureFailsClosed(t *testing.T) {
	t.Parallel()
	e, _, path := setupEnroller(t, "secrettok")
	// A non-empty directory at the marker path makes the rename fail after the
	// enroller was armed.
	marker := enrollmentMarkerPath(path)
	if err := os.Mkdir(marker, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(marker, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := enrollOnce(t, e, "secrettok"); code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", code)
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "enrolled:") {
		t.Fatal("key appended despite marker failure")
	}
}
