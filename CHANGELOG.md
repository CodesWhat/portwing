# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Removed

- **Dead `DOCKER_HOST` config surface**: the top-level `DOCKER_HOST` environment variable and the corresponding `Config.DockerHost` field never had a consumer — Portwing only ever dials the Docker daemon over the Unix socket (`DOCKER_SOCKET`). Documentation is now unix-socket-only; the unrelated `DOCKER_HOST` entry in the Compose child-process env var denylist (which blocks a stack from redirecting the daemon a compose operation targets) is unchanged.

## [0.6.0] - 2026-07-10

### Added

- **Agent info in the Drydock UI**: the `dd:ack` event now reports real host memory (`memoryGb`, read from `/proc/meminfo` with no cgo, rounded to one decimal GiB; 0 on non-Linux hosts), the agent's `logLevel`, and its `pollInterval` (as a Go duration string), so standard-mode agents no longer show 0 GB / blank runtime details in Drydock.
- **Edge-mode container deletion**: added the `dd:container_delete_request` / `dd:container_delete_response` wire message pair so Drydock's edge-mode `AgentClient.deleteContainer()` can remove containers over the WebSocket tunnel, matching the existing `dd:container_log_request` support for logs.
- **Container environment variables**: `RuntimeDetails` now includes `env` (`[]{key, value}`), parsed from Docker's `Config.Env`, matching Drydock's `ContainerRuntimeEnv` shape. Redaction of sensitive values remains Drydock's responsibility.
- **`COMPATIBILITY.md`**: canonical cross-repo version matrix (portwing × Drydock × sockguard preset × wire-compat constant) at the repo root.

### Fixed

- **Edge-mode hello rejection diagnostics**: when the Drydock controller rejects an agent's hello (bad signature, unknown agent, etc.), the agent now logs and surfaces the controller's `code` and `message` instead of a generic "expected welcome, got \"error\"" message.

### Security

- **Go toolchain bumped to 1.26.5 and `golang.org/x` dependencies refreshed** to clear published advisories. `go1.26.5` fixes the reachable `crypto/tls` vulnerability GO-2026-5856 (CVE-2026-42505) — called from the HTTPS server, the Docker client, and the banner writer — and the `os.Root` symlink-handling issue GO-2026-4970 (CVE-2026-39822). `golang.org/x/crypto` is updated 0.53.0 → 0.54.0 and `golang.org/x/sys` 0.46.0 → 0.47.0. The remaining osv-scanner finding, GO-2026-5932 in `golang.org/x/crypto/openpgp`, is unreachable (the agent imports only `x/crypto/argon2`) and has no fixed version, so it is triaged out in `.qlty/qlty.toml` rather than chased.
- **CI egress lockdown**: every workflow job now runs harden-runner with `egress-policy: block` and a per-job `allowed-endpoints` allowlist (24 jobs across 11 workflows) — previously all jobs ran in audit mode, which logs but does not stop exfiltration. Allowlists were derived from harden-runner audit telemetry across recent runs of every workflow, cross-checked against StepSecurity's recommended per-job policies, and adversarially reviewed per file (notably: `sum.golang.org` is allowed only for jobs that `go install` a tool outside the repo's `go.sum`; Docker Hub's dual CDN hostnames are both listed for image-pulling jobs; speculative endpoints were dropped). A compromised action or dependency can no longer phone home from CI.

### Changed

- **BREAKING: the container image now runs as the non-root `portwing` user (UID 65532) by default.** Previously the agent ran as root inside the container and could open the host Docker socket implicitly; deployments must now grant the socket's group explicitly — `group_add: ["${DOCKER_SOCK_GID}"]` in compose, `--group-add $(stat -c '%g' /var/run/docker.sock)` with `docker run`, or `supplementalGroups` in Kubernetes (the shipped k8s manifests already ran non-root and need no change). Mounted credential files must be readable by UID 65532 (`chown 65532:65532` + `chmod 0400`). `/data/stacks` and `/home/portwing` are pre-owned by the user so read-only-rootfs deployments and volume initialization keep working, and `DOCKER_CONFIG` defaults to `/tmp/.docker` so `docker login` during compose deploys works with a read-only root filesystem. `user: "0:0"` restores the old behavior. All examples and docs updated; this closes the last open finding from the June security audit.
- **Hermetic artifact builds**: `setup-go` caching is disabled in the release workflow and the GoReleaser config check, so no restored module/build cache can influence published artifacts. This clears zizmor's cache-poisoning findings at the root, and the now-redundant suppression config (`.github/zizmor.yml`, whose rationale predated the repo going public) is deleted — the workflow audit runs suppression-free.

## [0.5.1] - 2026-07-03

### Changed

- **Coverage reporting moved from Codecov to Qlty Cloud.** Part of the org-wide consolidation onto Qlty (one vendor for code quality and coverage). CI now publishes the Go coverprofile to Qlty Cloud via GitHub OIDC — no stored coverage token — and enforces a vendor-free statement-coverage floor (96%) with `go tool cover`, replacing the Codecov project ratchet and `codecov.yml`. A coverage badge was added to the README.
- **Blocking `qlty check` gate in CI.** A new CI job fails the build on any new qlty finding (shellcheck, hadolint, markdownlint, yamllint, and friends), with the linter configs (`.qlty/qlty.toml`, `.markdownlint.json`, `.yamllint.yml`) checked in and the existing scripts and Dockerfiles cleaned up to pass it. Standardizes the quality tooling with the sibling CodesWhat repos.
- **Marketing and docs sites converted to the shared CodesWhat web shell**, bringing chrome parity with the sibling project sites (CLI demo, comparison hub, OG/share imagery, favicons), and the configured site domain corrected from `getportwing.dev` to `getportwing.com`. Source-only — nothing is deployed yet.

### Fixed

- **Spurious edge compat-level mismatch warning**: the `welcome`-frame `serverCompatLevel` was compared to the agent's compat level as an exact string, so any minor/patch difference (e.g. Drydock's `1.4` vs the agent's `1.4.0`) logged a "controller compat level mismatch" warning on every connect. The comparison is now major-version-only, matching Drydock's semantics.
- **Container-poll startup/shutdown race**: `ListenAndServe` created the poll context and wrote the cancel field after startup, racing a concurrent `Shutdown` that reads it — a shutdown arriving early could also observe nil and leak the polling goroutine. Both fields are now set once at construction and never reassigned.

## [0.5.0] - 2026-06-23

### Added

- **Application/request Prometheus metrics**: `/metrics` and `/_portwing/metrics` now also expose `portwing_http_requests_total{method,code}`, `portwing_http_request_duration_seconds` (histogram), `portwing_http_requests_in_flight` (gauge), `portwing_auth_failures_total{reason}`, and `portwing_rate_limited_total`. The endpoints and their existing build/host/per-container series are unchanged.
- **Audit ring buffer and `GET /_portwing/audit`**: setting `AUDIT_BUFFER_SIZE` (default 256, 0 disables) retains the most recent audit records in memory for pull-based retrieval at the new authenticated endpoint `GET /_portwing/audit`, which returns `{"records":[...],"count":N}` newest-first with an optional `?limit=N` query parameter. The buffer is independent of `AUDIT_LOG` and works even when the slog sink is off. The JSON record schema is unchanged (ts/event/actor/method/path/outcome/status/duration_ms plus event-specific fields).
- **Kubernetes deployment examples**: hardened DaemonSet manifests for standard and edge mode under `examples/kubernetes/` (read-only rootfs, dropped capabilities, non-root, node-socket mount, health probes).
- **Broadened test coverage**: a fuzz target for the wire `Envelope` parser (`FuzzEnvelope`), HTTP handler tests for the server (auth wrap, compose body limit, MCP method gating), drydock `HandleMessage` branch tests, and compose `validateRequest` injection-vector tests. A second cross-package unit backfill (edge wire contract, docker client/compose/events, config, metrics, server, generic, pool, drydock adapter) lifts overall coverage from ~46% to ~70%.
- **Configurable exec TTY**: the edge `exec_start` frame now honors a `tty` field, letting the controller request a non-TTY exec; it defaults to `true` when the field is absent, preserving the previous always-TTY behavior.
- **Codecov coverage ratchet**: CI uploads `coverage.out` to Codecov on every run, and `codecov.yml` enforces a no-regression project gate (`target: auto`, 1% wobble) plus an 80% patch gate on new/changed lines. The fuzz/soak/integration tiers are excluded from the accounting, so the number is a floor.

### Security

- **Pre-auth request body cap**: signed (Ed25519) requests are buffered through `http.MaxBytesReader` at 1 MB before signature verification — previously a 64 MB `io.LimitReader` with no read timeout, which let a slow-drip sender hold a large per-connection buffer before authenticating. Over-limit bodies now get 413. The exec (10 MB) and MCP (1 MB) limits are unchanged.
- **Registry-credential and proxy-parameter validation**: compose `registryAuth.server` must be a valid `https` URI before `docker login` runs (blocks redirecting a shared registry credential to an attacker-controlled host); container-log `since`/`until` and the other log query params are built with `url.Values` rather than string concatenation (blocks `&follow=1`-style injection that could pin an indefinitely blocking stream); container IDs/names are validated against the Docker charset before they are interpolated into Docker API paths.
- **Private signing-key permission check**: `PRIVATE_KEY_FILE` is rejected when it is group/world-accessible (looser than 0600), matching the existing `authorized_keys` check.
- **Outbound TLS posture**: the edge controller dial pins `MinVersion: TLS 1.2` (matching the inbound server), warns when `TLS_SKIP_VERIFY=true`, and refuses to send an unauthenticated hello when Ed25519 signing fails and no token fallback is configured (previously a silent downgrade).
- **Docker daemon error bodies are no longer forwarded to clients**: non-2xx Docker responses surface a generic `docker error (status N)` to API/MCP callers while the raw body is logged server-side; the MCP log demuxer also caps per-frame allocation at 256 KiB.
- **Nonce-cache behavior documented**: `SECURITY.md` now describes the replay window and fail-open-when-full semantics of the nonce LRU and why it is not reachable by an unauthenticated caller.

### Fixed

- **Exec terminal resize was permanently broken in edge mode**: post-startup resizes were sent to Docker using the controller's exec ID instead of the Docker-assigned exec instance ID, so every resize after the initial one 404'd. The session now records the Docker exec ID and uses it for resizes.
- **Exec resize no longer blocks the read pump**: `HandleResize` previously ran the Docker resize round-trip (up to ~450 ms of retries) inline on the WebSocket read pump, stalling pings and every other session. Resizes are now enqueued onto the same single per-session input drainer as keystrokes, preserving order and keeping the read pump free.
- **Exec sessions no longer leak across reconnects**: every live exec session is torn down when a controller connection ends, and the per-session goroutines recover from panics instead of taking down the agent process.
- **Goroutine lifecycle on shutdown**: the container-poll loop, the SIGHUP reload goroutine, and the rate-limiter / nonce-cache cleanup tickers now stop on shutdown, and the standard-mode audit log file is flushed and closed.
- **Container refresh no longer issues a Docker inspect per container every poll**: the periodic inventory refresh caches the built container and re-inspects only when the list entry's state/status/image changes (initial inventory still inspects every container).
- Removed two dead struct fields (`SSEClient.done`, `Collector.prevTime`); constrained the MCP route to `POST`; switched several `io.EOF` string/`==` comparisons to `errors.Is`; pooled the 32 KiB proxy stream buffer; and dropped an unnecessary `[]byte`→`string` copy on the request-body path.
- **Edge client aligned with the Drydock controller wire contract**: an Ed25519 hello-signing failure is now fatal instead of silently falling back to a token hash and reconnecting forever against a controller that only accepts Ed25519; a 404 on the WebSocket upgrade fails fast (naming `DD_EXPERIMENTAL_PORTWING` as the likely cause) rather than retrying indefinitely; the `welcome` frame is parsed so the agent honors the controller's `pollInterval` and warns on a `serverCompatLevel` mismatch; and inbound `error` frames are surfaced in the read pump instead of being dropped to the adapter default.

### Changed

- **Security-model docs truthed up**: removed fabricated `CVE-2026-*` identifiers and a fake "Arcane Docker Manager" advisory from `docs/security-model.md` and the docs-site `security-model.mdx`, replacing them with described vulnerability classes; corrected the Compose-guard control reference to "Control 8 — Compose input guards"; fixed a recurring "misonfigure" typo; and synced stale version strings. The marketing comparison now lists edge mode as early-access (Drydock 1.5+) instead of planned.
- **Tooling**: enabled the `unused` linter in `.golangci.yml`, pinned GoReleaser to an exact release (`v2.16.0`) instead of the floating `~> v2`, and converted `interface{}` to `any` across the codebase.
- **Edge mode now requires `PRIVATE_KEY_FILE`.** The Drydock controller is Ed25519-only and rejects token-only agents, so the agent fails fast at startup when running in edge mode without a signing key instead of looping on a rejected hello. **Breaking for token-only edge deployments:** provision a `PRIVATE_KEY_FILE` (and enroll its public key) before upgrading. Standard mode is unaffected.

## [0.4.0] - 2026-06-22

### Added

- **Tier-3 monthly deep fuzz**: `quality-fuzz-monthly.yml` gives each of the five fuzz targets a 1-hour budget on the first of the month (dispatchable to longer budgets before a release), completing the smoke → nightly → monthly fuzz tiering. Crash corpora retain for 180 days.
- **Weekly soak test**: `quality-soak-weekly.yml` runs the agent (generic adapter) against a mock Docker upstream under a sustained loadgen mix — inventory/version/proxy reads plus SSE subscriber connect/hold/disconnect churn — and fails if its resident set grows past a configurable budget (64 MiB default) over a multi-hour soak. New harness under `benchmarks/cmd/{mockdocker,loadgen}` driven by `scripts/soak.sh`. Catches the long-lived-agent leak profile the unit/integration/fuzz tiers don't.
- **Monthly benchmark tracking**: Go benchmarks on the per-request hot paths (auth middleware, Argon2id verify — cold derivation vs. warm SHA-256 cache, client-IP extraction, rate limiter) and the parse paths (PHC, image-ref, Drydock labels, trusted-proxy CIDRs, MCP dispatch). `quality-bench-monthly.yml` reruns them with `-benchmem -count=5` on the first of each month and retains the results for 90 days so a ns/op or allocs/op regression shows up month over month. Completes the test-posture parity with sockguard.
- **Edge tunnel test harness**: a dedicated unit-test harness for the edge WebSocket tunnel — an in-memory controller/agent WebSocket pair plus a consumer-side `dockerAPI` seam so the exec sessions and request fan-out run against a scripted fake Docker daemon with no live socket. Covers hello signing, request dispatch and concurrency rejection, the exec-session lifecycle (start/input/resize/output/end), ordered input replay, and send-path eviction. Lifts `internal/edge` exec/dispatch coverage from effectively zero to ~54%.

### Fixed

- **Edge exec input ordering**: `exec_input` that arrived immediately after `exec_start` could be dropped, because the session was only registered after the Docker `CreateExec`/`StartExec` round-trip completed. The session is now registered synchronously up front and early input is buffered in arrival order by a single per-session writer goroutine, then replayed once the exec connection is live — keystrokes typed before the shell comes up are no longer lost or reordered.
- **Edge outbound backpressure**: every sender (exec output, request/stream responses, metrics, pings) previously wrote the WebSocket directly under one mutex with no write deadline, so a single slow or wedged controller could head-of-line-block every session, stall the read pump, and hang the agent indefinitely. Outbound frames now funnel through a single `sendPump` goroutine fronting a bounded queue with a per-frame write deadline; a controller that can't keep up is evicted and reconnected rather than dropping frames (which would hang a request or corrupt a stream).
- **Grype container image scan**: the scheduled image scan failed on a stale cached rootfs. The scan now rebuilds the Wolfi rootfs without the BuildKit layer cache so weekly/manual runs pick up current packages, and `.grype.yaml` carries a scoped ignore for `GO-2026-4610` only where it is embedded in `/usr/bin/docker-compose` (a Windows Docker CLI plugin search-path advisory that does not apply to the Linux image). Also cleared the gosec `G115` finding in the mock Docker benchmark with a constant-safe log-frame length.

### Changed

- **Standardized dependency/CVE scanning on Grype + govulncheck; Snyk stays off Portwing.** Snyk's GitHub SCM integration scans the full Go *module requirement graph* (`go mod graph`) instead of the compiled build graph, so it flags advisories in modules that transitive deps merely *require* but the binary never links in (nothing in `go list -deps ./...`, nothing reachable per govulncheck, clean under Grype). That's a methodology gap, not staleness, so it's being decommissioned org-wide. Portwing never wired Snyk into the repo (no `.snyk` policy, no workflow step, no README badge), so there was nothing to strip out on the repo side. govulncheck (Go call-graph reachability) and Grype (the built image's binary build-info, plus `go.mod`/`go.sum` and the npm lockfiles) already cover dependencies accurately. The existing weekly scan is consolidated into `security-grype.yml`, which now also runs on pull requests (path-filtered to source/deps/Dockerfile/the workflow itself), keeps the weekly cron and manual dispatch, guards the heavy container build off PRs (govulncheck plus the dependency scan give fast PR coverage), gives each scanner a distinct code-scanning `category` so the Grype image and dependency SARIF no longer clobber each other in the Security tab, and runs gosec in report-only mode (`-no-fail`) so its heuristic findings still feed the Security tab without gating the build (CodeQL, Grype, and govulncheck handle the gating).
- **Pinned the Go toolchain to `go1.26.4` and made it the single source of truth for CI.** `go.mod` now carries a `toolchain go1.26.4` directive (it previously declared only `go 1.26.0`), so every build — local, CI, and release — runs on a stdlib past the reachable `crypto/x509` / `net/url` advisories (GO-2026-4599/4600/4601, fixed in 1.26.1) instead of whatever 1.26.x a runner happened to install. Every workflow's `setup-go` step switched from the floating `go-version: "1.26"` to `go-version-file: go.mod`, so the pin now governs the build, the govulncheck/Grype scans, and the release in lockstep — bump the toolchain in one place to move them all. `govulncheck ./...` is clean on 1.26.4.
- **Refreshed pinned base-image digests** via Dependabot: `golang:1.26.4-alpine` (builder) and `cgr.dev/chainguard/wolfi-base:latest` (runtime rootfs) moved to current digests, rebuilding the Wolfi packages with the latest upstream security fixes. Tag pins are unchanged — digests only.

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
