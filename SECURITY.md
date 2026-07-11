# Security Policy

## Supported Versions

Security fixes are shipped on the **latest release line only**.

| Version        | Supported          |
| -------------- | ------------------ |
| 0.6.x (latest) | :white_check_mark: |
| < 0.6          | :x:                |

## Reporting a Vulnerability

If you discover a security vulnerability in Portwing, please report it responsibly.

**Do not open a public GitHub issue for security vulnerabilities.**

Instead, use [GitHub's private vulnerability reporting](https://github.com/CodesWhat/portwing/security/advisories/new) or email **<security@codeswhat.com>**. GitHub private reports are preferred because they keep report details private and tie disclosure to the advisory and fix workflow.

You can expect:

- **Acknowledgement** within 48 hours
- **Status update** within 7 days
- **Fix or mitigation** as soon as feasible, depending on severity

We appreciate responsible disclosure and will credit reporters in the release notes unless you prefer to stay anonymous.

## Scope

### In scope

- The Go agent — authentication (token, Argon2id `TOKEN_HASH`, Ed25519 per-request signatures), rate limiting, the Docker API proxy, the drydock and generic adapters, the edge-mode WebSocket tunnel, the enrollment endpoint, compose stack handling, the audit log, the MCP server, the health/metrics endpoints, and config parsing.
- The published container image at `ghcr.io/codeswhat/portwing:<tag>`, including its SBOM, build provenance, and cosign signatures.
- Any compiled binary distributed via a GitHub release tagged `v0.x.x` or later.

### Out of scope

- Drydock itself — separate project, report issues there.
- Third-party deployments of Portwing. If you find an unauthenticated agent exposed in the wild, contact that operator directly. We can help triage but we can't ship a fix for it.
- Denial-of-service via CPU/memory exhaustion from an *authenticated* client. An authenticated Portwing client already has full Docker control on that host; the trust boundary is the credential, and a client inside it is assumed to be cooperating in good faith.

If you're unsure whether something is in scope, err on the side of reporting — we'd rather deduplicate than miss a real bug.

## Threat model and container runtime identity

Portwing's job is remote Docker control: an authenticated client can do anything the Docker daemon allows. The primary security boundary is therefore **authentication**, not API filtering — and the controls below exist to keep credentials strong, replay-proof, and brute-force-resistant.

The published image runs as the dedicated **non-root `portwing` user (UID 65532)** — the final stage sets `USER 65532:65532`, and `/data/stacks` plus the user's home directory ship pre-owned by that UID so read-only-rootfs deployments work out of the box. Because the agent must open the host's Docker socket (`root:docker 0660` on typical hosts), a non-root agent needs the socket's group added at deploy time: `group_add: ["<gid>"]` in compose or `--group-add $(stat -c '%g' /var/run/docker.sock)` with `docker run`. Mounted credential files (tokens, Ed25519 keys) must be readable by UID 65532 — `chown 65532:65532` + `chmod 0400` on the host file is the recommended shape; the agent additionally refuses world-readable key files outright. If your environment genuinely requires the old behavior, `user: "0:0"` restores it. Be aware that root-vs-non-root is *not* the load-bearing boundary for this class of tool: any process that can reach the Docker socket can ask the daemon to create a privileged container and pivot to the host, regardless of its own UID. It does, however, remove a class of in-container nuisance (no root-owned files on volumes, no accidental privileged file access through other mounts) and satisfies `runAsNonRoot` admission policies without overrides.

The controls that materially harden a deployment:

- **Strong credentials** — set `TOKEN_HASH` (Argon2id PHC string from `portwing hash-token`) so a leaked config doesn't expose the credential, or use Ed25519 per-request signing (`AUTHORIZED_KEYS`) so no shared secret exists at all.
- **Read-only root filesystem, dropped capabilities, no new privileges** — `read_only: true`, `cap_drop: [ALL]`, `security_opt: ["no-new-privileges:true"]`. See `examples/` for ready-to-run compose files.
- **Socket filtering with sockguard** — put [sockguard](https://github.com/CodesWhat/sockguard) between Portwing and the Docker socket so even a fully compromised agent is constrained to an explicit API allowlist (`examples/docker-compose.with-sockguard.yml`).
- **Edge mode** — `DRYDOCK_URL` makes the agent dial out over WebSocket instead of listening; no inbound port exists to attack.
- **Rootless Docker on the host** — reduces the daemon's authority at the actual trust boundary.

**sockguard idle-session timeout:** when sockguard fronts the Docker socket, its `HijackHandler` force-closes any hijacked bidirectional stream — interactive `exec`/`attach` sessions — after 10 minutes with zero bytes of traffic in either direction (`hijackInactivityTimeout` in sockguard's `internal/proxy/hijack.go`). This limit doesn't exist when Portwing talks directly to `/var/run/docker.sock`. Operators pairing Portwing with sockguard should expect an idle terminal session (e.g. one left open with no keystrokes or output) to be dropped after 10 minutes of inactivity; send periodic traffic or reconnect if you need longer-lived idle sessions.

## Security measures

Portwing implements the following:

- **Authentication**: three mechanisms, all timing-safe — raw token (`TOKEN`, compared with `crypto/subtle`), Argon2id PHC hash (`TOKEN_HASH`, token never stored in clear), and Ed25519 per-request signatures (`AUTHORIZED_KEYS`) with replay protection (nonce LRU + ±60s timestamp window) and SIGHUP hot-reload of the key file. `TOKEN` and `TOKEN_HASH` are mutually exclusive.
- **Enrollment**: optional single-use `ENROLLMENT_TOKEN` for bootstrapping the first Ed25519 key — burned on first **successful** use, rate-limited (shares the 10-failed-attempts-per-IP-per-minute limiter), and audit-logged. The token is deliberately **not** burned on a failed attempt: doing so would let any unauthenticated caller disable enrollment with a single bad request. Use a high-entropy value (e.g. `openssl rand -hex 32`).
- **Rate limiting**: 10 failed auth attempts per IP per minute, checked *before* credential verification; failed Argon2id attempts always traverse the full derivation.
- **Audit log**: structured JSON audit trail (`AUDIT_LOG`) of auth failures, enrollment attempts, and mutating operations.
- **TLS**: TLS 1.2+ with modern AEAD cipher suites only.
- **Docker Compose security**: path traversal protection, env var validation and denylist, service name injection prevention.
- **Resource limits**: WebSocket read (16 MB), response body (100 MB), exec body (10 MB), signed-request body (1 MB), concurrent sessions (100), nonce LRU (10,000).

### Nonce cache capacity and fail-open behavior

The nonce LRU cache (capacity controlled by `NONCE_LRU_SIZE`, default 10,000) tracks nonces within the ±60 s timestamp window to block replayed Ed25519-signed requests. When the cache is full, new nonces are silently accepted without being recorded — the cache fails open for *tracking* while the timestamp window remains enforced. This is safe because nonces are only recorded *after* a valid Ed25519 signature has been verified: filling the cache to trigger fail-open requires a large volume of legitimately signed requests, which is not possible for an unauthenticated attacker. The default capacity of 10,000 entries exceeds expected request volume for a single-host agent and ensures the fail-open path is not reachable under normal conditions.

- **Minimal attack surface**: static binary, three direct dependencies, stdlib crypto only, Wolfi OS base image with no package manager.
- **Supply chain**: SHA-pinned GitHub Actions with harden-runner on every job, weekly govulncheck/grype/gosec scans, OpenSSF Scorecard, cosign-signed images, SLSA build provenance, and SBOMs on every release.

## What to include in a report

A good report makes triage fast and reduces the risk we misread the severity. Please include as much of the following as you can:

- **Portwing version and image digest** — `GET /api/v1/version` (or `portwing version`) and the digest of the image you tested (`docker image inspect`).
- **Reproducer** — the minimal config, environment, and request(s) that demonstrate the issue. If it needs a specific Docker daemon version or adapter (`drydock` vs `generic`), say which.
- **Observed behavior** — what Portwing did, including any relevant audit-log lines, `X-Portwing-Reason` headers, and status codes.
- **Expected behavior** — what you believe Portwing should have done instead, and why.
- **Impact assessment** — your read on severity, who it affects, and whether it requires a valid credential, network position, or host access.
- **Disclosure timeline** — when you found it, whether you've told anyone else, and whether a specific embargo date suits you.

If the bug involves a supply-chain concern (a tampered image, a cosign verification failure, a compromised dependency), also include the exact `cosign verify` / `gh attestation verify` command you ran and its full output.
