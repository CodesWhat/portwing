# Portwing Security Best-Practices Review

Date: 2026-07-20

Scope: Go agent, authentication and authorization boundary, Docker/Compose proxying, edge tunnel, MCP endpoint, deployment manifests, CI/supply chain, and the two static Next.js sites.

Review mode: source review, remediation implementation, and local
dynamic/static verification. All fixes described below are applied in the
current working tree; deployment has not been performed from this review.

## Executive summary

The original review found **one Critical, three High, four Medium, and two Low
findings**. All ten are now resolved in the current tree, with regression tests
and updated public contracts. The Docker proxy fails closed without a
credential, signature version 2 covers the exact request target, expensive
authentication work is bounded before allocation, credential and Compose file
operations are descriptor/root confined, enrollment and TLS configuration fail
safely, audit claims are accurate, raw-token comparison is fixed-length, and
the static sites ship a generated strict browser policy.

No unresolved source finding remains from this pass. The only operational
follow-up is to probe the deployed site after release and alert if the hosting
edge stops serving the checked-in headers.

## Validation performed

The following checks passed after remediation on 2026-07-20:

- `go test -race ./...`
- `govulncheck -show verbose ./...` — no reachable symbol or package vulnerabilities; `GO-2026-5932` exists only in the unimported `golang.org/x/crypto/openpgp` package
- Fresh, no-cache container build plus `grype --fail-on high` — no High or Critical findings after aligning every from-source Docker builder with Go 1.26.5
- `gosec -quiet ./...`
- `golangci-lint run`
- `go build ./...`
- `go test -run='^$' -tags integration ./internal/integration` — integration suite compiles (live dockerd tests were not run)
- `npm audit --audit-level=low` in both `docs/` and `website/` — zero vulnerabilities
- `npm run lint` in both sites (`tsc --noEmit`)
- `npm run build` in `website/` — includes a successful docs production build, website production build, and CSP freshness gate
- `npm run test:headers` — route CSP hashing, route mapping, and rendered image-origin allowlisting pass
- `actionlint`
- `zizmor .github/workflows/` — no findings in offline mode; 18 repository suppressions remain part of the existing policy
- Five-second smoke runs for `FuzzParsePHC`, `FuzzParseTrustedProxies`, `FuzzParseImageRef`, `FuzzParseLabels`, `FuzzMCPHandler`, and `FuzzEnvelope`
- Current-source gitleaks scan (ignored build output excluded) — only the existing documentation/demo credential examples were identified; no apparent credential material
- Generated hosting policy — 25 CSP-protected HTML routes plus global browser headers; maximum CSP value 3,469 bytes, no `unsafe-inline` in `script-src`, and `frame-ancestors 'none'` on every route

Runtime response headers for `https://getportwing.com` were not changed or
probed because this task did not authorize a deployment. `website/vercel.json`
is now the checked-in Vercel-layer contract, and the build fails when its
per-route script hashes are stale; deployment-side confirmation remains an
operational release check.

## Critical findings

The severity, impact, evidence, fix, mitigation, and false-positive fields
below preserve the original review record. The remediation fields at the top
of each finding describe and locate the current implementation.

### PW-SEC-001 — Missing credentials fail open to a remotely reachable Docker proxy

- **Remediation status:** Resolved on 2026-07-20.
- **Remediated in:** `internal/config/config.go:24-27,175-176`; `internal/server/http.go:136-145,247-253`.
- **Remediation evidence:** Standard mode now returns a startup error unless a token, token hash, or authorized-keys registry is configured. Local unauthenticated use requires `ALLOW_UNAUTHENTICATED=true`; non-loopback exposure also requires `ALLOW_UNAUTHENTICATED_REMOTE=true`. Startup regression coverage exercises implicit failure, explicit loopback success, remote rejection, and the double-opt-in path in `internal/server/coverage_test.go:498-562`.

- **Rule ID:** GO-CONFIG-001 / secure fail-closed authentication boundary
- **Severity:** Critical
- **Location:** `internal/config/config.go:64-103`, `internal/config/config.go:154-164`, `internal/server/http.go:101-139`, `internal/server/http.go:243-275`, `internal/server/middleware.go:286-310`
- **Impact statement:** A default instance whose port is reachable can be used without credentials to create a privileged container, mount the host filesystem, and take over the host.
- **Evidence:** Configuration defaults all authentication mechanisms to empty while binding to every interface:

  ```go
  token := getEnv("TOKEN", "")
  tokenHash := getEnv("TOKEN_HASH", "")
  authorizedKeysFile := getEnv("AUTHORIZED_KEYS", "")
  // ...
  BindAddress: getEnv("BIND_ADDRESS", "0.0.0.0"),
  ```

  `NewServer` explicitly permits that state and only warns:

  ```go
  // verifier == nil means no auth configured.
  if verifier == nil && ed25519Cfg.Registry == nil {
      slog.Warn("no authentication configured: all requests will be accepted ...")
  }
  ```

  The middleware then bypasses authentication, including for the catch-all Docker proxy:

  ```go
  if verifier == nil && ed.Registry == nil {
      next.ServeHTTP(rw, r)
      return
  }
  // ...
  mux.Handle("/", authWrap(s.handleDockerProxy))
  ```

- **Impact:** The Docker socket is the authorization boundary for the host. The default combination of no credential, `0.0.0.0`, and a transparent catch-all means an omitted/misspelled secret is not merely a degraded mode; it is remote root-equivalent access. This also contradicts `docs/content/docs/security-model.mdx:31-42`, which says missing or misconfigured authentication causes startup failure.
- **Fix:** Fail startup in standard mode unless at least one of `TOKEN`, `TOKEN_HASH`, or `AUTHORIZED_KEYS` is configured. If unauthenticated local development must remain possible, require a conspicuous explicit opt-in such as `ALLOW_UNAUTHENTICATED=true`; reject that opt-in when binding to a non-loopback address unless a second explicit override is supplied. Add startup tests for every missing/misnamed-credential combination.
- **Mitigation:** Until fixed, require credentials in all manifests, bind to loopback or a private interface, and enforce network-policy/firewall restrictions. Sockguard limits post-auth Docker operations but does not repair the missing perimeter authentication boundary.
- **False positive notes:** The checked-in hardened Compose and Kubernetes examples do configure a token, and the README warns about unauthenticated operation. Those operational controls reduce exposure for users who follow them exactly, but they do not make the application secure by default or prevent a configuration typo from producing an open proxy.

## High findings

### PW-SEC-002 — Ed25519 signatures do not cover the URL query string

- **Remediation status:** Resolved on 2026-07-20.
- **Remediated in:** `internal/auth/verify.go:23-26,62-88,184-200`; `internal/server/http.go:46-55`.
- **Remediation evidence:** Signature version 2 adds `X-Portwing-Signature-Version: 2` and signs `EscapedPath()` plus the exact `RawQuery` (including an empty forced query). Unversioned signatures are accepted only for query-free requests. Tests prove that query addition, removal, value changes, and reordering invalidate the signature and that escaped paths are preserved (`internal/auth/verify_test.go:87-198`).

- **Rule ID:** request-signature integrity / canonicalization
- **Severity:** High
- **Location:** `internal/auth/verify.go:57-67`, `internal/auth/verify.go:164-170`, `internal/server/http.go:370-395`
- **Evidence:** Verification signs only `r.URL.Path`:

  ```go
  msg := CanonicalMessage(r.Method, r.URL.Path, bodyHash, tsUnix, nonceHeader)
  ```

  The Docker proxy forwards the complete request URI, including the unsigned query:

  ```go
  dockerURL := fmt.Sprintf("http://localhost%s", r.URL.RequestURI())
  ```

- **Impact:** A network intermediary or untrusted TLS-terminating proxy can intercept a valid signed request, suppress the original, alter only its query string, and deliver the modified request once with the original signature and nonce. Docker query parameters materially change operations—for example `force=1`, volume removal flags, pruning filters, log ranges, and streaming behavior—without changing the signed method, path, or body. Nonce replay protection does not help when the modified request is the first copy delivered.
- **Fix:** Define and version a canonical request-target that includes the exact escaped path and raw query, preferably the exact origin-form request target (`EscapedPath()` plus `?` and `RawQuery` when present). Update both signer and verifier together and add tests proving that adding, removing, reordering, or changing a query parameter invalidates the signature. Plan a compatibility transition because this changes the wire signature contract.
- **Mitigation:** Enforce end-to-end TLS from signer to agent and do not terminate it at infrastructure outside the trusted computing base. This reduces, but does not remove, the need for complete signature coverage.
- **False positive notes:** Token-authenticated requests do not promise detached message integrity and are unaffected as a protocol-compatibility matter. The issue is specifically material because the security model recommends Ed25519 when TLS terminates at a load balancer that is not fully trusted (`docs/content/docs/security-model.mdx:123-129`).

### PW-SEC-003 — Concurrent cold Argon2 attempts bypass the effective rate limit and can exhaust memory

- **Remediation status:** Resolved on 2026-07-20.
- **Remediated in:** `internal/server/middleware.go:64-98,205-258,334-363`; `internal/server/argon2.go:169-229`.
- **Remediation evidence:** The rate limiter atomically reserves at most two in-flight token verifications per source IP before verification. The production Argon2 verifier independently permits at most two cold derivations agent-wide and returns HTTP 429 before calling `argon2.IDKey` when capacity is full. Instrumented concurrency tests prove both bounds (`internal/server/middleware_test.go:61-119`; `internal/server/argon2_test.go:198-244`).

- **Rule ID:** GO-CONC-001 / GO-HTTP-002 resource exhaustion
- **Severity:** High
- **Location:** `internal/server/middleware.go:312-320`, `internal/server/middleware.go:395-405`, `internal/server/argon2.go:146-159`, `internal/server/argon2.go:180-201`
- **Evidence:** The middleware checks the completed-failure counter, performs credential verification, and records a failure only after verification returns:

  ```go
  if rl.IsRateLimited(clientIP) { /* reject */ }
  // ...
  if !verifier.Verify(provided) {
      rl.RecordFailure(clientIP)
      // ...
  }
  ```

  Before the first successful token verification populates the SHA-256 cache, every wrong token performs the configured Argon2 derivation. The generated default uses 19,456 KiB per derivation:

  ```go
  memory uint32 = 19456
  // ...
  hash := argon2.IDKey(...)
  ```

- **Impact:** Immediately after startup, one unauthenticated source can open many requests concurrently. Each request observes fewer than ten completed failures and begins a roughly 19 MiB Argon2 operation before any request increments the counter. A small burst can exceed the checked-in Kubernetes 256 MiB memory limit and repeatedly crash the agent, preventing the first legitimate request from ever priming the fast verification cache.
- **Fix:** Put a small global semaphore around expensive Argon2 verification and reserve per-IP attempts/in-flight slots atomically before derivation. Reject or queue excess work before allocating Argon2 memory. Add a concurrency regression test using an instrumented slow verifier to prove that no more than the configured number of expensive verifications can overlap.
- **Mitigation:** Apply connection and request-rate limiting at the reverse proxy, keep strict container memory limits/restart policy, and arrange an authenticated health/bootstrap request immediately after startup.
- **False positive notes:** Once a correct token has been seen, the verifier uses a cached SHA-256 comparison and this specific high-memory path closes until restart. The exploitable cold-start window and ability to keep the process from reaching the primed state remain.

### PW-SEC-004 — Key permission validation accepts group/world-writable credential files

- **Remediation status:** Resolved on 2026-07-20.
- **Remediated in:** `internal/auth/keys.go:96-107,183-237`; `internal/auth/keygen.go:14-25`.
- **Remediation evidence:** Both authorized-keys and private-key loaders open first, validate the opened descriptor with `f.Stat`, require a regular file, reject world-read and group/world-write bits on Unix, and then read from that same descriptor. Group-readable `0640` remains supported for secret mounts. Tests cover modes `0620` and `0602` plus non-regular paths (`internal/auth/keys_test.go:205-254`; `internal/auth/keygen_test.go:102-131`).

- **Rule ID:** GO-CONFIG-001
- **Severity:** High
- **Location:** `internal/auth/keys.go:96-108`, `internal/auth/keys.go:188-208`, `internal/auth/keygen.go:15-26`
- **Evidence:** The shared permission check rejects only the world-read bit:

  ```go
  if mode&0o004 != 0 {
      return fmt.Errorf("... world-readable ...")
  }
  ```

  The same check protects both the standard-mode `authorized_keys` registry and the edge-mode private key. Modes such as `0620` (group-writable) and `0602` (world-writable but not world-readable) therefore pass this check.
- **Impact:** Any local principal able to write the configured `authorized_keys` file can grant itself root-equivalent remote Docker access after reload/restart. A writable edge private key can be replaced or corrupted, undermining agent identity and availability. The current `stat`-then-open sequence also leaves a path-replacement race between permission validation and the file read.
- **Fix:** Require a regular file with no group/other write bits (`mode.Perm() & 0o022 == 0`) while continuing to allow the documented `0640` read shape. Open the file first and validate permissions with `f.Stat()` on the opened descriptor so the checked object is the object read; use platform-appropriate no-follow handling where compatible with Kubernetes/Docker secret mounts.
- **Mitigation:** Mount all credential paths read-only, use owner-controlled `0400`/`0600` or root-owned `0640`, and prevent unrelated host users/containers from sharing the credential file's writable group.
- **False positive notes:** The checked-in examples use read-only secret mounts, so they are not directly exploitable through this condition. The code-level validation is still weaker than its purpose and accepts unsafe operator-supplied modes.

## Medium findings

### PW-SEC-005 — Enrollment parses an unbounded unauthenticated request body

- **Remediation status:** Resolved on 2026-07-20.
- **Remediated in:** `internal/auth/enroll.go:19,36-54,72-88,95-105,141-142`; `internal/server/middleware.go:139-179,386-411`.
- **Remediation evidence:** Enrollment retains and compares only a fixed-size SHA-256 digest of its bootstrap token, accepts at most one 64 KiB JSON value, returns 413 on overflow, and rejects trailing JSON. Malformed/oversized bodies feed a dedicated per-IP abuse window while 401 responses retain the credential-failure accounting. Regression tests cover digest storage, oversized bodies, trailing values, token preservation, and repeated malformed-request throttling (`internal/auth/enroll_test.go:160-215`; `internal/server/middleware_test.go:724-759`).

- **Rule ID:** GO-HTTP-002
- **Severity:** Medium
- **Location:** `internal/auth/enroll.go:53-89`, `internal/server/http.go:207-217`, `internal/server/http.go:248-253`, `internal/server/middleware.go:234-264`
- **Evidence:** The public bootstrap endpoint decodes directly from `r.Body` without `http.MaxBytesReader`:

  ```go
  var req enrollRequest
  if err := json.NewDecoder(r.Body).Decode(&req); err != nil { /* 400 */ }
  ```

  The server intentionally has no `ReadTimeout`, and `rateLimitOnly` records only downstream `401` responses. Malformed/oversized JSON that produces `400` is not counted.
- **Impact:** When enrollment is enabled, an unauthenticated client can send very large JSON strings or slow body streams, consuming memory/connections before the enrollment token is checked. Repeated invalid-JSON requests bypass the failure counter entirely.
- **Fix:** Wrap the body with `http.MaxBytesReader` at a conservative enrollment-specific limit (for example 64 KiB), reject overflow with `413`, disallow trailing JSON values, and count oversized/malformed requests toward an abuse limiter distinct from credential failures. Consider a per-request body-read deadline or rely on a documented reverse-proxy deadline.
- **Mitigation:** Expose enrollment only briefly on a restricted network, use a reverse proxy with body-size/read-time limits, and remove `ENROLLMENT_TOKEN` after bootstrap.
- **False positive notes:** The route exists only when both enrollment and an authorized-keys registry are configured. That reduces the affected deployment set but does not authenticate the body-parsing phase.

### PW-SEC-006 — Compose path containment is lexical and follows pre-existing symlinks

- **Remediation status:** Resolved on 2026-07-20.
- **Remediated in:** `internal/docker/compose.go:220-345`.
- **Remediation evidence:** Compose uploads and `.env.drydock` writes now operate relative to `os.OpenRoot(STACKS_DIR)`, reject symlink/non-directory components and symlink/non-regular destinations, and retain root confinement across path-swap races. Tests plant both a symlinked stack root and a symlinked nested destination and prove no outside file is created or modified (`internal/docker/compose_test.go:70-119`).

- **Rule ID:** GO-PATH-001
- **Severity:** Medium
- **Location:** `internal/docker/compose.go:216-266`, `internal/docker/compose.go:370-417`, `docs/content/docs/security-model.mdx:189-202`
- **Evidence:** `resolvePath` cleans and compares absolute path strings but never resolves or rejects symlink components:

  ```go
  full, err := filepath.Abs(filepath.Join(base, cleanPath))
  if !pathWithin(base, full) { /* reject */ }
  // ...
  os.WriteFile(absPath, data, 0o600)
  ```

  `os.WriteFile` follows a pre-existing symlink. The security model nevertheless states that a symlink pointing outside the stacks directory is rejected.
- **Impact:** If an attacker or another workload can plant a symlink below `STACKS_DIR`, a subsequent Compose file or `.env.drydock` write can overwrite a file outside the intended root that is writable by the Portwing UID. This can cross boundaries in shared-volume or sockguard-constrained deployments even though a fully trusted authenticated Docker administrator is already root-equivalent.
- **Fix:** Perform writes relative to an opened root directory using directory-descriptor/no-follow semantics, rejecting symlinks in every path component and at the final file. Keep the validation and write in one rooted operation to avoid TOCTOU races. Add tests with both a symlinked stack root and a symlinked nested file.
- **Mitigation:** Do not share `STACKS_DIR` with other containers/users, pre-own it narrowly, and mount unrelated sensitive paths read-only.
- **False positive notes:** An authenticated caller with unrestricted raw Docker access already controls the host, so this does not increase privilege in that specific threat model. It remains relevant where Sockguard intentionally narrows Docker operations or where another local tenant can modify the stacks volume.

### PW-SEC-007 — A partially configured TLS keypair silently downgrades to plaintext HTTP

- **Remediation status:** Resolved on 2026-07-20.
- **Remediated in:** `internal/config/config.go:158-162`.
- **Remediation evidence:** Configuration loading now rejects the XOR state: `TLS_CERT` and `TLS_KEY` must both be set or both empty. Table-driven tests cover certificate-only and key-only failures (`internal/config/config_test.go:27-56`).

- **Rule ID:** GO-CONFIG-001 / transport fail-closed validation
- **Severity:** Medium
- **Location:** `internal/config/config.go:154-164`, `internal/server/http.go:219-236`, `internal/server/http.go:540-548`
- **Evidence:** `TLS_CERT` and `TLS_KEY` are loaded independently. TLS is used only when both are non-empty; every other state falls through to plaintext `ListenAndServe()` with no startup error or warning.
- **Impact:** A typo, missing secret mount, or incomplete rotation can make a deployment that operators believe is using TLS start successfully in plaintext. Bearer credentials and Docker control traffic can then be observed or modified on the network.
- **Fix:** Validate at configuration load that `TLS_CERT` and `TLS_KEY` are either both set or both empty. Return a startup error for the XOR case and add table-driven config tests.
- **Mitigation:** Terminate TLS at a correctly monitored reverse proxy, enforce HTTPS-only network paths, and add a deployment probe that verifies the actual scheme/certificate.
- **False positive notes:** Deployments intentionally using plaintext behind a same-host or private TLS-terminating proxy may leave both values empty; the proposed validation preserves that supported case.

### PW-SEC-008 — Marketing claims local audit logs are tamper-evident, but no integrity mechanism exists

- **Remediation status:** Resolved on 2026-07-20 by correcting the claims rather than inventing an unimplemented integrity mechanism.
- **Remediated in:** `docs/content/docs/audit-logging.mdx`, both security-model documents, website/docs metadata and footers, the marketing feature copy, `website/public/llms.txt`, and every affected comparison page.
- **Remediation evidence:** The local file is now consistently described as append-only during normal operation but not cryptographically tamper-evident. Public copy directs operators to external append-only/WORM storage under separate credentials for tamper evidence. A repository-wide search leaves only the explicit negative statement in `docs/content/docs/security-model.mdx:297`.

- **Rule ID:** security-control accuracy / audit integrity
- **Severity:** Medium
- **Location:** `internal/audit/audit.go:78-121`, `docs/content/docs/audit-logging.mdx:136`, `website/src/app/data/features.ts:123-129`, `website/src/lib/comparison-route-data/hawser.tsx:10-22`, `docs/src/lib/site-config.ts:19-24`
- **Evidence:** The implementation writes ordinary JSON lines to a `0600` append-opened file:

  ```go
  f, err := os.OpenFile(dest, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
  h := slog.NewJSONHandler(w, ...)
  ```

  It has no hash chain, signature, monotonic counter, remote acknowledgement, or immutable storage. The detailed audit documentation correctly says: “Portwing itself does not provide on-disk integrity protection beyond the `0600` file mode.” Multiple website/docs metadata and comparison pages nevertheless call the built-in log “tamper-evident.”
- **Impact:** Operators may rely on the local file for incident evidence or compliance under the mistaken belief that alteration/deletion is detectable, even though a host or process compromise can rewrite it without evidence.
- **Fix:** Either remove “tamper-evident” from all built-in-log claims and state that tamper evidence requires immediate export to immutable storage, or implement a documented cryptographic/remote-anchoring design and verify it during export.
- **Mitigation:** Ship audit records immediately to append-only/WORM storage under separate credentials and alert on delivery gaps.
- **False positive notes:** `O_APPEND` reduces accidental overwrite through the existing file descriptor but does not provide tamper evidence against a principal that can reopen, truncate, replace, or delete the file.

## Low findings

### PW-SEC-009 — Raw-token comparison leaks whether the presented token length matches

- **Remediation status:** Resolved on 2026-07-20.
- **Remediated in:** `internal/server/middleware.go:40-52`; `internal/server/http.go:105-106`.
- **Remediation evidence:** The verifier retains only `sha256.Sum256(configuredToken)`, hashes each presented value, and compares two fixed-size arrays with `subtle.ConstantTimeCompare`. `TestRawTokenVerifierUsesFixedLengthDigest` verifies the storage shape and behavior (`internal/server/middleware_test.go:121-132`).

- **Rule ID:** GO-AUTH-001
- **Severity:** Low
- **Location:** `internal/server/middleware.go:22-34`, `docs/content/docs/security-model.mdx:163-167`
- **Evidence:** The raw verifier passes variable-length strings directly to `subtle.ConstantTimeCompare`:

  ```go
  return subtle.ConstantTimeCompare([]byte(token), []byte(v.token)) == 1
  ```

  Go documents that `ConstantTimeCompare` returns immediately when slice lengths differ. The security model currently claims the comparison is constant time regardless of how the token differs.
- **Impact:** A sufficiently capable remote timing observer may learn the configured token length. Random tokens remain infeasible to guess, so this does not reveal token bytes and has low practical severity.
- **Fix:** Compare fixed-length SHA-256 digests of the presented and configured raw tokens, or enforce a fixed token format/length before a fixed-size constant-time comparison. Update the documentation's timing claim.
- **Mitigation:** Use high-entropy fixed-length tokens or Ed25519 authentication and enforce TLS.
- **False positive notes:** Network jitter makes exploitation difficult, and the documented token-generation commands already produce predictable high-entropy lengths.

### PW-SEC-010 — Static-site security headers are not defined in the repository

- **Remediation status:** Resolved on 2026-07-20 at the Vercel static-hosting layer.
- **Remediated in:** `website/vercel.json`; `website/scripts/security-headers.mjs`; `website/scripts/security-headers.test.mjs`; `website/package.json`; both Next configs.
- **Remediation evidence:** The generator hashes every rendered inline script per HTML route, derives only the external image origins actually rendered by that page, and emits CSP plus `nosniff`, frame denial, referrer, and permissions headers. Stable static build IDs make the route hashes reproducible, and `postbuild --check` fails on stale policy. The final export contains 25 CSP routes, no `unsafe-inline` in any `script-src`, `frame-ancestors 'none'` everywhere, and a 3,469-byte maximum CSP value.

- **Rule ID:** NEXT-HEADERS-001 / REACT-HEADERS-001
- **Severity:** Low
- **Location:** `website/next.config.ts:7-13`, `docs/next.config.ts:10-16`, `website/src/app/layout.tsx:69-88`
- **Evidence:** Both sites use `output: "export"`, so Next.js application headers are not configured. No checked-in hosting configuration defines CSP, clickjacking protection, `nosniff`, referrer policy, or permissions policy. The marketing layout includes an intentional inline script, which requires a CSP hash if a strict CSP is deployed.
- **Impact:** If the hosting edge does not add these headers, browser hardening against content injection, framing, and MIME sniffing is absent. The sites are static and use trusted content, keeping severity low.
- **Fix:** Define the headers at the actual static hosting/CDN layer. Use a hash for the constant `REVEAL_BOOTSTRAP` script rather than allowing arbitrary inline script; keep `frame-ancestors`, `object-src`, `base-uri`, and `nosniff` appropriately restrictive.
- **Mitigation:** Confirm deployed headers with an external probe and monitor them in CI.
- **False positive notes:** The headers may already be injected by infrastructure not represented in this repository. Runtime verification was attempted but could not be completed because DNS resolution was unavailable in the review environment.

## Positive controls observed

- Every named privileged route and the Docker catch-all are structurally wrapped by the same authentication middleware.
- Portwing authentication headers are stripped before requests reach Docker.
- `NonceLRU.Add` is the atomic replay authority; the pre-check is not used as the acceptance decision.
- `statusRecorder` preserves `Flush`, `Hijack`, and `Unwrap`.
- Forwarded client-IP headers are ignored unless the direct peer is in an explicit trusted-proxy CIDR.
- CORS is not enabled by default, and the agent does not use cookie authentication, avoiding CSRF exposure.
- Edge WebSockets set a 16 MiB read limit, response bodies are capped, and exec/request concurrency is bounded.
- Compose subprocesses use argument arrays rather than a shell; environment keys and service-name flag injection are checked.
- Container identifiers used by higher-level APIs are syntax-validated before interpolation into Docker paths.
- CI uses SHA-pinned actions, restricted permissions, `persist-credentials: false`, runner egress controls, race tests, fuzzing, vulnerability scanning, workflow scanning, signed releases, provenance, and SBOM generation.
- The current Go and npm dependency scans found no reachable known vulnerabilities.

## Remediation completion

The original sequence was completed in full:

1. [x] Make unauthenticated standard mode an explicit, loopback-constrained opt-in.
2. [x] Version the Ed25519 canonical target and sign the exact query string.
3. [x] Bound concurrent cold Argon2 verification before memory allocation.
4. [x] Harden key-file open/permission validation.
5. [x] Add enrollment body limits, single-value parsing, and malformed-request abuse accounting.
6. [x] Replace Compose upload writes with rooted, symlink-resistant operations.
7. [x] Fail closed on a partial TLS keypair.
8. [x] Correct audit-integrity claims and define/test static-site headers at the hosting layer.
9. [x] Remove the raw-token length timing distinction.

All ten finding IDs are resolved. Deployment of the checked-in site policy and
an external response-header probe remain release operations, not source-code
remediation gaps.
