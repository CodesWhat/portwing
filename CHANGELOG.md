# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.2.1] - 2026-06-12

### Fixed

- **Multi-arch image signatures**: cosign now signs the manifest lists (`ghcr.io/codeswhat/lookout:<version>` and `:latest`) in addition to the per-arch images, so `cosign verify` succeeds on the index digest users actually pull — not just the `-amd64`/`-arm64`/`-armv7` tags (GoReleaser `docker_signs` `artifacts: all`).
- **Release pipeline on private repositories**: the GitHub build-provenance attestation steps are gated on public repository visibility, so the release workflow no longer hard-fails on plans where artifact attestation is unavailable. Cosign keyless signatures (image manifests + `checksums.txt.bundle`) and CycloneDX SBOMs still cover every artifact regardless of visibility; attestations activate automatically when the repository is public.

### Changed

- **Alpine runtime rootfs (armv7)**: packages are pulled from the base image's own main+community repositories instead of hardcoded `v3.21` URLs, making the `FROM` tag the single source of truth so Dependabot base-image bumps apply cleanly without repository-URL drift.
- **README**: restructured to match the sibling drydock/sockguard layout — centered header, grouped badge rows, collapsible sections, Star History, and a prominent alpha status banner.

### Dependencies

- Bump the `golang` builder image to `1.26.4-alpine` (Dependabot `docker-minor` group).

## [0.2.0] - 2026-06-12

### Added

- **Ed25519 per-request authentication**: signed requests via `X-Lookout-Key-ID` / `X-Lookout-Timestamp` / `X-Lookout-Nonce` / `X-Lookout-Signature` headers, verified against an `authorized_keys` file (`AUTHORIZED_KEYS`). Replay protection via nonce LRU (`NONCE_LRU_SIZE`) and timestamp window (`MAX_CLOCK_SKEW_SECONDS`), SIGHUP hot-reload of the key file, `lookout keygen` CLI subcommand, `X-Lookout-Reason` diagnostic header on 401s, and signed hello for edge mode (`PRIVATE_KEY_FILE`).
- **Key enrollment**: optional single-use `ENROLLMENT_TOKEN` (`POST /api/lookout/enroll`) for bootstrapping the first Ed25519 key — burned on first use, rate-limited, and audit-logged.
- **Repository infrastructure**: hardened CI (SHA-pinned actions, harden-runner, zizmor, actionlint), five Go fuzz targets (60s in CI, 5m nightly), integration suite against a real Docker daemon, weekly vulnerability scans (govulncheck/grype/gosec), monthly mutation testing, OpenSSF Scorecard, CodeQL, Dependabot, and a release-cut → release pipeline with CHANGELOG validation and post-publish verification.
- **Community and policy docs**: CONTRIBUTING, CODE_OF_CONDUCT, SECURITY (threat model + private advisory reporting), RELEASING, AGENTS, issue templates, CODEOWNERS.
- **Deployment examples**: hardened Docker Compose files for standard, edge, and sockguard-layered deployments (`examples/`), all `read_only` + `cap_drop: [ALL]` + `no-new-privileges` with secrets-based tokens.
- **Git hooks**: lefthook pipeline (lint, race tests, govulncheck, fuzz smoke, goreleaser dry-run, workflow checks) and an emoji-conventional-commit message validator.
- **Supply-chain pipeline**: cosign keyless signing of container images, SBOM generation via syft, SLSA build provenance attestation via `actions/attest-build-provenance`.
- **Prometheus metrics**: `/metrics` and `/_lookout/metrics` endpoints exposing `lookout_build_info`, container count, and host resource metrics (CPU, memory, disk, network).
- **Argon2id token hashing**: `TOKEN_HASH` environment variable accepts an Argon2id PHC string (OWASP-recommended parameters: m=19456 KiB, t=2, p=1). `TOKEN_HASH_FILE` for Docker secrets support. A SHA-256 success cache keeps per-request cost flat after first verification. `lookout hash-token` CLI subcommand generates PHC strings.
- **Bearer auth**: `Authorization: Bearer <token>` header supported in addition to the `X-Lookout-Token` scheme.
- **`TRUSTED_PROXIES`**: configurable CIDR list for trusted reverse proxies; `X-Forwarded-For` is only honored from trusted sources for rate-limiting.
- **Audit logging**: structured JSON audit log (`AUDIT_LOG` env var) recording auth events (success, failure, rate-limit) with IP, user-agent, method, and path.
- **MCP server**: read-only Model Context Protocol server at `/_lookout/mcp` (Streamable HTTP transport, protocol revision 2025-11-25). Tools: `list_containers`, `inspect_container`, `container_logs`, `host_metrics`, `container_stats`.
- **Generic REST adapter**: headless REST+SSE management API (`internal/generic`) for standalone mode without a Drydock platform connection.
- **Drydock route additions**: `GET /api/log/entries`, watcher poll/get/container endpoints, trigger exec/batch endpoints.
- **`dd:watcher-snapshot` SSE event**: full watcher + container inventory payload emitted immediately after `dd:ack` on SSE connect and after every poll cycle, so Drydock can prune stale containers.
- **OpenAPI 3.1 spec**: `api/openapi.yaml` documenting all endpoints, request/response schemas, and security schemes.
- **Security model doc**: `docs/security-model.md` describing the defense-in-depth posture.
- **Watchtower migration guide**: `docs/watchtower-migration.md` for teams migrating from Watchtower.

### Fixed

- **Streaming through auth middleware**: response body flushing and WebSocket hijack now work correctly when wrapped by the auth middleware chain (`statusRecorder` forwards `Flush` and `Hijack`).
- **Version injection**: GoReleaser ldflags now correctly set `protocol.AgentVersion` and `protocol.Commit` at build time.

## [0.1.0] - 2025-06-01

### Added

- Initial release of Lookout remote Docker agent.
- **Transparent Docker API proxy**: forwards Docker API requests from the Drydock platform to the local Docker daemon in standard mode.
- **Edge mode**: WebSocket tunnel to Drydock platform (`DRYDOCK_URL` + `TOKEN`) for environments where inbound connections are not possible.
- **Drydock adapter**: full Drydock protocol compatibility — container sync, component sync, watcher/trigger stubs, SSE broadcasting.
- **SSE event stream**: real-time container state events at `/api/events`.
- **Standard mode HTTP server**: `/_lookout/health`, `/_lookout/info`, `/api/containers`, `/api/containers/{id}/logs`, `/api/events`.
- **Token authentication**: `TOKEN` environment variable with timing-safe comparison.
- **Rate limiting**: 10 failed auth attempts per IP per minute.
- **Multi-arch Docker image**: `linux/amd64` and `linux/arm64` via GoReleaser + Wolfi (Chainguard) base image.
- **Static binary**: CGO_ENABLED=0, stripped, no external runtime dependencies.
