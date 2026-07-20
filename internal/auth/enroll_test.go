package auth

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
