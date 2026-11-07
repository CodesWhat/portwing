// SPDX-License-Identifier: MIT

package auth

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// signature-v3-vectors.json is the shared fixture Drydock consumes. The key in
// it is derived from a readable ASCII seed and exists only for this file: it is
// not a credential and authorizes nothing anywhere. UPDATE_SIGNATURE_VECTORS=1
// regenerates the file from vectorSpecs; a normal run verifies it.
const vectorsPath = "testdata/signature-v3-vectors.json"

const vectorSeedASCII = "portwing-test-vectors-only-00001"

const (
	vectorAgentA = "agent-a-0123456789abcdef0123456789abcdef"
	vectorAgentB = "agent-b-fedcba9876543210fedcba9876543210"
	vectorTime   = int64(1793000000)
)

type vectorFile struct {
	Description string       `json:"description"`
	TestOnly    bool         `json:"testOnly"`
	Format      vectorFormat `json:"format"`
	Key         vectorKey    `json:"key"`
	AgentIDRule vectorRule   `json:"agentIdRule"`
	Agents      vectorAgents `json:"agents"`
	Info        vectorInfo   `json:"infoEndpoint"`
	Now         int64        `json:"nowUnix"`
	Vectors     []vector     `json:"vectors"`
}

type vectorFormat struct {
	V3     string `json:"v3"`
	V2     string `json:"v2"`
	Legacy string `json:"legacy"`
	Notes  string `json:"notes"`
}

type vectorKey struct {
	SeedASCII       string `json:"seedAscii"`
	PublicKeyBase64 string `json:"publicKeyBase64"`
	KeyID           string `json:"keyId"`
}

type vectorRule struct {
	Pattern   string   `json:"pattern"`
	MinLength int      `json:"minLength"`
	MaxLength int      `json:"maxLength"`
	Generated string   `json:"generatedFormat"`
	Valid     []string `json:"valid"`
	Invalid   []string `json:"invalid"`
}

type vectorInfo struct {
	Path        string            `json:"path"`
	Example     map[string]any    `json:"exampleWhenV3Available"`
	Unavailable map[string]any    `json:"exampleWhenV3Unavailable"`
	Reasons     map[string]string `json:"v3Reasons"`
	Notes       string            `json:"notes"`
}

type vectorAgents struct {
	A string `json:"A"`
	B string `json:"B"`
}

type vectorRequest struct {
	Method string `json:"method"`
	Target string `json:"target"`
	Body   string `json:"body"`
}

type vectorState struct {
	KeyLocked  bool `json:"keyLockedToV3"`
	MinVersion int  `json:"minVersion"`
	// V3Unavailable is the reason the agent refuses v3 (empty: v3 offered).
	V3Unavailable string `json:"v3Unavailable,omitempty"`
}

type vectorExpect struct {
	Accept bool   `json:"accept"`
	Status int    `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type vector struct {
	Name            string            `json:"name"`
	Description     string            `json:"description"`
	SignedVersion   string            `json:"signedVersion"`
	SignedForAgent  string            `json:"signedForAgent"`
	PresentedTo     string            `json:"presentedToAgent"`
	Request         vectorRequest     `json:"request"`
	Timestamp       int64             `json:"timestamp"`
	Nonce           string            `json:"nonce"`
	BodySHA256      string            `json:"bodySha256"`
	CanonicalString string            `json:"canonicalString"`
	CanonicalHex    string            `json:"canonicalHex"`
	Signature       string            `json:"signatureBase64url"`
	Headers         map[string]string `json:"headers"`
	State           vectorState       `json:"agentState"`
	Expect          vectorExpect      `json:"expect"`
}

func vectorKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	if len(vectorSeedASCII) != ed25519.SeedSize {
		t.Fatalf("vector seed must be %d bytes", ed25519.SeedSize)
	}
	priv := ed25519.NewKeyFromSeed([]byte(vectorSeedASCII))
	return priv.Public().(ed25519.PublicKey), priv
}

// vectorSpecs lists the cases; everything derived from them (canonical bytes,
// signature) is computed, never hand-written.
func vectorSpecs() []vector {
	const (
		queryTarget = "/v1.44/containers/json?all=1&filters=%7B%22status%22%3A%5B%22running%22%5D%7D"
		composeBody = `{"operation":"up","stackName":"web","composeFile":"services: {}"}`
	)
	req := func(method, target, body string) vectorRequest {
		return vectorRequest{Method: method, Target: target, Body: body}
	}
	accept := vectorExpect{Accept: true, Status: 200}
	reject := func(reason string) vectorExpect {
		return vectorExpect{Accept: false, Status: 401, Reason: reason}
	}
	nonce := func(n string) string { return strings.Repeat("0", 32-len(n)) + n }
	v := func(name, desc, signed, signedFor, presented string, r vectorRequest, n string, headers map[string]string, state vectorState, exp vectorExpect) vector {
		return vector{Name: name, Description: desc, SignedVersion: signed, SignedForAgent: signedFor,
			PresentedTo: presented, Request: r, Timestamp: vectorTime, Nonce: nonce(n),
			Headers: headers, State: state, Expect: exp}
	}
	ver := func(s string) map[string]string { return map[string]string{HeaderSignatureVersion: s} }
	verAud := func(s, aud string) map[string]string {
		return map[string]string{HeaderSignatureVersion: s, HeaderAudience: aud}
	}
	none := vectorState{}
	notV3 := func(reason string) vectorState { return vectorState{V3Unavailable: reason} }
	return []vector{
		v("v3-get-no-query", "GET without a query, signed for agent A and presented to agent A.",
			"3", vectorAgentA, vectorAgentA, req("GET", "/_portwing/info", ""), "1", ver("3"), none, accept),
		v("v3-get-with-query", "GET with a percent-encoded query; the target covers the raw query exactly as sent.",
			"3", vectorAgentA, vectorAgentA, req("GET", queryTarget, ""), "2", ver("3"), none, accept),
		v("v3-post-with-body", "POST with a JSON body; the body SHA-256 is part of the canonical bytes.",
			"3", vectorAgentA, vectorAgentA, req("POST", "/_portwing/compose", composeBody), "3", ver("3"), none, accept),
		v("v3-audience-header-matching", "Optional audience header naming this agent is accepted.",
			"3", vectorAgentA, vectorAgentA, req("GET", "/_portwing/info", ""), "4", verAud("3", vectorAgentA), none, accept),
		v("v3-cross-agent-without-audience-header", "Signed for agent A, replayed to agent B holding the same key: the bytes differ, so the signature fails.",
			"3", vectorAgentA, vectorAgentB, req("GET", queryTarget, ""), "5", ver("3"), none, reject("invalid-signature")),
		v("v3-cross-agent-with-audience-header", "Same replay, but the request names agent A as its audience: rejected with a distinct reason once the signature verifies under the named audience.",
			"3", vectorAgentA, vectorAgentB, req("GET", queryTarget, ""), "6", verAud("3", vectorAgentA), none, reject("audience-mismatch")),
		v("v3-signature-sent-as-v2", "A valid v3 signature presented with version header 2 never verifies.",
			"3", vectorAgentA, vectorAgentA, req("GET", queryTarget, ""), "7", ver("2"), none, reject("invalid-signature")),
		v("v3-signature-sent-as-legacy", "A valid v3 signature presented with no version header never verifies.",
			"3", vectorAgentA, vectorAgentA, req("GET", "/_portwing/info", ""), "8", nil, none, reject("invalid-signature")),
		v("v2-signature-sent-as-v3", "A valid v2 signature presented with version header 3 never verifies.",
			"2", "", vectorAgentA, req("GET", queryTarget, ""), "9", ver("3"), none, reject("invalid-signature")),
		v("legacy-signature-sent-as-v3", "A valid legacy signature presented with version header 3 never verifies.",
			"1", "", vectorAgentA, req("GET", "/_portwing/info", ""), "a", ver("3"), none, reject("invalid-signature")),
		v("v2-accepted-before-lock", "A v2 signature is still accepted while the key has not signed v3 and no minimum is set.",
			"2", "", vectorAgentA, req("GET", queryTarget, ""), "b", ver("2"), none, accept),
		v("legacy-accepted-before-lock", "A legacy query-free signature is still accepted under the same conditions.",
			"1", "", vectorAgentA, req("GET", "/_portwing/info", ""), "c", nil, none, accept),
		v("v2-refused-after-lock", "After the key has verified a v3 request, its valid v2 signature is refused.",
			"2", "", vectorAgentA, req("GET", queryTarget, ""), "d", ver("2"), vectorState{KeyLocked: true}, reject("signature-locked")),
		v("legacy-refused-after-lock", "After the key has verified a v3 request, its valid legacy signature is refused.",
			"1", "", vectorAgentA, req("GET", "/_portwing/info", ""), "e", nil, vectorState{KeyLocked: true}, reject("signature-locked")),
		v("v3-accepted-after-lock", "A locked key keeps working with v3.",
			"3", vectorAgentA, vectorAgentA, req("GET", "/_portwing/info", ""), "f", ver("3"), vectorState{KeyLocked: true}, accept),
		v("v3-refused-agent-id-not-v3-compatible", "AGENT_ID outside ^[a-z0-9-]{1,64}$: v3 is refused with a specific reason (only to a caller whose signature verifies; a forged one gets invalid-signature).",
			"3", vectorAgentA, vectorAgentA, req("GET", "/_portwing/info", ""), "12", ver("3"), notV3("agent-id-not-v3-compatible"), reject("agent-id-not-v3-compatible")),
		v("v3-refused-agent-id-not-persistent", "No AGENT_ID and no persistent state directory: v3 is refused with a specific reason.",
			"3", vectorAgentA, vectorAgentA, req("GET", "/_portwing/info", ""), "13", ver("3"), notV3("agent-id-not-persistent"), reject("agent-id-not-persistent")),
		v("v3-refused-state-not-writable", "AGENT_ID set but the state directory is not writable, so downgrade locks could not be durable: v3 is refused.",
			"3", vectorAgentA, vectorAgentA, req("GET", "/_portwing/info", ""), "15", ver("3"), notV3("state-not-writable"), reject("state-not-writable")),
		v("v2-accepted-when-v3-unavailable", "v2 keeps working while v3 is unavailable.",
			"2", "", vectorAgentA, req("GET", queryTarget, ""), "14", ver("2"), notV3("agent-id-not-persistent"), accept),
		v("v2-refused-by-min-version-3", "SIGNATURE_MIN_VERSION=3 refuses a valid v2 signature for every key.",
			"2", "", vectorAgentA, req("GET", queryTarget, ""), "10", ver("2"), vectorState{MinVersion: 3}, reject("signature-version-too-low")),
		v("v3-accepted-with-min-version-3", "SIGNATURE_MIN_VERSION=3 accepts v3.",
			"3", vectorAgentA, vectorAgentA, req("GET", "/_portwing/info", ""), "11", ver("3"), vectorState{MinVersion: 3}, accept),
	}
}

// marshalVectors renders the fixture with a trailing newline and without HTML
// escaping, so <agentId> stays readable.
func marshalVectors(f vectorFile) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(f); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func canonicalForVector(v vector) []byte {
	body := []byte(v.Request.Body)
	switch v.SignedVersion {
	case "3":
		return CanonicalMessageV3(v.SignedForAgent, v.Request.Method, v.Request.Target, BodyHashHex(body), v.Timestamp, v.Nonce)
	default:
		target := v.Request.Target
		if v.SignedVersion == "1" {
			target = strings.SplitN(target, "?", 2)[0]
		}
		return CanonicalMessage(v.Request.Method, target, BodyHashHex(body), v.Timestamp, v.Nonce)
	}
}

func buildVectorFile(t *testing.T) vectorFile {
	t.Helper()
	pub, priv := vectorKeyPair(t)
	f := vectorFile{
		Description: "Portwing request signature test vectors for the v3 (audience-bound) format. TEST ONLY: the key below is derived from a readable ASCII seed, authorizes nothing, and must never be used as a credential. Every vector is verified against an agent whose ID is presentedToAgent, with the clock pinned to nowUnix.",
		TestOnly:    true,
		Format: vectorFormat{
			V3:     "portwing-sig-v3\\n<agentId>\\nMETHOD\\nTARGET\\nBODY_SHA256\\nTIMESTAMP\\nNONCE",
			V2:     "METHOD\\nTARGET\\nBODY_SHA256\\nTIMESTAMP\\nNONCE",
			Legacy: "same as v2 with TARGET = path only; valid only for requests without a query",
			Notes:  "TARGET is the escaped path plus the raw query exactly as sent (for v2 and v3). BODY_SHA256 is lowercase hex of the body, e3b0c442... for an empty body. Signatures are Ed25519, base64url without padding. X-Portwing-Signature-Version selects the format. X-Portwing-Audience is optional; when present it must equal the agent ID or the request is rejected with audience-mismatch.",
		},
		Key: vectorKey{
			SeedASCII:       vectorSeedASCII,
			PublicKeyBase64: base64.StdEncoding.EncodeToString(pub),
			KeyID:           KeyIDForPublicKey(pub),
		},
		AgentIDRule: vectorRule{
			Pattern:   "^[a-z0-9-]{1,64}$",
			MinLength: 1,
			MaxLength: MaxAgentIDLength,
			Generated: "32 lowercase hex characters (16 random bytes)",
			Valid:     []string{"a", "agent-1", "0123456789abcdef0123456789abcdef", "550e8400-e29b-41d4-a716-446655440000", strings.Repeat("a", 64)},
			Invalid:   []string{"", "Agent", "agent_1", "agent.1", "agent 1", "agent/1", "agent\n1", strings.Repeat("a", 65)},
		},
		Agents: vectorAgents{A: vectorAgentA, B: vectorAgentB},
		Info: vectorInfo{
			Path:        "GET /_portwing/info",
			Example:     map[string]any{"agentId": vectorAgentA, "v3": true},
			Unavailable: map[string]any{"agentId": "Agent_ID", "v3": false, "v3Reason": "agent-id-not-v3-compatible"},
			Reasons: map[string]string{
				"agent-id-not-v3-compatible": "AGENT_ID is set to a value outside ^[a-z0-9-]{1,64}$. v3 requests answer 401 with this X-Portwing-Reason; v2 and legacy keep working.",
				"state-not-writable":         "AGENT_ID is set but the state directory cannot be written, so the downgrade locks could not survive a restart. v3 requests answer 401 with this X-Portwing-Reason; v2 and legacy keep working.",
				"agent-id-not-persistent":    "No AGENT_ID and no persistent state directory, so the generated agent ID changes on restart. v3 requests answer 401 with this X-Portwing-Reason; v2 and legacy keep working.",
			},
			Notes: "A v3-specific refusal is only ever returned to a caller whose signature verified; a forged signature for a known key gets invalid-signature, while an unknown key ID gets unknown-key. The response carries more fields (version, mode, capabilities, ...). During initial negotiation, a controller can read agentId and v3 with a v2 signature while its key is unlocked and the minimum version permits v2. With SIGNATURE_MIN_VERSION=3 or a v3-locked key, it must already know the agent ID and sign the info request with v3. Start signing v3 only when v3 is true, and treat a missing v3 field as an older agent without v3. v3Reason is present only when v3 is false. Pin v3 per agent: once one v3 request to an agent has been accepted, never go back to v2 or legacy for it without operator action, not after an invalid-signature and not because a later info response says v3 is false, since the info body is unsigned and a 401 reason can be forged when TLS is terminated in between. The per-key lock is first-contact trust, and it needs a durable state volume to survive a replaced container. SIGNATURE_MIN_VERSION=3 is the recommended end state once every controller signs v3.",
		},
		Now: vectorTime,
	}
	for _, v := range vectorSpecs() {
		msg := canonicalForVector(v)
		v.BodySHA256 = BodyHashHex([]byte(v.Request.Body))
		v.CanonicalString = string(msg)
		v.CanonicalHex = hex.EncodeToString(msg)
		v.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, msg))
		f.Vectors = append(f.Vectors, v)
	}
	return f
}

func TestSignatureV3Vectors(t *testing.T) {
	if os.Getenv("UPDATE_SIGNATURE_VECTORS") == "1" {
		data, err := marshalVectors(buildVectorFile(t))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(vectorsPath, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	raw, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Fatalf("read %s: %v (regenerate with UPDATE_SIGNATURE_VECTORS=1)", vectorsPath, err)
	}
	var f vectorFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if !f.TestOnly || f.Now != vectorTime || len(f.Vectors) == 0 {
		t.Fatalf("fixture header wrong: testOnly=%v now=%d vectors=%d", f.TestOnly, f.Now, len(f.Vectors))
	}

	// The committed file must equal what the spec list generates, so neither
	// can drift from the other.
	want, _ := marshalVectors(buildVectorFile(t))
	got, _ := marshalVectors(f)
	if string(want) != string(got) {
		t.Fatal("fixture is stale; regenerate with UPDATE_SIGNATURE_VECTORS=1")
	}

	pub, _ := vectorKeyPair(t)
	if f.Key.PublicKeyBase64 != base64.StdEncoding.EncodeToString(pub) || f.Key.KeyID != KeyIDForPublicKey(pub) {
		t.Fatal("key section does not match the seed")
	}

	for _, rule := range f.AgentIDRule.Valid {
		if err := ValidateAgentID(rule); err != nil {
			t.Errorf("rule.valid %q rejected: %v", rule, err)
		}
	}
	for _, rule := range f.AgentIDRule.Invalid {
		if ValidateAgentID(rule) == nil {
			t.Errorf("rule.invalid %q accepted", rule)
		}
	}

	pinVerifyClock(t, time.Unix(f.Now, 0))
	for _, v := range f.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			// Canonical bytes: hex and string forms agree with each other and
			// with the production formatter.
			hexBytes, err := hex.DecodeString(v.CanonicalHex)
			if err != nil || string(hexBytes) != v.CanonicalString {
				t.Fatalf("canonical hex/string disagree: %v", err)
			}
			if string(canonicalForVector(v)) != v.CanonicalString {
				t.Fatalf("canonical bytes differ from the production formatter\n got %q\nwant %q", canonicalForVector(v), v.CanonicalString)
			}
			if v.SignedVersion == "3" && !strings.HasPrefix(v.CanonicalString, "portwing-sig-v3\n"+v.SignedForAgent+"\n") {
				t.Fatalf("v3 canonical string lacks the domain tag and audience: %q", v.CanonicalString)
			}
			sig, err := base64.RawURLEncoding.DecodeString(v.Signature)
			if err != nil || !ed25519.Verify(pub, hexBytes, sig) {
				t.Fatal("recorded signature does not verify over the canonical bytes")
			}

			verdict := verifyVector(t, f, v)
			if v.Expect.Accept {
				if verdict != nil {
					t.Fatalf("want accept, got %v", verdict)
				}
				return
			}
			if verdict == nil {
				t.Fatal("want reject, got accept")
			}
			if ReasonFor(verdict) != v.Expect.Reason {
				t.Fatalf("reason = %q (%v), want %q", ReasonFor(verdict), verdict, v.Expect.Reason)
			}
		})
	}
}

// verifyVector runs one vector through VerifyRequestDetailed against a fresh
// agent whose ID is the vector's presentedToAgent.
func verifyVector(t *testing.T, f vectorFile, v vector) error {
	t.Helper()
	pubRaw, err := base64.StdEncoding.DecodeString(f.Key.PublicKeyBase64)
	if err != nil {
		t.Fatal(err)
	}
	reg := NewKeyRegistry("")
	reg.keys[f.Key.KeyID] = &AuthorizedKey{KeyID: f.Key.KeyID, PubKey: pubRaw}
	locks := NewMemorySignatureLocks()
	if v.State.KeyLocked {
		if _, err := locks.Lock(f.Key.KeyID, time.Unix(f.Now, 0)); err != nil {
			t.Fatal(err)
		}
	}
	reg.SetSignaturePolicy(SignaturePolicy{AgentID: v.PresentedTo, MinVersion: v.State.MinVersion, V3Unavailable: v.State.V3Unavailable, Locks: locks})
	lru := NewNonceLRU(100, 60)
	t.Cleanup(lru.Close)

	r := httptest.NewRequest(v.Request.Method, v.Request.Target, strings.NewReader(v.Request.Body))
	r.Header.Set(HeaderKeyID, f.Key.KeyID)
	r.Header.Set(HeaderTimestamp, jsonInt(v.Timestamp))
	r.Header.Set(HeaderNonce, v.Nonce)
	r.Header.Set(HeaderSignature, v.Signature)
	for k, val := range v.Headers {
		r.Header.Set(k, val)
	}
	_, err = VerifyRequestDetailed(r, []byte(v.Request.Body), reg, lru, 60)
	return err
}

func jsonInt(n int64) string { return strconv.FormatInt(n, 10) }

func TestSignatureV3Vectors_CoverEveryVerdictClass(t *testing.T) {
	t.Parallel()
	reasons := map[string]bool{}
	accepts := 0
	for _, v := range vectorSpecs() {
		if v.Expect.Accept {
			accepts++
		} else {
			reasons[v.Expect.Reason] = true
		}
	}
	for _, r := range []string{"invalid-signature", "audience-mismatch", "signature-locked", "signature-version-too-low", "agent-id-not-v3-compatible", "agent-id-not-persistent", "state-not-writable"} {
		if !reasons[r] {
			t.Errorf("no vector expects rejection reason %q", r)
		}
	}
	if accepts < 4 {
		t.Errorf("only %d accepting vectors", accepts)
	}
}
