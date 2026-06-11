# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Supply-chain pipeline**: cosign keyless signing of container images, SBOM generation via syft, SLSA build provenance attestation via `actions/attest-build-provenance`.
- **Prometheus metrics**: `/metrics` and `/_lookout/metrics` endpoints exposing `lookout_build_info`, container count, and host resource metrics (CPU, memory, disk, network).
- **Argon2id token hashing**: `TOKEN_HASH` environment variable accepts an Argon2id PHC string (OWASP-recommended parameters: m=19456 KiB, t=2, p=1). `TOKEN_HASH_FILE` for Docker secrets support. A SHA-256 success cache keeps per-request cost flat after first verification. `lookout hash-token` CLI subcommand generates PHC strings.
- **Bearer auth**: `Authorization: Bearer <token>` header supported in addition to the `X-Lookout-Token` scheme.
- **`TRUSTED_PROXIES`**: configurable CIDR list for trusted reverse proxies; `X-Forwarded-For` is only honored from trusted sources for rate-limiting.
- **Audit logging**: structured JSON audit log (`AUDIT_LOG` env var) recording auth events (success, failure, rate-limit) with IP, user-agent, method, and path.
- **MCP server**: read-only Model Context Protocol server at `/_lookout/mcp` (Streamable HTTP transport, protocol revision 2025-11-25). Tools: `list_containers`, `inspect_container`, `container_logs`, `host_metrics`, `container_stats`.
- **Generic REST adapter**: headless REST+SSE management API (`internal/generic`) for standalone mode without a Drydock platform connection.
- **Drydock route additions**: `GET /api/log/entries`, watcher poll/get/container endpoints, trigger exec/batch endpoints.
- **`dd:watcher-snapshot` SSE event**: emitted after initial container sync so clients know the snapshot is complete.
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
