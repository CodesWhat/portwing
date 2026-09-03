# ML-DSA for the Edge Hello and Request Signatures — Design

**Status:** Deferred (decision recorded; no code change)  
**Author:** PW-5.13a evaluation  
**Branch:** `docs/pqc-edge-auth-design`  
**Date:** 2026-09-03  
**Related:** `docs/design/ed25519-auth.md`, `docs/security-model.md`, `COMPETITIVE-LANDSCAPE.md`

---

## 1. Decision

**Defer adopting ML-DSA on both signed surfaces. Do not adopt pure ML-DSA-87
anywhere.** The two surfaces get different answers when the trigger fires, and
the difference is the whole point of this note:

- **Edge hello (once per connection):** adopt **hybrid Ed25519 + ML-DSA-87**
  when Drydock ships a verifier. The cost is 4,627 signature bytes and about
  360 microseconds, once, on a connection that lives for hours. Because the
  hello is the only signed message in edge mode, upgrading it makes the whole
  edge session post-quantum authenticated.
- **Standard-mode per-request signature:** **do not adopt, in any form.** It
  would add about 6,084 bytes to every request forever, roughly 24 times the
  current signed-request header block, to defend a threat that is a key
  lifetime problem rather than a per-message one. If standard mode ever needs a
  post-quantum identity, the answer is a post-quantum authenticated session or
  mTLS layer that mints a short-lived credential, not a 6 KB header on every
  Docker API call.
- **Today, with no wire change:** publish a key rotation cadence. The real
  exposure is bounded by how long an agent identity key stays in service, and
  rotation is the mitigation that actually matches that shape. Section 8.

This is a defer with the target design already settled, so the work is a
scheduled change rather than a fresh evaluation. Triggers are in section 7.

---

## 2. What is actually signed today

Two messages carry a signature, and both cross the Portwing/Drydock boundary in
opposite directions. Neither can be changed by one repo alone.

### 2.1 Edge hello — Portwing signs, Drydock verifies

`internal/edge/client.go:645-676` (`signHello`) signs a fixed canonical string
once per WebSocket connection:

```text
GET\n/api/portwing/ws\n<sha256 of empty string>\n<unix seconds>\n<32-hex nonce>
```

That is 129 bytes. Method, path, and body hash are constants; only the
timestamp and nonce vary. The signature goes into `HelloMessage.Signature` as
base64url (`internal/protocol/messages.go:58-77`), and signing failure is fatal
rather than a fallback to token auth (`internal/edge/client.go:602-608`).

**Nothing after the hello is signed.** The WebSocket carries requests,
responses, streams, and exec traffic with no per-message signature, so the
hello is a session-establishing handshake and WSS provides integrity for
everything that follows. This is why the hello is the cheap surface: one
signature buys authentication for the entire connection.

Drydock verifies it in `app/api/portwing-ws.ts`, reconstructing the identical
five-field canonical string and calling `verify(null, canonical, pubKey,
sigBuf)`. The algorithm is chosen entirely by the key, and the key is forced to
Ed25519 by a hardcoded SPKI prefix constant `302a300506032b6570032100`.

### 2.2 Standard-mode request signature — Drydock signs, Portwing verifies

`internal/auth/verify.go` (`VerifyRequest`) verifies a signature over
`METHOD\nREQUEST-TARGET\nbody-sha256-hex\nunix-timestamp\nnonce` on **every**
HTTP request, after a timestamp skew check and a nonce LRU replay check. Five
headers carry it: `X-Portwing-Key-ID`, `X-Portwing-Timestamp`,
`X-Portwing-Nonce`, `X-Portwing-Signature`, and `X-Portwing-Signature-Version`.

Standard mode has no session. Each request is independently authenticated, so
there is no handshake to upgrade and no way to amortise a signature across
requests. That is the structural reason the two surfaces price so differently.

### 2.3 Key format and identity

Both sides use the same `authorized_keys` line shape,
`ed25519 <standard-base64 pubkey> [comment]`, and both hard-reject anything
else. Portwing's parser rejects a first field that is not the literal
`ed25519` and a decoded key that is not exactly 32 bytes
(`internal/auth/keys.go:135-165`). Drydock rejects the same two things, with
the 32-byte check appearing in three separate places.

The key ID is `hex(SHA-256(raw public key)[:8])`, 16 hex characters
(`internal/auth/keygen.go:90-93`). It hashes bare key bytes with **no algorithm
domain separation**, which is a real obstacle to a second algorithm: two keys of
different types would be indistinguishable by ID alone. Any hybrid design has to
fix this, and fixing it is a shared-format change, not a local one.

Portwing reloads `authorized_keys` on SIGHUP (`internal/server/http.go:245-266`)
and the registry holds many keys at once. Drydock's key store supports
revocation through a `revokedAt` field. Overlap rotation is therefore already
possible on both sides with no code change — see section 8.

---

## 3. Measured numbers

Measured on this machine with a throwaway benchmark, not committed. Reproduce
with the recipe in section 9.

- **Machine:** Apple M4 Pro, 14 cores, `darwin/arm64`.
- **Toolchain:** `go1.27.1`, `crypto/mldsa` and `crypto/ed25519` from the
  standard library.
- **Method:** `go test -bench . -benchtime 2s -count 5`, median of 5 reported.
- **Message:** the real 129-byte edge-hello canonical string from section 2.1.

Note these are **not** comparable to `BENCHMARKS.md`, which was measured on a
GitHub-hosted Xeon under `go1.26.6`. They are internally consistent with each
other, which is what the ratios below depend on.

### 3.1 Sizes

| Scheme | Public key | Signature | Signature as base64url | Public key as base64 |
| --- | --- | --- | --- | --- |
| Ed25519 | 32 B | 64 B | 86 chars | 44 chars |
| ML-DSA-65 | 1,952 B | 3,309 B | 4,412 chars | 2,604 chars |
| ML-DSA-87 | 2,592 B | 4,627 B | 6,170 chars | 3,456 chars |

ML-DSA-87 is 72 times Ed25519's signature and 81 times its public key.
ML-DSA-65 is not a meaningful saving here: it is still 51 times the signature
size, so it trades a security category for a rounding error on the number that
actually hurts. If the cost is acceptable at all, take the higher category.

One pleasant surprise: a Go `mldsa.PrivateKey` serialises as a **32-byte seed**,
the same size as an Ed25519 seed. The private key at rest in `PRIVATE_KEY_FILE`
does not grow. Only the public half and the signature do.

### 3.2 Timing

| Operation | Ed25519 | ML-DSA-65 | ML-DSA-87 | Hybrid (Ed + 87) |
| --- | --- | --- | --- | --- |
| Sign | 13.4 us | 317.8 us | 364.5 us | 408.1 us |
| Verify | 27.0 us | 81.2 us | 140.4 us | 165.0 us |
| Keygen | 13.2 us | not measured | 210.3 us | not measured |

ML-DSA-87 verify is 5.2 times Ed25519 verify; hybrid verify is 6.1 times. Sign
is worse in ratio (27 and 30 times) but sign happens on the client, once per
connection for the hello.

Allocation behaviour is good: ML-DSA-87 verify is 0 B/op and 0 allocs/op, the
same as Ed25519 verify. Sign is 4,864 B/op in a single allocation.

**CPU is not the blocker.** Even at 140 us, a single core does about 7,100
ML-DSA-87 verifies per second, and Portwing is a single-agent sidecar, not a
fleet-wide verification service. `BENCHMARKS.md` publishes no request rate at
all and explicitly disclaims throughput measurement, so there is no fleet number
to multiply against. The honest framing is the ratio, and the ratio is
affordable. Bytes are the problem, not cycles.

Worth stating plainly because it is easy to misread: the published
`AuthMiddleware/authorized_raw` figure of 433.4 ns in `BENCHMARKS.md` is the
**raw-token** path. `internal/server/middleware_bench_test.go:59` passes an empty
`Ed25519Config{}`, so no signature is verified in that benchmark. The signature
verification already dominates the middleware today, at roughly 62 times the
whole measured token path.

### 3.3 Per-request byte cost, standard mode

Header block for the five signature headers, name plus `": "` plus value plus
CRLF:

| Configuration | Signature header block | Delta per request |
| --- | --- | --- |
| Ed25519 today | 266 B | baseline |
| Pure ML-DSA-87 | 6,350 B | +6,084 B |
| Hybrid Ed25519 + ML-DSA-87 | 6,503 B | +6,237 B |

That is roughly 6.1 MB of pure signature overhead per 1,000 requests. A
6,170-character single header value also sits close to the 8 KB per-header-line
limit that a default nginx `large_client_header_buffers 4 8k` imposes. Portwing's
own Go server does not set `MaxHeaderBytes`, so it takes Go's 1 MB default and
would accept it, but any reverse proxy in front of an agent becomes a
deployment-specific compatibility question that does not exist today.

### 3.4 Once-per-connection byte cost, edge hello

The hello frame grows by the same 6,084 bytes, once, against a 16 MB WebSocket
read limit (`internal/edge/client.go:45`). It is not measurable in practice.
An `authorized_keys` line grows from roughly 55 bytes to roughly 3,470.

This is the entire case for splitting the decision by surface.

### 3.5 Cross-implementation interoperability, verified

Go 1.27's `crypto/mldsa` and Node 24.20's `node:crypto` (OpenSSL 3.6.4) produce
**mutually verifiable** ML-DSA-87 signatures with an empty context. Both
directions were tested and both pass:

- Node verified a Go-produced signature over the hello canonical string.
- Go verified a Node-produced signature over the same string.

Node needs the raw 2,592-byte public key wrapped in SPKI DER, exactly as
Drydock already does for Ed25519. The prefix is a fixed 22 bytes:

```text
30820a32300b060960864801650304031303820a2100
```

Drydock runs `node:24-alpine` and requires `node >= 24.0.0`, so this is
available to it today. **Neither side needs a new dependency.** That removes
what would otherwise have been the strongest reason to defer, and it is why the
trigger in section 7 is cheap to hit rather than years away.

---

## 4. The threat, stated honestly

**Harvest-now-decrypt-later does not apply to signatures.** There is no
plaintext behind a signature to recover later. A captured hello or signed
request is already useless for replay: the timestamp window and the nonce LRU
reject it, on both surfaces, today.

The real exposure is narrower and worth naming precisely. An agent identity key
is long-lived, and its public half is deliberately distributed: it sits in
Drydock's key store and in `authorized_keys` files on both hosts. An attacker
who holds that public key and later obtains a cryptographically relevant quantum
computer, **while that same key is still in service**, can recover the private
key and impersonate the agent from then on. The exposure window is the key's
remaining service life intersected with CRQC availability. It is not the
lifetime of any captured traffic.

Two consequences follow, and they drive the decision.

First, **shortening the key's service life attacks the exposure directly**,
which is what makes rotation a real mitigation rather than a consolation prize.
Section 8.

Second, **the property has to attach to the identity, not to a message
subset**, and in standard mode each request is independently authenticated, so
every request would need the post-quantum signature for the property to hold.
There is no partial credit. Signing only some requests, or only the hello while
standard mode stays classical, protects nothing. This is precisely why the
per-request cost cannot be avoided by sampling or by upgrading one surface and
calling standard mode covered.

### 4.1 What is already post-quantum

Worth recording so the gap is not overstated. The agent's outbound edge dial
uses `&tls.Config{MinVersion: tls.VersionTLS12}` with no `CurvePreferences`
override (`internal/edge/client.go:346-350`), so it takes Go's default key
exchange preferences. From Go 1.24 those include the `X25519MLKEM768` hybrid
post-quantum key exchange, and from Go 1.26 also `SecP256r1MLKEM768` and
`SecP384r1MLKEM1024`. Subject to the controller offering one of them, **the
harvest-now-decrypt-later-relevant defence is already on by default** and needs
no work here.

So the confidentiality of edge traffic against a future CRQC is handled at the
transport. What remains is authentication, which is the part that was never an
HNDL problem. That is the correct order of operations, and it is the reason
this item is a low-priority scheduled change rather than an urgent gap.

---

## 5. Options compared

### 5.1 Pure ML-DSA-87

**Rejected.**

It is an immediate hard break with every deployed Drydock, on both surfaces at
once. Drydock rejects a hello signature longer than 200 characters before it
ever reaches verification, with a comment noting Ed25519 base64url signatures
are exactly 86 characters; an ML-DSA-87 signature is 6,170. The `authorized_keys`
parsers on both sides reject a non-`ed25519` algorithm field and a public key
that is not exactly 32 bytes. Drydock's agent config validates `authmode`
against a two-value allowlist.

Beyond compatibility, it removes a mature primitive in exchange for one that
was standardised in August 2024, on a surface where the classical primitive is
not weak against any attacker that exists. A lattice break would be total
against a pure deployment. The comparison to Arcane does not carry: Arcane's
ML-DSA-87 change is inside an opt-in mTLS feature that is off unless
`EDGE_MTLS_MODE` is set, so matching it would not mean matching a default.

### 5.2 Hybrid Ed25519 + ML-DSA-87

**Right shape, wrong time to ship.**

An AND-composition, verifying both signatures and failing closed if either
fails, is at least as strong as the stronger of the two. It survives both a
CRQC and a lattice break, which is the correct posture for a primitive this
young. It is also the only shape that can be deployed additively, since the
existing 86-character `signature` field stays valid and the post-quantum
material goes in new fields Drydock currently ignores.

The temptation is to ship the additive half now so the fleet already emits it
when Drydock catches up. **That is a trap, for two reasons.** An emitted but
unverified signature provides exactly zero security while costing bytes and CPU,
and it would read in a changelog as post-quantum protection that does not exist.
More seriously, it would commit Portwing to a wire shape before the counterparty
has designed theirs, and the counterparty's design is the one that must carry the
key ID domain-separation fix from section 2.3. Two independently invented
"hybrid" formats is a worse outcome than waiting.

### 5.3 Defer

**Chosen.** With the target design settled in section 6 and triggers in
section 7, so this is a scheduled change rather than an open question.

---

## 6. Target design, for when the trigger fires

Recorded now so the evaluation is not repeated.

1. **Edge hello: hybrid, additive.** Add `pubKeyIdPq` and `signaturePq` to
   `HelloMessage`. The existing `pubKeyId` and `signature` keep their meaning
   and their 86-character size, so an older controller keeps working unchanged.
   Both signatures cover the identical existing canonical string; no new
   canonicalisation and no new replay logic.
2. **Verification is fail-closed AND.** When a controller advertises the
   post-quantum capability in its welcome frame and the agent has a
   post-quantum key registered, both signatures must verify. Never accept one
   valid signature alongside one invalid one, and never let a missing
   post-quantum signature silently downgrade a key that has one registered.
3. **Key ID gets algorithm domain separation.** The current
   `hex(SHA-256(raw pubkey)[:8])` collides conceptually across algorithms.
   Derive the post-quantum ID over the algorithm label and the key together,
   for example `hex(SHA-256("ml-dsa-87" || raw pubkey)[:8])`. This is a shared
   format decision and belongs in the Drydock-side design, not here.
4. **`authorized_keys` gains a second algorithm token**, `ml-dsa-87`, with a
   2,592-byte length check beside the existing 32-byte one, on both sides. A
   single agent identity is then two lines that share a comment.
5. **Standard mode stays Ed25519.** If a post-quantum identity is ever required
   there, do it as a post-quantum authenticated session or mTLS handshake that
   issues a short-lived credential. A credential with a service life measured
   in minutes is not a CRQC concern, which is exactly the property section 4
   establishes. Do not put ML-DSA in a per-request header.

---

## 7. Revisit triggers

Any one of these reopens the decision. Each is checkable rather than a vibe.

1. **Drydock ships an ML-DSA verifier.** Concretely: the hardcoded Ed25519
   SPKI prefix constant in `app/api/portwing-ws.ts` gains a second algorithm,
   the 200-character hello signature guard is raised, and the `agent-keys`
   record grows an algorithm field beside its 32-byte public key check. This is
   the primary trigger and the cheapest to hit, because section 3.5 shows no
   new dependency is needed on either side.
2. **A user or compliance requirement lands** that names FIPS 204 on the agent
   identity. CNSA 2.0 is the date anchor worth watching even though it binds
   National Security Systems rather than a self-hosted Docker agent; its
   signature timelines run to 2030 and 2033. Note that CNSA 2.0 prefers pure
   ML-DSA while European guidance prefers hybrid, so if this trigger fires the
   pure-versus-hybrid call in section 5 has to be re-argued rather than
   inherited.
3. **Go's `crypto/tls` gains post-quantum signature algorithm support** for
   certificates. If the transport can carry a post-quantum identity, the
   cheaper answer for standard mode is mTLS with a post-quantum certificate
   rather than an application-layer header, and target design item 5 should be
   revisited first.
4. **A credible CRQC estimate lands inside the rotation window** established in
   section 8. If keys rotate annually, an estimate that moves inside that
   horizon collapses the argument that rotation is sufficient.

---

## 8. What closes the gap today: rotation

The exposure in section 4 is bounded by an identity key's service life, and
Portwing already has everything needed to bound it. This costs no wire change,
no new dependency, and no Drydock coordination.

What already works:

- The registry holds many keys at once, so a new key can be trusted before the
  old one is withdrawn (`internal/auth/keys.go`).
- Portwing reloads `authorized_keys` on SIGHUP with no restart and no dropped
  connections (`internal/server/http.go:245-266`).
- Drydock's key store supports revocation through `revokedAt` and rejects a
  revoked key at hello time.

Recommended guidance, to be added to `docs/security-model.md`:

1. **Rotate each agent identity key at least annually**, and immediately on any
   suspected host compromise. The point is to keep every key's service life
   comfortably shorter than any credible CRQC horizon, so a key that is
   attackable in 2035 was retired years earlier.
2. **Rotate with overlap, never with a gap.** Generate the new keypair, add its
   public key to `authorized_keys` on both sides, SIGHUP Portwing, cut the agent
   over to the new `PRIVATE_KEY_FILE`, confirm connections are authenticating on
   the new key ID, then remove the old key and revoke it on the Drydock side.
   Both key IDs are logged on add and remove, so the cutover is observable.
3. **Treat the public key as sensitive-adjacent.** It is not a secret, but it is
   the input to the attack in section 4, so keeping it off public surfaces
   shrinks the population of attackers who could ever use a CRQC against it.
4. **Keep the blast radius per key.** One key per agent, never a shared fleet
   key, so a single recovered key impersonates one agent rather than all of
   them. Drydock already binds an agent name to a key ID and rejects a
   mismatched claim.

Rotation is not a lesser substitute for post-quantum signatures. For this
specific threat it attacks the exposure window directly, and it is available now
rather than after a two-repo protocol change.

---

## 9. Reproducing the measurements

Nothing in this section is committed. To re-measure, create a throwaway module
outside the repo with `go 1.27` and a benchmark that:

1. Signs the 129-byte canonical string from section 2.1 with
   `ed25519.Sign` and with `mldsa.GenerateKey(mldsa.MLDSA87()).Sign` using an
   empty `&mldsa.Options{}`.
2. Reports `len(sig)`, `len(pk.Bytes())`, and their base64url and standard-base64
   encoded lengths.
3. Benchmarks sign, verify, and keygen for Ed25519, ML-DSA-65, and ML-DSA-87,
   plus a hybrid case that signs with both and verifies both.
4. Runs `go test -run '^$' -bench . -benchtime 2s -count 5` and takes the median.

For the interoperability check in section 3.5, have the Go side write the raw
public key, signature, and message as hex; on the Node side wrap the raw public
key with the 22-byte SPKI prefix, `createPublicKey({format: "der", type:
"spki"})`, and `verify(null, msg, pubKey, sig)`. Then reverse the direction with
`generateKeyPairSync("ml-dsa-87")` and `mldsa.NewPublicKey` on the Go side.

Re-run this before acting on any trigger in section 7. The numbers are toolchain
and hardware specific, and `crypto/mldsa` is new enough that its performance is
likely to improve.
