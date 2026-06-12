# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- **Release pipeline**: pin GoReleaser to the `~> v2` major line (was `latest`) in both the release workflow and the CI config-check job, so neither can silently jump to a future GoReleaser v3 and to clear the action's "using 'latest' as default version" advisory.

### Fixed

- **Flaky fuzz smoke / gating CI**: the Go fuzzing harness intermittently failed with a spurious `context deadline exceeded` (no crash, no slow input — verified handlers stay sub-10ms on adversarial inputs). On many-core machines Go fuzzing's default one-worker-per-core saturates every core and starves the coordinator goroutine until a worker misses its sync deadline. Both the pre-push hook and the gating CI fuzz job now cap fuzz worker count to `max(1, min(4, cores-1))` so the coordinator always keeps a core, which *prevents* the starvation; CI additionally retries the residual known `-fuzztime` boundary race once as a backstop (never retrying a real crash).

## [0.2.0] - 2026-06-12

### Added

- **Ed25519 per-request authentication**: signed requests via `X-Lookout-Key-ID` / `X-Lookout-Timestamp` / `X-Lookout-Nonce` / `X-Lookout-Signature` headers, verified against an `authorized_keys` file (`AUTHORIZED_KEYS`). Replay protection via nonce LRU (`NONCE_LRU_SIZE`) and timestamp window (`MAX_CLOCK_SKEW_SECONDS`), SIGHUP hot-reload of the key file, `lookout keygen` CLI subcommand, `X-Lookout-Reason` diagnostic header on 401s, and signed hello for edge mode (`PRIVATE_KEY_FILE`).
- **Key enrollment**: optional single-use `ENROLLMENT_TOKEN` (`POST /api/lookout/enroll`) for bootstrapping the first Ed25519 key — burned on first use, rate-limited, and audit-logged.
- **Repository infrastructure**: hardened CI (SHA-pinned actions, harden-runner, zizmor, actionlint), five Go fuzz targets (60s in CI, 5m nightly), integration suite against a real Docker daemon, weekly vulnerability scans (govulncheck/grype/gosec), monthly mutation testing, OpenSSF Scorecard, CodeQL, Dependabot, and a release-cut → release pipeline with CHANGELOG validation and post-publish verification.
- **Community and policy docs**: CONTRIBUTING, CODE_OF_CONDUCT, SECURITY (threat model + private advisory reporting), RELEASING, AGENTS, issue templates, CODEOWNERS.
- **Deployment examples**: hardened Docker Compose files for standard, edge, and sockguard-layered deployments (`examples/`), all `read_only` + `cap_drop: [ALL]` + `no-new-privileges` with secrets-based tokens.
- **Git hooks**: lefthook pipeline (lint, race tests, govulncheck, fuzz smoke, goreleaser dry-run, workflow checks) and an emoji-conventional-commit message validator.
- **Supply-chain pipeline**: cosign keyless signing of release archives (`checksums.txt.bundle`) and container images — both the per-arch images and the multi-arch manifest lists — CycloneDX SBOM generation via syft, and SLSA build provenance attestation via `actions/attest-build-provenance` (activated on public repositories).
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
