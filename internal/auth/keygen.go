package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
)

// LoadPrivateKey reads an Ed25519 private key from a PEM-encoded PKCS#8 file.
func LoadPrivateKey(path string) (ed25519.PrivateKey, error) {
	// #nosec G304 -- private key path is explicit operator configuration.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading private key %q: %w", path, err)
	}
	return ParsePrivateKeyPEM(data)
}

// ParsePrivateKeyPEM decodes a PEM block containing a PKCS#8 Ed25519 private key.
func ParsePrivateKeyPEM(pemData []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in private key data")
	}
	if block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("expected PEM type %q, got %q", "PRIVATE KEY", block.Type)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing PKCS#8 private key: %w", err)
	}
	ed, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is not Ed25519 (got %T)", key)
	}
	return ed, nil
}

// MarshalPrivateKeyPEM encodes an Ed25519 private key as a PKCS#8 PEM block.
func MarshalPrivateKeyPEM(priv ed25519.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("marshaling PKCS#8 key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: der,
	}), nil
}

// GenerateKeyPair generates a new Ed25519 keypair and returns the private key
// in PKCS#8 PEM format and the public key as an authorized_keys line.
func GenerateKeyPair(comment string) (privPEM []byte, authKeyLine string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("generating key: %w", err)
	}

	privPEM, err = MarshalPrivateKeyPEM(priv)
	if err != nil {
		return nil, "", err
	}

	authKeyLine = AuthorizedKeyLine(pub, comment)
	return privPEM, authKeyLine, nil
}

// AuthorizedKeyLine formats an Ed25519 public key as an authorized_keys line.
func AuthorizedKeyLine(pub ed25519.PublicKey, comment string) string {
	b64 := base64.StdEncoding.EncodeToString(pub)
	if comment != "" {
		return "ed25519 " + b64 + " " + comment
	}
	return "ed25519 " + b64
}

// KeyIDForPublicKey computes the Key-ID (hex(SHA-256(raw pubkey)[:8])) for
// an Ed25519 public key.
func KeyIDForPublicKey(pub ed25519.PublicKey) string {
	h := sha256.Sum256(pub)
	return hex.EncodeToString(h[:8])
}

// NewNonce generates a fresh 128-bit random nonce as a 32-character hex string.
func NewNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating nonce: %w", err)
	}
	return hex.EncodeToString(b), nil
}
