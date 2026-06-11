# Security Policy

## Supported Versions

Security fixes are shipped on the **latest release line only**.

| Version        | Supported          |
| -------------- | ------------------ |
| 0.2.x (latest) | :white_check_mark: |
| < 0.2          | :x:                |

## Reporting a Vulnerability

If you discover a security vulnerability in Lookout, please report it responsibly.

**Do not open a public GitHub issue for security vulnerabilities.**

Instead, use [GitHub's private vulnerability reporting](https://github.com/CodesWhat/lookout/security/advisories/new) or email **<security@codeswhat.com>**. GitHub private reports are preferred because they keep report details private and tie disclosure to the advisory and fix workflow.

You can expect:

- **Acknowledgement** within 48 hours
- **Status update** within 7 days
- **Fix or mitigation** as soon as feasible, depending on severity

We appreciate responsible disclosure and will credit reporters in the release notes unless you prefer to stay anonymous.

## Scope

**In scope**

- The Go agent — authentication (token, Argon2id `TOKEN_HASH`, Ed25519 per-request signatures), rate limiting, the Docker API proxy, the drydock and generic adapters, the edge-mode WebSocket tunnel, the enrollment endpoint, compose stack handling, the audit log, the MCP server, the health/metrics endpoints, and config parsing.
- The published container image at `ghcr.io/codeswhat/lookout:<tag>`, including its SBOM, build provenance, and cosign signatures.
- Any compiled binary distributed via a GitHub release tagged `v0.x.x` or later.

**Out of scope**

- Drydock itself — separate project, report issues there.
- Third-party deployments of Lookout. If you find an unauthenticated agent exposed in the wild, contact that operator directly. We can help triage but we can't ship a fix for it.
- Denial-of-service via CPU/memory exhaustion from an *authenticated* client. An authenticated Lookout client already has full Docker control on that host; the trust boundary is the credential, and a client inside it is assumed to be cooperating in good faith.

If you're unsure whether something is in scope, err on the side of reporting — we'd rather deduplicate than miss a real bug.

## Threat model and container runtime identity

Lookout's job is remote Docker control: an authenticated client can do anything the Docker daemon allows. The primary security boundary is therefore **authentication**, not API filtering — and the controls below exist to keep credentials strong, replay-proof, and brute-force-resistant.

The published image's final stage is `FROM scratch` with no `USER` directive, so the agent runs as **root inside the container by default**. This is a deliberate out-of-the-box tradeoff: the agent must open the host's Docker socket (`root:docker 0660` on typical hosts). The Wolfi rootfs ships a dedicated `lookout` user (UID 65532), so you can opt in to non-root with `user: "65532:65532"` plus `group_add` for your socket's group ID. Be aware that root-vs-non-root is *not* the load-bearing boundary for this class of tool: any process that can reach the Docker socket can ask the daemon to create a privileged container and pivot to the host, regardless of its own UID.

The controls that materially harden a deployment:

- **Strong credentials** — set `TOKEN_HASH` (Argon2id PHC string from `lookout hash-token`) so a leaked config doesn't expose the credential, or use Ed25519 per-request signing (`AUTHORIZED_KEYS`) so no shared secret exists at all.
- **Read-only root filesystem, dropped capabilities, no new privileges** — `read_only: true`, `cap_drop: [ALL]`, `security_opt: ["no-new-privileges:true"]`. See `examples/` for ready-to-run compose files.
- **Socket filtering with sockguard** — put [sockguard](https://github.com/CodesWhat/sockguard) between Lookout and the Docker socket so even a fully compromised agent is constrained to an explicit API allowlist (`examples/docker-compose.with-sockguard.yml`).
- **Edge mode** — `DRYDOCK_URL` makes the agent dial out over WebSocket instead of listening; no inbound port exists to attack.
- **Rootless Docker on the host** — reduces the daemon's authority at the actual trust boundary.

## Security measures

Lookout implements the following:

- **Authentication**: three mechanisms, all timing-safe — raw token (`TOKEN`, compared with `crypto/subtle`), Argon2id PHC hash (`TOKEN_HASH`, token never stored in clear), and Ed25519 per-request signatures (`AUTHORIZED_KEYS`) with replay protection (nonce LRU + ±60s timestamp window) and SIGHUP hot-reload of the key file. `TOKEN` and `TOKEN_HASH` are mutually exclusive.
- **Enrollment**: optional single-use `ENROLLMENT_TOKEN` for bootstrapping the first Ed25519 key — burned on first use (success **or** failure), rate-limited, and audit-logged.
- **Rate limiting**: 10 failed auth attempts per IP per minute, checked *before* credential verification; failed Argon2id attempts always traverse the full derivation.
- **Audit log**: structured JSON audit trail (`AUDIT_LOG`) of auth failures, enrollment attempts, and mutating operations.
- **TLS**: TLS 1.2+ with modern AEAD cipher suites only.
- **Docker Compose security**: path traversal protection, env var validation and denylist, service name injection prevention.
- **Resource limits**: WebSocket read (16 MB), response body (100 MB), exec body (10 MB), signed-request body (64 MB), concurrent sessions (100), nonce LRU (10,000).
- **Minimal attack surface**: static binary, three direct dependencies, stdlib crypto only, Wolfi OS base image with no package manager.
- **Supply chain**: SHA-pinned GitHub Actions with harden-runner on every job, weekly govulncheck/grype/gosec scans, OpenSSF Scorecard, cosign-signed images, SLSA build provenance, and SBOMs on every release.

## What to include in a report

A good report makes triage fast and reduces the risk we misread the severity. Please include as much of the following as you can:

- **Lookout version and image digest** — `GET /api/v1/version` (or `lookout version`) and the digest of the image you tested (`docker image inspect`).
- **Reproducer** — the minimal config, environment, and request(s) that demonstrate the issue. If it needs a specific Docker daemon version or adapter (`drydock` vs `generic`), say which.
- **Observed behavior** — what Lookout did, including any relevant audit-log lines, `X-Lookout-Reason` headers, and status codes.
- **Expected behavior** — what you believe Lookout should have done instead, and why.
- **Impact assessment** — your read on severity, who it affects, and whether it requires a valid credential, network position, or host access.
- **Disclosure timeline** — when you found it, whether you've told anyone else, and whether a specific embargo date suits you.

If the bug involves a supply-chain concern (a tampered image, a cosign verification failure, a compromised dependency), also include the exact `cosign verify` / `gh attestation verify` command you ran and its full output.
