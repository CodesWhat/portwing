# ML-DSA for the Edge Hello and Request Signatures — Design

**Status:** Deferred (decision recorded; no code change)  
**Author:** PW-5.13a evaluation  
**Branch:** `docs/pqc-edge-auth-design`  
**Date:** 2026-09-03  
**Related:** `docs/design/ed25519-auth.md`, `docs/security-model.md`, `COMPETITIVE-LANDSCAPE.md`

**Revised 2026-09-03 after review.** The decision is unchanged. Six corrections
landed and each is marked in place rather than quietly rewritten, because
several were overclaims and the record of what was wrong is worth keeping: the
scope of what a hybrid hello buys (§4.2), the identity binding a hybrid
composition requires (§5.2, §6.3), the impossibility of negotiating on the
welcome frame (§6.1), the fact that Go's post-quantum certificate support has
already landed (§7.3), the edge-mode rotation sequence (§8.2), and three
measurement or framing errors (§3.1, §3.2, §3.4).

---

## 1. Decision

**Defer adopting ML-DSA on both signed surfaces. Do not adopt pure ML-DSA-87
anywhere.** The two surfaces get different answers when the trigger fires, and
the difference is the whole point of this note:

- **Edge hello (once per connection):** adopt **hybrid Ed25519 + ML-DSA-87**
  when Drydock ships a verifier. The cost is a 4,627-byte signature and about
  360 microseconds, once, on a connection that lives for hours. What that buys
  is precisely **agent-to-controller authentication of the hello**, and nothing
  more. It is not a post-quantum edge session; section 4.2 sets out what else
  would be required and why this note does not claim it.
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
hello is the only application-layer authentication in edge mode and WSS carries
everything that follows. This is why the hello is the cheap surface to upgrade:
one signature, once per connection, rather than one per message.

Two limits on what that signature proves, both load-bearing for section 4.2.
The hello authenticates the **agent to the controller only**; nothing
authenticates the controller back to the agent except the TLS server
certificate, which `internal/config/config.go:147-149` states outright. And the
hello has **no channel binding**: the canonical string covers a fixed method,
path, and empty-body hash plus a timestamp and nonce, with nothing tying it to
the TLS connection it travels over.

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
(`internal/auth/keygen.go:90-93`), and Drydock derives it identically. It
identifies **a key**, not an agent identity. That is fine while an identity is
exactly one key, and it is the root of the binding problem in section 5.2 the
moment an identity is two keys. Section 6 item 3 resolves it.

Portwing reloads `authorized_keys` on SIGHUP (`internal/server/http.go:245-266`)
and the registry holds many keys at once, so **standard mode** supports overlap
rotation today with no code change.

**Edge mode does not**, and an earlier draft of this note said it did. Drydock
binds an agent name to a single key ID in a `Map<string, {keyId}>`
(`app/api/portwing-ws.ts:151`) and rejects a hello whose `agentName` is already
bound to a different `pubKeyId` with `agent-name-claimed`
(`app/api/portwing-ws.ts:763-781`). The binding is durable across controller
restarts, is **not** released on disconnect, and is cleared only by revoking the
owning key or by idle eviction after 24 hours under cap pressure. A second live
session under one name is separately refused as `agent-already-connected`. The
consequence for rotation is in section 8.2, and it is not the sequence this note
originally gave.

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

ML-DSA-65's signature is genuinely smaller, by 28.5 percent (1,318 bytes), and
its public key by 24.7 percent. That is a real saving and it is worth not
dismissing. It just does not change either decision, because neither decision
turns on a number in that range. On the per-request surface, 3,309 bytes is
still 51 times Ed25519 and still lands the header block in the same
multiple-kilobyte class, with the same proxy-limit and per-request-cost problems
intact; 28.5 percent off a cost that was rejected as an order-of-magnitude
mistake does not rescue it. On the hello, where the cost is already acceptable,
saving 1,318 bytes once per connection buys nothing worth a lower security
category. So the saving is real and the conclusion is unchanged: if the cost is
acceptable at all, take the higher category.

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

**CPU is not the blocker on the agent.** Even at 140 us, a single core does
about 7,100 ML-DSA-87 verifies per second, and Portwing is a single-agent
sidecar, not a fleet-wide verification service. `BENCHMARKS.md` publishes no
request rate at all and explicitly disclaims throughput measurement, so there is
no fleet number to multiply against.

That claim needs qualifying on the other side, and an earlier draft did not
qualify it. **Drydock signs centrally for the whole fleet**, and signing is the
expensive direction: 364.5 us against Ed25519's 13.4 us, a factor of 27, on a
single-threaded Node event loop. A controller driving N agents pays that on every
standard-mode request it issues, so the per-request option's CPU cost lands
almost entirely on the controller rather than on the agent measured here. That
does not change the verdict, because the byte cost already rejects the
per-request option on its own, but "CPU is affordable" is an agent-side statement
and should not be read as a fleet-side one. On the hello, where signing is once
per connection, it is a non-issue in both directions.

Bytes are the problem, not cycles.

Worth stating plainly because it is easy to misread: the published
`AuthMiddleware/authorized_raw` figure of 433.4 ns in `BENCHMARKS.md` is the
**raw-token** path. `internal/server/middleware_bench_test.go:59` passes an empty
`Ed25519Config{}`, so no signature is verified in that benchmark. Signature
verification therefore dominates the authenticated request path by about two
orders of magnitude, and ML-DSA-87 would multiply that already-dominant term by
5.2. Read that as a magnitude and not a ratio: the 433.4 ns figure was measured
on a different machine and toolchain from the numbers above, which is exactly
the comparison this section warned against making precisely.

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

The recommended shape adds a `signaturePq` field beside the existing
`signature` rather than replacing it, so the delta is **+6,187 bytes** of JSON
(the 6,170-character base64url value, its quotes, the `"signaturePq":` key, and
the separating comma), not the 6,084-byte replacement delta used for standard
mode in 3.3. An earlier draft quoted the replacement figure here, which
understated the additive design by about 100 bytes.

The governing limit is **Drydock's**, not Portwing's: the controller's
`WebSocketServer` is constructed with `maxPayload: MAX_PAYLOAD_BYTES`, 16 MB
(`app/api/portwing-ws.ts:68, 458`). Portwing's own
`conn.SetReadLimit(maxReadSize)` at `internal/edge/client.go:386`, with the
constant at `:45`, caps frames the agent *receives*, so it does not govern a
hello the agent sends, and an earlier draft cited it as though it did. The two happen to be the same 16 MB, which is
why the error was invisible. A 6 KB hello against a 16 MB cap is not measurable
in practice either way.

A hybrid `authorized_keys` line grows from roughly 55 bytes to roughly 3,520.

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

So the confidentiality of edge traffic against a **passive** future CRQC is
handled at the transport. What remains is authentication, which is the part that
was never an HNDL problem. That is the correct order of operations, and it is
the reason this item is a low-priority scheduled change rather than an urgent
gap.

### 4.2 What a hybrid hello does not buy

An earlier draft of this note claimed a hybrid hello would make the edge session
post-quantum authenticated. That is wrong and worth stating plainly, because it
is the kind of overclaim that stops further work from happening.

A hybrid hello defends exactly one thing: an attacker who recovers the agent's
identity private key from its public half cannot then impersonate that agent to
the controller. That is agent-to-controller authentication of a single message.

It does **not** defend the session against an active attacker, for two reasons
from section 2.1. Controller authentication rests entirely on the TLS server
certificate, whose signature is classical X.509. An attacker who can forge that
certificate, which is the same CRQC capability, becomes a machine-in-the-middle:
the agent completes a TLS handshake with the attacker, and the hybrid PQ key
exchange from section 4.1 protects that connection's confidentiality against a
passive observer while doing nothing about the endpoint being wrong. Second,
because the hello carries no channel binding, the attacker can relay the agent's
perfectly valid hello, hybrid signature and all, onto its own connection to the
real controller, and then drive an unsigned session against dockerd. Upgrading
the hello's signature algorithm does not touch either problem.

Mutual post-quantum authentication of the edge session would additionally need:

1. **Post-quantum controller authentication.** A post-quantum or hybrid
   certificate chain for the controller, so the agent's peer verification does
   not rest on a classical signature. Section 7 trigger 3 covers the toolchain
   side of this, which is further along than expected.
2. **Channel binding.** The hello canonical string extended to cover a TLS
   exporter value for the connection, so a relayed hello is invalid on any
   channel but the one it was signed for. This is cheap and worth doing on its
   own merits, independent of any post-quantum work, because it closes the relay
   above against a classical machine-in-the-middle too.
3. **Post-quantum session integrity**, or an explicit decision that TLS record
   integrity is sufficient once the endpoints are authenticated.

None of that is in scope here, and item 2 is arguably the highest-value item in
this whole note. It is filed as its own roadmap concern rather than folded into
the ML-DSA decision, because it is not a post-quantum problem.

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
fails, is at least as strong as the stronger of the two **provided both keys
are bound to a single identity**. That proviso is the whole design, not a
footnote, and an earlier draft of this note dropped it while proposing two
independently registered key IDs joined by a shared comment. That shape is
broken: an attacker holding a victim's CRQC-recovered Ed25519 key and their own
legitimately enrolled ML-DSA key presents one of each, both verify against their
respective registered keys, and the AND passes. The composition is then only as
strong as its weakest key, which is the opposite of the intended property.
Section 6 item 3 fixes this by deriving the identity's ID over both public keys
at once, so a key ID resolves to a pair and keys from two records cannot be
combined. With that binding in place the composition survives both a CRQC and a
lattice break, which is the correct posture for a primitive this young.

It is also the only shape that can be deployed additively, since the existing
86-character `signature` field stays valid and the post-quantum material goes in
a new field Drydock currently ignores.

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

1. **Edge hello: hybrid, additive, sent unconditionally.** Add `signaturePq`
   to `HelloMessage` alongside the existing `signature`, which keeps its
   meaning and its 86-character size. There is no second key ID field; see
   item 3. The agent sends both signatures on every hello whenever it holds a
   hybrid identity, and does **not** try to negotiate first.

   An earlier draft gated this on a controller capability in the welcome
   frame. That is not implementable: `internal/edge/client.go:393` sends the
   hello and only then waits for the welcome at `:398`, so nothing about the
   controller is known when the hello is built. Sending unconditionally is
   safe against older controllers, which was verified rather than assumed:
   Drydock parses the hello with a bare `JSON.parse` and per-field `typeof`
   checks (`app/api/portwing-ws.ts:545-560`), with no schema validator, no
   `strict`, and no unknown-key rejection anywhere in that path, so extra
   fields are ignored. Its auth-mode detector inspects only the four classic
   fields, so a hybrid hello still takes the Ed25519 path on an old controller.

   One concrete gotcha for the implementer: the existing 200-character
   signature guard at `app/api/portwing-ws.ts:668-674` is specific to
   `hello.signature`. `signaturePq` needs its own length guard, sized for a
   6,170-character value, or it is an unbounded allocation from
   attacker-controlled input.
2. **Verification is fail-closed AND, and downgrade protection is policy, not
   negotiation.** Both signatures must verify, and one valid signature beside
   one invalid signature is a rejection, never a pass.

   Be honest about where downgrade safety comes from. Because the hello is the
   first frame and old controllers silently ignore the post-quantum fields,
   **the wire cannot protect against downgrade at all.** An attacker holding a
   CRQC-recovered Ed25519 key simply omits `signaturePq`. The only thing that
   stops that is the controller refusing a classical-only hello for an identity
   its own records mark as hybrid. That check lives entirely in Drydock's key
   store and is the single most important part of this design to get right.
3. **The key ID identifies the identity, not a key.** This is the correction to
   section 5.2's binding gap and it replaces the "algorithm domain separation"
   framing an earlier draft used, which was too weak a fix.

   Registering two independently-identified keys would be unsafe: an attacker
   with a victim's CRQC-recovered Ed25519 key plus their own legitimately
   enrolled ML-DSA key could present one of each and satisfy a naive AND. So a
   hybrid identity is **one record carrying both public keys**, and its ID is
   derived over both at once, with a version label to keep it disjoint from
   today's IDs:

   ```text
   hex(SHA-256("portwing-identity-v2" || ed25519_pub || mldsa87_pub)[:8])
   ```

   A key ID then resolves to a pair, both signatures are checked against that
   one record's two keys, and there is no way to combine keys from two records.
   The binding is in the identifier itself, so no separate cross-signature is
   needed.
4. **`authorized_keys` gains a hybrid line type**, not a second independent
   line. One line carries both public keys so the file cannot express the
   unsafe two-record shape:

   ```text
   hybrid-ed25519-mldsa87 <base64 ed25519 pub> <base64 mldsa87 pub> [comment]
   ```

   Both parsers keep the existing 32-byte check for the first key and add a
   2,592-byte check for the second. Two separate lines sharing a comment,
   which an earlier draft proposed, is exactly the shape item 3 rules out.
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
3. **The controller side gains post-quantum certificate support.** An earlier
   draft listed "Go's `crypto/tls` gains post-quantum signature algorithms" as
   a future trigger. **That is already satisfied and the trigger was wrong.**
   Go 1.27 registers `MLDSA44`, `MLDSA65`, and `MLDSA87` as full
   `x509.SignatureAlgorithm` values with no pre-hashing
   (`crypto/x509/x509.go:401-403`), parses ML-DSA public keys and PKCS#8
   private keys (`crypto/x509/parser.go:366-374`, `crypto/x509/pkcs8.go:83-111`),
   and carries the matching `MLDSA44`/`MLDSA65`/`MLDSA87` TLS signature schemes
   with `directSigning` (`crypto/tls/auth.go:145-146, 161-162`). So the agent's
   own stack could already verify a post-quantum controller certificate today.
   The live blocker is the other end: the controller's Node TLS stack and the
   CA tooling that would issue such a certificate. When that lands, section 4.2
   item 1 becomes available, and mTLS with a post-quantum certificate is a
   better answer for standard mode than an application-layer header, so target
   design item 5 should be revisited before anything else here.
4. **A credible CRQC estimate lands inside the rotation window** established in
   section 8. If keys rotate annually, an estimate that moves inside that
   horizon collapses the argument that rotation is sufficient.

---

## 8. What closes the gap today: rotation

The exposure in section 4 is bounded by an identity key's service life, and
bounding it costs no wire change, no new dependency, and no protocol
coordination. The **sequence differs by mode**, though, and getting that wrong
is how a rotation turns into an outage.

### 8.1 Standard mode: overlap rotation works

Portwing's registry holds many keys at once (`internal/auth/keys.go`) and
reloads `authorized_keys` on SIGHUP with no restart and no dropped connections
(`internal/server/http.go:245-266`). There is no name binding. So the ordinary
add-then-remove sequence works with no gap: append the new public key, SIGHUP,
cut the client over to the new `PRIVATE_KEY_FILE`, confirm requests are
authenticating under the new key ID, then remove the old line and SIGHUP again.
Both key IDs are logged on add and remove, so the cutover is observable.

### 8.2 Edge mode: revoke-then-reconnect, with a brief disconnect

An earlier draft of this note gave the 8.1 sequence for both modes. **That does
not work in edge mode**, and it would fail closed at the worst moment.

Drydock binds one agent name to one key ID (`app/api/portwing-ws.ts:151`). A
hello presenting a new `pubKeyId` under a name already bound to a different key
is rejected with `agent-name-claimed` before any welcome
(`app/api/portwing-ws.ts:763-781`), which Portwing correctly classifies as
terminal, so the agent stops rather than retrying
(`internal/edge/hello_reject.go`). The binding survives controller restarts and
is **not** released when the agent disconnects; only revoking the owning key
clears it, or idle eviction after 24 hours once the binding map is at its 10,000
cap. Two live sessions under one name are separately refused as
`agent-already-connected`.

So the working sequence, and the one to document:

1. Register the new public key in Drydock's key store. It coexists with the old
   one; the store is keyed per key with no name field, so this is safe.
2. Stage the new `PRIVATE_KEY_FILE` on the agent host without restarting it.
3. **Revoke the old key in Drydock.** This tears down the live session and
   releases the name binding in the same step.
4. Restart or let the agent reconnect. It presents the new key, the name is
   unbound, and it rebinds cleanly.

Step 3 to step 4 is a real gap, bounded by the agent's reconnect backoff. Plan
rotation as a short maintenance action rather than a zero-downtime one, and do
not reverse steps 3 and 4: revoking after the new key connects cannot work,
because the new key can never connect while the old binding stands.

**Named prerequisite for zero-downtime edge rotation.** Drydock would have to
let one agent name hold a set of authorized key IDs rather than a single one,
admitting any member and pruning on revocation. That is a Drydock change, not a
Portwing one, and it is worth raising with that repo independently of anything
post-quantum, because it also gates the hybrid migration in section 6: moving an
identity from a classical key ID to a hybrid one is itself a key ID change, so
it inherits exactly this disconnect.

### 8.3 Guidance to add to `docs/security-model.md`

1. **Rotate each agent identity key at least annually**, and immediately on any
   suspected host compromise. The point is to keep every key's service life
   comfortably shorter than any credible CRQC horizon, so a key that is
   attackable in 2035 was retired years earlier.
2. **Use the sequence for the mode**, 8.1 for standard and 8.2 for edge. Rehearse
   the edge one before relying on it, since it involves a deliberate disconnect.
3. **Treat the public key as sensitive-adjacent.** It is not a secret, but it is
   the input to the attack in section 4, so keeping it off public surfaces
   shrinks the population of attackers who could ever use a CRQC against it.
4. **Keep the blast radius per key.** One key per agent, never a shared fleet
   key, so a single recovered key impersonates one agent rather than all of
   them. Drydock's name binding already enforces this for edge mode.

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
