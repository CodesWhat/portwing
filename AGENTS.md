# AGENTS.md

Guidance for coding agents working in this repository.

## What is Portwing?

Portwing is a security-first remote Docker agent written in Go. It exposes a transparent Docker API proxy over the local Docker socket, plus higher-level endpoints for container lifecycle, compose stack management, exec, and events. It runs in two modes: **standard** (inbound HTTP/S server on `PORT`, default 3000) and **edge** (outbound WebSocket tunnel to `DRYDOCK_URL` — no inbound ports). Two adapters: **drydock** (native `dd:*` protocol + SSE compatibility for [drydock](https://github.com/CodesWhat/drydock)) and **generic** (clean REST API defined in `api/openapi.yaml`).

## Repository structure

- `cmd/portwing/` — entrypoint; subcommands: serve (default), `keygen`, `hash-token`, `version`
- `internal/config/` — env-var configuration (no flags, no config files)
- `internal/server/` — standard-mode HTTP server, auth middleware, rate limiting, audit log, metrics
- `internal/auth/` — Ed25519 per-request auth: key registry (`authorized_keys` format), nonce LRU, request verification, keygen, enrollment
- `internal/edge/` — edge-mode WebSocket client (outbound tunnel)
- `internal/docker/` — Docker socket client (proxy, hijack, streaming)
- `internal/adapter/` — adapter interface + `drydock/` and shared types; `internal/generic/` — generic REST adapter
- `internal/mcp/` — Model Context Protocol server (JSON-RPC over HTTP)
- `internal/protocol/` — wire types shared between standard and edge modes; `version.go` holds `AgentVersion`
- `api/openapi.yaml` — generic adapter API contract
- `docs/` — security model, drydock integration notes
- `scripts/drydock-compat-check.sh` — 31-check live compatibility suite against a running agent

## Build, test, lint

```bash
go build ./cmd/portwing              # build
go test -race ./...                 # all tests (race detector is mandatory)
golangci-lint run                   # lint (config: .golangci.yml, v2 schema)
go test -tags integration ./internal/integration/   # needs a real dockerd

# Fuzzers (5s smoke; CI runs 60s, nightly runs 5m):
go test -run='^$' -fuzz='^FuzzParsePHC$' -fuzztime=5s ./internal/server/
go test -run='^$' -fuzz='^FuzzParseTrustedProxies$' -fuzztime=5s ./internal/server/
go test -run='^$' -fuzz='^FuzzParseImageRef$' -fuzztime=5s ./internal/adapter/
go test -run='^$' -fuzz='^FuzzParseLabels$' -fuzztime=5s ./internal/adapter/drydock/
go test -run='^$' -fuzz='^FuzzMCPHandler$' -fuzztime=5s ./internal/mcp/

# Live drydock-compat smoke (agent must be running):
./scripts/drydock-compat-check.sh http://localhost:3000 <token>
```

## Hard invariants — do not break these

1. **Dependency policy: three direct deps, period.** `gorilla/websocket`, `google/uuid`, `golang.org/x/crypto`. Everything else is stdlib. Do not add a dependency without explicit maintainer approval; all crypto is stdlib (`crypto/ed25519`, `crypto/subtle`, `x/crypto/argon2`).
2. **`statusRecorder` must forward `Flush`, `Hijack`, and `Unwrap`** (`internal/server/middleware.go`). SSE streaming and Docker exec/attach hijacking break silently otherwise. Regression test: `TestAuthMiddlewarePreservesStreamingInterfaces`.
3. **Rate limiting runs before credential verification** — failed-auth accounting must stay cheap and happen first, including on the enrollment path (`rateLimitOnly` records 401s).
4. **Nonce replay protection: `NonceLRU.Add` is the atomic authority.** Never gate acceptance on `Seen()` alone — that's a TOCTOU race. `Seen()` is only a cheap pre-verify reject.
5. **`AgentVersion` stays a `var`** (`internal/protocol/version.go`) — GoReleaser injects it with `-X`, which silently does nothing to a `const`.
6. **`dd:watcher-snapshot` containers must serialize as a JSON array, never `null`** — drydock's `handleWatcherSnapshotEvent` prunes from it; `nil` slices must be normalized to `[]adapter.Container{}`.
7. **macOS test sockets: unix socket paths are limited to 104 bytes on darwin.** Use `os.MkdirTemp("", "lk")` for socket dirs in tests, never `t.TempDir()` (its path is too long on macOS runners).
8. **Shell scripts target bash 3.2** (macOS system bash). Under `set -u`, empty-array expansion must use `${arr[@]+"${arr[@]}"}`; command substitutions feeding a pipeline that may legitimately fail need `|| true` under `set -e`.
9. **Auth failures return 401 with an `X-Portwing-Reason` header** (`timestamp-skew`, `replay`, `unknown-key`, `invalid-signature`) — the compat script and drydock both key off these.

## Conventions

- **Commits:** emoji conventional commits — `<emoji> <type>(scope): <description>` (see CONTRIBUTING.md). Enforced by lefthook via `scripts/validate-commit-msg.sh`.
- **Branches:** `main` is production; one active dev branch is the next release; feature branches merge into the dev branch promptly and are deleted after merge.
- **Tests:** table-driven with `httptest`; anything touching goroutines runs under `-race` in CI — write tests accordingly (no unsynchronized `httptest.ResponseRecorder` access from a handler goroutine; wrap with a mutex-guarded recorder).
- **Errors:** wrap with `fmt.Errorf("context: %w", err)`; structured logging via `log/slog` only.

## CI map

`ci.yml` (lint, test -race, fuzz smoke, build matrix, zizmor, actionlint) on every push/PR · `quality-fuzz-nightly.yml` (5m per fuzzer) · `quality-integration.yml` (real dockerd) · `quality-mutation-monthly.yml` (Gremlins) · `security-vuln-weekly.yml` (govulncheck, grype, gosec) · `security-scorecard.yml` (OpenSSF) · `release-cut.yml` → `release.yml` (GoReleaser + cosign + provenance; see RELEASING.md).
