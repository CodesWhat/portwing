// SPDX-License-Identifier: MIT

package auth

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func FuzzVerifyRequest(f *testing.F) {
	privateKey := ed25519.NewKeyFromSeed([]byte("portwing-auth-fuzz-seed-00000001"))
	publicKey := privateKey.Public().(ed25519.PublicKey)
	keyHash := sha256.Sum256(publicKey)
	keyID := hex.EncodeToString(keyHash[:8])
	registry := NewKeyRegistry("")
	registry.keys[keyID] = &AuthorizedKey{KeyID: keyID, PubKey: publicKey}
	const fuzzAgentID = "fuzz-agent-0001"
	registry.SetSignaturePolicy(SignaturePolicy{AgentID: fuzzAgentID})

	seedTarget := "/v1.44/containers/a%2Fb/logs?tail=10&stdout=1"
	seedBody := []byte(`{"follow":true}`)
	seedTimestamp := time.Now().Unix()
	seedNonce := "00112233445566778899aabbccddeeff"
	seedSignature := ed25519.Sign(privateKey, CanonicalMessage(
		http.MethodGet,
		seedTarget,
		BodyHashHex(seedBody),
		seedTimestamp,
		seedNonce,
	))
	f.Add(
		http.MethodGet,
		seedTarget,
		seedBody,
		keyID,
		strconv.FormatInt(seedTimestamp, 10),
		seedNonce,
		base64.RawURLEncoding.EncodeToString(seedSignature),
		SignatureVersion2,
	)
	seedV3Signature := ed25519.Sign(privateKey, CanonicalMessageV3(
		fuzzAgentID,
		http.MethodGet,
		seedTarget,
		BodyHashHex(seedBody),
		seedTimestamp,
		seedNonce,
	))
	f.Add(
		http.MethodGet,
		seedTarget,
		seedBody,
		keyID,
		strconv.FormatInt(seedTimestamp, 10),
		seedNonce,
		base64.RawURLEncoding.EncodeToString(seedV3Signature),
		SignatureVersion3,
	)
	f.Add("", "/", []byte(nil), "", "", "", "", "")
	f.Add("DELETE", "/containers/id?force=1", []byte("body"), "unknown", "not-a-time", "not-a-nonce", "%%%", "99")
	f.Add("POST", "/images/%zz/json", []byte{0, 1, 2}, keyID, "9223372036854775808", strings.Repeat("a", 32), "=", SignatureVersion2)

	f.Fuzz(func(
		t *testing.T,
		method string,
		requestTarget string,
		body []byte,
		fuzzKeyID string,
		fuzzTimestamp string,
		fuzzNonce string,
		fuzzSignature string,
		fuzzVersion string,
	) {
		if !strings.HasPrefix(requestTarget, "/") {
			return
		}
		parsedURL, err := url.ParseRequestURI(requestTarget)
		if err != nil {
			return
		}

		req := &http.Request{Method: method, URL: parsedURL, Header: make(http.Header)}
		req.Header.Set(HeaderKeyID, fuzzKeyID)
		req.Header.Set(HeaderTimestamp, fuzzTimestamp)
		req.Header.Set(HeaderNonce, fuzzNonce)
		req.Header.Set(HeaderSignature, fuzzSignature)
		req.Header.Set(HeaderSignatureVersion, fuzzVersion)

		lru := NewNonceLRU(1, 60)
		lru.Close()
		gotKeyID, verifyErr := VerifyRequest(req, body, registry, lru, 60)
		if verifyErr != nil {
			if gotKeyID != "" {
				t.Fatalf("VerifyRequest returned key ID %q with error %v", gotKeyID, verifyErr)
			}
		} else {
			if gotKeyID != keyID {
				t.Fatalf("VerifyRequest accepted unexpected key ID %q", gotKeyID)
			}
			tsUnix, err := strconv.ParseInt(fuzzTimestamp, 10, 64)
			if err != nil {
				t.Fatalf("VerifyRequest accepted invalid timestamp %q", fuzzTimestamp)
			}
			signature, err := base64.RawURLEncoding.DecodeString(fuzzSignature)
			if err != nil {
				t.Fatalf("VerifyRequest accepted invalid signature encoding %q", fuzzSignature)
			}
			var message []byte
			switch fuzzVersion {
			case SignatureVersion3:
				message = CanonicalMessageV3(fuzzAgentID, method, CanonicalRequestTarget(parsedURL), BodyHashHex(body), tsUnix, fuzzNonce)
			case SignatureVersion2:
				message = CanonicalMessage(method, CanonicalRequestTarget(parsedURL), BodyHashHex(body), tsUnix, fuzzNonce)
			case "":
				if parsedURL.RawQuery != "" || parsedURL.ForceQuery {
					t.Fatalf("VerifyRequest accepted a legacy signature for target %q", requestTarget)
				}
				message = CanonicalMessage(method, parsedURL.Path, BodyHashHex(body), tsUnix, fuzzNonce)
			default:
				t.Fatalf("VerifyRequest accepted signature version %q for target %q", fuzzVersion, requestTarget)
			}
			if !ed25519.Verify(publicKey, message, signature) {
				t.Fatal("VerifyRequest accepted a signature that does not match the canonical request")
			}
		}

		nonceHash := sha256.Sum256(append([]byte(method+"\x00"+requestTarget+"\x00"), body...))
		validNonce := hex.EncodeToString(nonceHash[:16])
		validTimestamp := time.Now().Unix()
		canonicalTarget := CanonicalRequestTarget(parsedURL)
		validSignature := ed25519.Sign(privateKey, CanonicalMessage(
			method,
			canonicalTarget,
			BodyHashHex(body),
			validTimestamp,
			validNonce,
		))
		setValidHeaders := func(header http.Header) {
			header.Set(HeaderKeyID, keyID)
			header.Set(HeaderTimestamp, strconv.FormatInt(validTimestamp, 10))
			header.Set(HeaderNonce, validNonce)
			header.Set(HeaderSignature, base64.RawURLEncoding.EncodeToString(validSignature))
			header.Set(HeaderSignatureVersion, SignatureVersion2)
		}

		validReq := &http.Request{Method: method, URL: parsedURL, Header: make(http.Header)}
		setValidHeaders(validReq.Header)
		validLRU := NewNonceLRU(1, 60)
		validLRU.Close()
		if got, err := VerifyRequest(validReq, body, registry, validLRU, 60); err != nil || got != keyID {
			t.Fatalf("exact canonical target %q did not verify: key ID %q, error %v", canonicalTarget, got, err)
		}

		mutatedURL := *parsedURL
		if mutatedURL.RawQuery == "" {
			mutatedURL.RawQuery = "portwing-fuzz-mutation=1"
		} else {
			mutatedURL.RawQuery += "&portwing-fuzz-mutation=1"
		}
		mutatedReq := &http.Request{Method: method, URL: &mutatedURL, Header: make(http.Header)}
		setValidHeaders(mutatedReq.Header)
		mutatedLRU := NewNonceLRU(1, 60)
		mutatedLRU.Close()
		if _, err := VerifyRequest(mutatedReq, body, registry, mutatedLRU, 60); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("mutated canonical target %q returned %v, want ErrBadSignature", CanonicalRequestTarget(&mutatedURL), err)
		}

		v3Signature := ed25519.Sign(privateKey, CanonicalMessageV3(
			fuzzAgentID,
			method,
			canonicalTarget,
			BodyHashHex(body),
			validTimestamp,
			validNonce,
		))
		for version, wantOK := range map[string]bool{SignatureVersion3: true, SignatureVersion2: false} {
			v3Req := &http.Request{Method: method, URL: parsedURL, Header: make(http.Header)}
			setValidHeaders(v3Req.Header)
			v3Req.Header.Set(HeaderSignature, base64.RawURLEncoding.EncodeToString(v3Signature))
			v3Req.Header.Set(HeaderSignatureVersion, version)
			v3LRU := NewNonceLRU(1, 60)
			v3LRU.Close()
			got, err := VerifyRequest(v3Req, body, registry, v3LRU, 60)
			if wantOK && (err != nil || got != keyID) {
				t.Fatalf("exact v3 canonical target %q did not verify: key ID %q, error %v", canonicalTarget, got, err)
			}
			if !wantOK && !errors.Is(err, ErrBadSignature) {
				t.Fatalf("v3 signature presented as version %s returned %v, want ErrBadSignature", version, err)
			}
		}
	})
}
