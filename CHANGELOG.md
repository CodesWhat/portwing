# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Tier-3 monthly deep fuzz**: `quality-fuzz-monthly.yml` gives each of the five fuzz targets a 1-hour budget on the first of the month (dispatchable to longer budgets before a release), completing the smoke → nightly → monthly fuzz tiering. Crash corpora retain for 180 days.
- **Weekly soak test**: `quality-soak-weekly.yml` runs the agent (generic adapter) against a mock Docker upstream under a sustained loadgen mix — inventory/version/proxy reads plus SSE subscriber connect/hold/disconnect churn — and fails if its resident set grows past a configurable budget (64 MiB default) over a multi-hour soak. New harness under `benchmarks/cmd/{mockdocker,loadgen}` driven by `scripts/soak.sh`. Catches the long-lived-agent leak profile the unit/integration/fuzz tiers don't.
- **Monthly benchmark tracking**: Go benchmarks on the per-request hot paths (auth middleware, Argon2id verify — cold derivation vs. warm SHA-256 cache, client-IP extraction, rate limiter) and the parse paths (PHC, image-ref, Drydock labels, trusted-proxy CIDRs, MCP dispatch). `quality-bench-monthly.yml` reruns them with `-benchmem -count=5` on the first of each month and retains the results for 90 days so a ns/op or allocs/op regression shows up month over month. Completes the test-posture parity with sockguard.
- **Edge tunnel test harness**: a dedicated unit-test harness for the edge WebSocket tunnel — an in-memory controller/agent WebSocket pair plus a consumer-side `dockerAPI` seam so the exec sessions and request fan-out run against a scripted fake Docker daemon with no live socket. Covers hello signing, request dispatch and concurrency rejection, the exec-session lifecycle (start/input/resize/output/end), ordered input replay, and send-path eviction. Lifts `internal/edge` exec/dispatch coverage from effectively zero to ~54%.

### Fixed

- **Edge exec input ordering**: `exec_input` that arrived immediately after `exec_start` could be dropped, because the session was only registered after the Docker `CreateExec`/`StartExec` round-trip completed. The session is now registered synchronously up front and early input is buffered in arrival order by a single per-session writer goroutine, then replayed once the exec connection is live — keystrokes typed before the shell comes up are no longer lost or reordered.
- **Edge outbound backpressure**: every sender (exec output, request/stream responses, metrics, pings) previously wrote the WebSocket directly under one mutex with no write deadline, so a single slow or wedged controller could head-of-line-block every session, stall the read pump, and hang the agent indefinitely. Outbound frames now funnel through a single `sendPump` goroutine fronting a bounded queue with a per-frame write deadline; a controller that can't keep up is evicted and reconnected rather than dropping frames (which would hang a request or corrupt a stream).

## [0.3.0] - 2026-06-15

### Added

- **Startup banner**: the agent prints a centered truecolor half-block render of the logo plus a one-line `version · mode · adapter` summary at startup. Color is emitted only to a TTY (or under `FORCE_COLOR`) and suppressed under `NO_COLOR`, with ANSI escapes stripped so piped and log output stay clean.

### Changed

- **Renamed to Portwing** (formerly Lookout): the project name, Go module path (`github.com/codeswhat/portwing`), binary, Docker image, and every `lookout`-prefixed identifier are now `portwing`. **Breaking for anyone running a pre-release build:** the auth header `X-Lookout-Token` is now `X-Portwing-Token` (and the Ed25519 request headers `X-Lookout-Key-ID` / `-Timestamp` / `-Nonce` / `-Signature` are now `X-Portwing-*`), and the Prometheus metrics are renamed from `lookout_*` to `portwing_*` — update any clients, scrapers, dashboards, and alert rules accordingly. There is no backward-compatible alias.
- **Release pipeline**: pin GoReleaser to the `~> v2` major line (was `latest`) in both the release workflow and the CI config-check job, so neither can silently jump to a future GoReleaser v3 and to clear the action's "using 'latest' as default version" advisory.
- **Docker release build**: migrate from the deprecated GoReleaser `dockers` + `docker_manifests` blocks to a single `dockers_v2` entry that builds all three platforms (linux/amd64, linux/arm64, linux/arm/v7) in one buildx run and pushes a single multi-arch OCI index per tag. The two per-arch release Dockerfiles are unified into one `Dockerfile.release` that selects Wolfi (amd64/arm64) or Alpine (armv7) by `TARGETARCH`. The published image now also carries a Syft image SBOM attestation, and `:latest` is no longer moved by prereleases.

### Fixed

- **Edge reconnect backoff**: a long-lived edge session that drops now reconnects from `RECONNECT_DELAY` instead of inheriting stale exponential backoff from earlier connect failures (SPEC §13.1).
- **Edge read deadline**: the WebSocket read deadline is now held at a steady-state `max(2·HEARTBEAT_INTERVAL, 60s)` and re-armed on every received message, so a controller that goes silent (stops answering pings) is detected and triggers a reconnect instead of blocking forever (SPEC §13.2).
- **Flaky fuzz smoke / gating CI**: the Go fuzzing harness intermittently failed with a spurious `context deadline exceeded` (no crash, no slow input — verified handlers stay sub-10ms on adversarial inputs). On many-core machines Go fuzzing's default one-worker-per-core saturates every core and starves the coordinator goroutine until a worker misses its sync deadline. Both the pre-push hook and the gating CI fuzz job now cap fuzz worker count to `max(1, min(4, cores-1))` so the coordinator always keeps a core, which *prevents* the starvation; CI additionally retries the residual known `-fuzztime` boundary race once as a backstop (never retrying a real crash).

## [0.2.0] - 2026-06-12

### Added

- **Ed25519 per-request authentication**: signed requests via `X-Portwing-Key-ID` / `X-Portwing-Timestamp` / `X-Portwing-Nonce` / `X-Portwing-Signature` headers, verified against an `authorized_keys` file (`AUTHORIZED_KEYS`). Replay protection via nonce LRU (`NONCE_LRU_SIZE`) and timestamp window (`MAX_CLOCK_SKEW_SECONDS`), SIGHUP hot-reload of the key file, `portwing keygen` CLI subcommand, `X-Portwing-Reason` diagnostic header on 401s, and signed hello for edge mode (`PRIVATE_KEY_FILE`).
- **Key enrollment**: optional single-use `ENROLLMENT_TOKEN` (`POST /api/portwing/enroll`) for bootstrapping the first Ed25519 key — burned on first use, rate-limited, and audit-logged.
- **Repository infrastructure**: hardened CI (SHA-pinned actions, harden-runner, zizmor, actionlint), five Go fuzz targets (60s in CI, 5m nightly), integration suite against a real Docker daemon, weekly vulnerability scans (govulncheck/grype/gosec), monthly mutation testing, OpenSSF Scorecard, CodeQL, Dependabot, and a release-cut → release pipeline with CHANGELOG validation and post-publish verification.
- **Community and policy docs**: CONTRIBUTING, CODE_OF_CONDUCT, SECURITY (threat model + private advisory reporting), RELEASING, AGENTS, issue templates, CODEOWNERS.
- **Deployment examples**: hardened Docker Compose files for standard, edge, and sockguard-layered deployments (`examples/`), all `read_only` + `cap_drop: [ALL]` + `no-new-privileges` with secrets-based tokens.
- **Git hooks**: lefthook pipeline (lint, race tests, govulncheck, fuzz smoke, goreleaser dry-run, workflow checks) and an emoji-conventional-commit message validator.
- **Supply-chain pipeline**: cosign keyless signing of release archives (`checksums.txt.bundle`) and container images — both the per-arch images and the multi-arch manifest lists — CycloneDX SBOM generation via syft, and SLSA build provenance attestation via `actions/attest-build-provenance` (activated on public repositories).
- **Prometheus metrics**: `/metrics` and `/_portwing/metrics` endpoints exposing `portwing_build_info`, container count, and host resource metrics (CPU, memory, disk, network).
- **Argon2id token hashing**: `TOKEN_HASH` environment variable accepts an Argon2id PHC string (OWASP-recommended parameters: m=19456 KiB, t=2, p=1). `TOKEN_HASH_FILE` for Docker secrets support. A SHA-256 success cache keeps per-request cost flat after first verification. `portwing hash-token` CLI subcommand generates PHC strings.
- **Bearer auth**: `Authorization: Bearer <token>` header supported in addition to the `X-Portwing-Token` scheme.
- **`TRUSTED_PROXIES`**: configurable CIDR list for trusted reverse proxies; `X-Forwarded-For` is only honored from trusted sources for rate-limiting.
- **Audit logging**: structured JSON audit log (`AUDIT_LOG` env var) recording auth events (success, failure, rate-limit) with IP, user-agent, method, and path.
- **MCP server**: read-only Model Context Protocol server at `/_portwing/mcp` (Streamable HTTP transport, protocol revision 2025-11-25). Tools: `list_containers`, `inspect_container`, `container_logs`, `host_metrics`, `container_stats`.
- **Generic REST adapter**: headless REST+SSE management API (`internal/generic`) for standalone mode without a Drydock platform connection.
- **Drydock route additions**: `GET /api/log/entries`, watcher poll/get/container endpoints, trigger exec/batch endpoints.
- **`dd:watcher-snapshot` SSE event**: full watcher + container inventory payload emitted immediately after `dd:ack` on SSE connect and after every poll cycle, so Drydock can prune stale containers.
- **OpenAPI 3.1 spec**: `api/openapi.yaml` documenting all endpoints, request/response schemas, and security schemes.
- **Security model doc**: `docs/security-model.md` describing the defense-in-depth posture.
- **Watchtower migration guide**: `docs/migrating-from-watchtower.md` for teams migrating from Watchtower.

### Fixed

- **Streaming through auth middleware**: response body flushing and WebSocket hijack now work correctly when wrapped by the auth middleware chain (`statusRecorder` forwards `Flush` and `Hijack`).
- **Version injection**: GoReleaser ldflags now correctly set `protocol.AgentVersion` at build time.

## [0.1.0] - 2025-06-01

### Added

- Initial release of Portwing remote Docker agent.
- **Transparent Docker API proxy**: forwards Docker API requests from the Drydock platform to the local Docker daemon in standard mode.
- **Edge mode**: WebSocket tunnel to Drydock platform (`DRYDOCK_URL` + `TOKEN`) for environments where inbound connections are not possible.
- **Drydock adapter**: full Drydock protocol compatibility — container sync, component sync, watcher/trigger stubs, SSE broadcasting.
- **SSE event stream**: real-time container state events at `/api/events`.
- **Standard mode HTTP server**: `/_portwing/health`, `/_portwing/info`, `/api/containers`, `/api/containers/{id}/logs`, `/api/events`.
- **Token authentication**: `TOKEN` environment variable with timing-safe comparison.
- **Rate limiting**: 10 failed auth attempts per IP per minute.
- **Multi-arch Docker image**: `linux/amd64` and `linux/arm64` via GoReleaser + Wolfi (Chainguard) base image.
- **Static binary**: CGO_ENABLED=0, stripped, no external runtime dependencies.
