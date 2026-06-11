# Lookout Security Model

This document is the citable specification for Lookout's security controls.
Each control is named and described precisely so downstream tools, auditors,
and compliance checks can reference it by name.

## Controls

### 1. Proxy-First Architecture

Every byte that would reach the Docker socket passes through Lookout's HTTP
server, which applies authentication before forwarding. There is no path by
which an unauthenticated caller can reach the Docker daemon directly. The
catch-all mux entry that implements the Docker API proxy is registered last
and is wrapped with the same auth middleware as every named route:

```
mux.Handle("/", auth(s.handleDockerProxy))
```

This means a missing or misconfigured auth middleware would cause a build-time
or startup-time failure rather than a silent security gap.

### 2. Timing-Safe Token Comparison

Token validation uses `crypto/subtle.ConstantTimeCompare` from the Go standard
library. The comparison runs in constant time regardless of whether and where
the provided token diverges from the configured secret, eliminating
timing-oracle attacks that can leak token bytes one at a time.

Applies to all accepted credential headers: `Authorization: Bearer`,
`X-Lookout-Token`, and `X-Dd-Agent-Secret`.

### 3. Argon2id Token-at-Rest (TOKEN_HASH)

Lookout supports storing the token as an Argon2id hash rather than in
plaintext. Three mechanisms are available (evaluated in order):

| Mechanism | Environment variable | Description |
|-----------|----------------------|-------------|
| Plaintext | `TOKEN` / `DD_AGENT_SECRET` | Token value directly; read from env |
| Plaintext file | `TOKEN_FILE` / `DD_AGENT_SECRET_FILE` | Path to a file containing the token |
| Hash | `TOKEN_HASH` / `TOKEN_HASH_FILE` | Argon2id PHC string (see below) |

The `lookout hash-token` CLI subcommand generates a suitable hash using
OWASP-recommended parameters (m=19456 KiB, t=2, p=1). The token is read from
stdin so it never appears in shell history or process listings:

```bash
printf '%s' "$TOKEN" | lookout hash-token
# $argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>
```

Set the output as `TOKEN_HASH` (or write it to a file referenced by
`TOKEN_HASH_FILE`). At runtime Lookout compares the incoming token against
the stored hash using Argon2id verification, still wrapped with
`crypto/subtle` for the final byte comparison to prevent timing leakage.

This means the plaintext token is never stored on disk; a compromised
environment variable dump or configuration file reveals only the hash.

### 4. Compose Path-Traversal Guard

All file paths in `ComposeRequest` are resolved to their absolute forms and
verified to remain within `STACKS_DIR` before any disk operation:

```go
if !strings.HasPrefix(resolved, absBase+string(filepath.Separator)) && resolved != absBase {
    return "", fmt.Errorf("path %q escapes stacks directory", path)
}
```

A caller supplying `../../etc/passwd` or a symlink pointing outside the stacks
directory will receive a validation error; no file is read or written.

### 5. Compose Env Denylist

Environment variables passed in `ComposeRequest.EnvVars` are validated on two
axes before being written to `.env.drydock`:

1. **Key format:** Must match `^[a-zA-Z_][a-zA-Z0-9_]*$`.
2. **Denylist:** The following keys are rejected unconditionally:
   `LD_PRELOAD`, `LD_LIBRARY_PATH`, `PATH`, `DOCKER_HOST`, `DOCKER_CONFIG`,
   `DOCKER_CERT_PATH`, `DOCKER_TLS_VERIFY`, `DOCKER_CONTEXT`, `HOME`,
   `SHELL`, `BASH_ENV`, `ENV`, `CDPATH`, `IFS`.

The denylist covers dynamic linker hijacking (`LD_*`), shell init-file
injection (`BASH_ENV`, `ENV`), and Docker socket redirection (`DOCKER_HOST`,
`DOCKER_CONTEXT`).

### 6. Compose Flag-Injection Filter

Service names in `ComposeRequest.Services` are validated to not start with
`-`. Docker Compose passes service names as positional arguments; a name like
`--privileged` would be interpreted as a flag by the subprocess shell,
potentially escalating privileges or altering compose behaviour.

```go
if strings.HasPrefix(svc, "-") {
    return fmt.Errorf("invalid service name: %q", svc)
}
```

### 7. Rate Limiting

Failed authentication attempts are tracked per remote IP in a sliding
one-minute window:

- **Threshold:** 10 failures within 60 seconds triggers a block.
- **Response:** HTTP 429 (`Too Many Requests`) until the window expires.
- **Memory cap:** At most 10,000 IP entries are tracked simultaneously;
  new entries beyond the cap are silently dropped to prevent memory
  exhaustion (fail-open for tracking, not for auth).
- **Cleanup:** A background goroutine prunes expired entries every 5 minutes.

### 8. TLS 1.2+ AEAD-Only

When `TLS_CERT` and `TLS_KEY` are configured, Lookout presents TLS with:

- **Minimum version:** TLS 1.2 (`tls.VersionTLS12`)
- **Cipher suites (TLS 1.2):** ECDHE-ECDSA-AES256-GCM-SHA384,
  ECDHE-RSA-AES256-GCM-SHA384, ECDHE-ECDSA-AES128-GCM-SHA256,
  ECDHE-RSA-AES128-GCM-SHA256, ECDHE-ECDSA-CHACHA20-POLY1305,
  ECDHE-RSA-CHACHA20-POLY1305. All are AEAD modes; CBC and RC4 suites are
  not in the list.
- **Curves:** X25519, P-256.

TLS 1.3 cipher suites are controlled by Go's TLS stack, which selects only
AEAD suites by design.

### 9. Resource Caps

| Resource | Limit | Purpose |
|----------|-------|---------|
| WebSocket read | 16 MB | Prevent memory exhaustion from large WebSocket frames |
| HTTP response body read | 100 MB | Bound buffered Docker API responses |
| Exec request body | 10 MB | Limit exec payload size |
| Concurrent exec sessions | 100 | Prevent unbounded goroutine growth |
| Concurrent stream sessions | 100 | Prevent unbounded goroutine growth |
| Rate-limiter IP table | 10,000 entries | Prevent rate-limiter map exhaustion |

### 10. Panic Recovery

The outermost HTTP handler is wrapped with `RecoveryMiddleware`, which uses
`defer/recover` to catch any panics in downstream handlers. On recovery it
logs the full stack trace at ERROR level and returns HTTP 500. This prevents
a single malformed request from crashing the agent process and taking the
Docker proxy offline.

### 11. Ed25519 Per-Client Key Authentication

When `AUTHORIZED_KEYS` is configured, Lookout authenticates requests using
per-client Ed25519 keypairs instead of (or alongside) the shared token. Full
design rationale is in `docs/design/ed25519-auth.md`.

**Key registry:** Operator writes an `authorized_keys`-style file listing
trusted 32-byte Ed25519 public keys (one per line, `ed25519 <base64> [comment]`
format). The agent loads this file at startup and on `SIGHUP`. Hot reload adds
or removes keys from the in-memory map without restarting.

**Per-request signature:** Every authenticated HTTP request carries four
headers (`X-Lookout-Key-ID`, `X-Lookout-Timestamp`, `X-Lookout-Nonce`,
`X-Lookout-Signature`). The agent verifies the Ed25519 signature over a
canonical string of `METHOD\nPATH\nbody-sha256-hex\ntimestamp\nnonce` using
`crypto/ed25519.Verify` from the Go standard library. No new dependencies.

**Replay protection:** Two complementary mechanisms:
1. **Timestamp window:** Requests with `|now - timestamp| > MAX_CLOCK_SKEW_SECONDS`
   (default 60 s) are rejected with `X-Lookout-Reason: timestamp-skew`.
2. **Nonce LRU:** An in-memory nonce cache (capacity `NONCE_LRU_SIZE`, default
   10,000 entries) tracks nonces within the timestamp window. Repeated nonces
   return `X-Lookout-Reason: replay`. The LRU is preserved across SIGHUP reloads.

**Verification order in middleware:** If `X-Lookout-Signature` is present, Ed25519
verification runs; if absent, the request falls through to the existing token
verifier. Both auth methods can coexist during migration.

**File security:** The agent refuses to load an authorized_keys file with world-
read permission (`mode & 0004 != 0`), ensuring key material is never readable
by untrusted system users.

**Edge-mode signed hello:** In edge (WebSocket) mode, when `PRIVATE_KEY_FILE`
is configured the agent signs the WebSocket hello message with its Ed25519
private key, embedding `pubKeyId`/`timestamp`/`nonce`/`signature` fields. The
controller verifies these before sending `welcome`. Falls back to `tokenHash`
when no private key is configured.

**Model C enrollment (optional):** When `ENROLLMENT_TOKEN` is set alongside
`AUTHORIZED_KEYS`, the agent exposes `POST /api/lookout/enroll` (outside the
auth middleware, rate-limited). A caller presents the enrollment token and a
public key; the agent appends the key to the authorized_keys file, reloads the
registry, and burns the token (refusing further enrollment until restart).

See `docs/design/ed25519-auth.md` for full threat analysis, key rotation
procedures, and migration path from token auth.

---

## CVE Mapping

The table below maps classes of publicly known vulnerabilities to the Lookout
controls that structurally prevent them. "Structurally prevents" means the
architecture makes exploitation impossible regardless of token or config
values, not merely that a patch has been applied.

Only CVEs verified against public advisories at the time of writing are cited.
CVE identifiers that could not be independently verified have been omitted.

| CVE / Advisory | Vulnerability Class | Lookout Control | Architectural Reason |
|----------------|---------------------|-----------------|----------------------|
| **CVE-2024-41110** ([GHSA-v23v-6jw2-98fq](https://github.com/moby/moby/security/advisories/GHSA-v23v-6jw2-98fq)) | Docker AuthZ plugin bypass via zero-length `Content-Length` body. A crafted request with `Content-Length: 0` causes the daemon to forward the request to the AuthZ plugin without a body; plugins that default-deny on missing body are bypassed. CVSS 9.9. | Control 1 — Proxy-First Architecture | Lookout does not use Docker's AuthZ plugin mechanism at all. Authentication is implemented at the Lookout HTTP layer (controls 1 + 2), before the request is ever forwarded to the Docker socket. There is no AuthZ plugin to bypass. |
| **CVE-2026-34040** ([Docker Engine 29.3.1 advisory](https://www.docker.com/blog/docker-security-advisory-docker-engine-authz-plugin/)) | Docker AuthZ plugin bypass via oversized body (>1 MB bodies silently dropped before the plugin sees them, while the daemon still executes the original request). Incomplete fix for CVE-2024-41110. CVSS 8.8. | Control 1 — Proxy-First Architecture | Same reason as CVE-2024-41110: Lookout authenticates at the HTTP layer, not via Docker AuthZ plugins. The body-stripping behaviour in Docker's plugin dispatch path is irrelevant because Lookout's auth decision is made and enforced before the request reaches that path. |
| **CVE-2026-23944** ([OpenCVE](https://app.opencve.io/cve/CVE-2026-23944)) | Arcane Docker Manager: proxy middleware attaches agent auth token and forwards request to remote Docker agent *before* verifying the original request is authenticated. Unauthenticated callers can reach remote Docker agents. CVSS 9.8. | Control 1 — Proxy-First Architecture | Lookout's auth middleware is applied to every route including the catch-all proxy *before* any forwarding occurs (see `registerRoutes` in `internal/server/http.go`). The middleware cannot be bypassed by ordering — auth wraps the handler, not the other way around. |
| **CVE-2026-44848** ([Portainer, CCB Belgium advisory](https://ccb.belgium.be/advisories/warning-two-critical-vulnerabilities-portainer-allow-full-host-takeover-patch)) | Portainer: Docker plugin management endpoints (`/plugins/*`) absent from proxy authorization handler map, allowing non-admin users to install and enable arbitrary Docker plugins. CVSS 9.4. | Control 1 — Proxy-First Architecture | Lookout's catch-all handler applies auth uniformly to *all* paths, including `/plugins/*`. There is no per-path allow-list to misconfigure; omitting a path from the allow-list is not possible because the default is to authenticate everything. |
| **CVE-2026-44849** ([Endor Labs](https://www.endorlabs.com/vulnerability/cve-2026-44849)) | Portainer: endpoint security bypass via Swarm service create/update — seven endpoint security settings (privileged mode, host PID, device mapping, etc.) not enforced on Swarm service API paths. | Control 1 — Proxy-First Architecture | Lookout does not implement its own container security policy; it relies on the Docker daemon's own access controls and the operator's token-based access model. All Docker API paths are proxied with the same auth enforcement — there are no Swarm-specific code paths with weakened controls. |
| **CVE-2026-44850** ([TheHackerWire](https://www.thehackerwire.com/portainer-ce-bind-mount-bypass-cve-2026-44850/)) | Portainer: bind-mount restriction bypass — "disable bind mounts for non-administrators" setting not enforced because only `HostConfig.Binds` was checked, not `HostConfig.Mounts`. CVSS 8.5. | Control 1 — Proxy-First Architecture | Lookout does not implement container creation policies; it proxies Docker API requests to authenticated callers. Access control is binary (token present and valid = full Docker API access). Lookout is therefore not a policy enforcement layer and does not reproduce this class of partial-enforcement flaw. |
| **GHSA-7vx4-hf96-mqq6** (Dockge console injection, High, published 2025-03-31 — [GitHub Advisory](https://github.com/louislam/dockge/security/advisories)) | Terminal/console output injection in Dockge's web UI, likely via unsanitised container log or exec output rendered in the browser. | Not applicable (different attack surface) | Lookout does not have a web UI or a browser-rendered console. Log and exec output are streamed as raw bytes to authenticated API callers. Browser-side rendering is the responsibility of Drydock/DockPilot, not Lookout. |
| **CVE-2025-64419 / CVE-2025-66209–66213** ([Coolify, Aikido](https://www.aikido.dev/blog/ai-pentesting-coolify-cves)) | Coolify: command injection via docker-compose.yaml content and other inputs allowing root RCE on the host. CVSS up to 10.0. | Controls 4, 5, 6 — Compose Guards | Lookout validates all compose inputs before execution: path traversal protection (control 4), env var key/denylist validation (control 5), service name flag-injection filter (control 6). Compose file content written via `files` map is stored as-is and executed by the Docker Compose binary — operators should treat this with the same trust level as direct compose file access. |

### Notes on Omitted CVEs

- **CVE-2026-44885:** No advisory matching this identifier was found in public
  CVE databases at the time of writing (June 2026). Omitted pending
  confirmation.
- **Coolify 2026 CVEs beyond the batch above:** The Coolify disclosure
  referenced multiple additional CVEs; only the representative root-RCE class
  is cited here as it is the directly analogous attack surface.

### Scope Caveat

Lookout is a privileged agent: an authenticated caller has full Docker API
access. The controls above prevent *unauthenticated or improperly authorized*
access and certain classes of *injection* attacks against the Compose path.
They do not limit what an authenticated caller can do with the Docker API.
Operators should treat the Lookout token as a root-equivalent credential.
