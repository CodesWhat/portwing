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
- `scripts/drydock-compat-check.sh` — 35-check live compatibility suite against a running agent

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

- **Commits:** plain Conventional Commits, no emoji — `<type>(scope): <description>` (see CONTRIBUTING.md). Enforced by lefthook via `scripts/validate-commit-msg.sh`.
- **Branches:** `main` is production; one active dev branch is the next release; feature branches merge into the dev branch promptly and are deleted after merge.
- **Tests:** table-driven with `httptest`; anything touching goroutines runs under `-race` in CI — write tests accordingly (no unsynchronized `httptest.ResponseRecorder` access from a handler goroutine; wrap with a mutex-guarded recorder).
- **Errors:** wrap with `fmt.Errorf("context: %w", err)`; structured logging via `log/slog` only.

## CI map

`ci-verify.yml` calls the organization Go and Node workflows by a full commit SHA; fixed commands live in `scripts/ci/`, while Portwing keeps its fuzz inventory, CodeQL category, and dependency review locally. It runs lint, test -race, the coverage floor, fuzz smoke, web builds, workflow security, and release-config checks on every applicable push/PR. `quality-fuzz-nightly.yml` (5m per fuzzer) · `quality-integration.yml` (real dockerd) · `quality-integration-engines.yml` (weekly, the same suite against a pinned Docker Engine matrix) · `quality-mutation-monthly.yml` (Gremlins) · `security-grype.yml` (Grype, gosec) · `security-scorecard.yml` (OpenSSF) · `release-cut.yml` → `release.yml` (GoReleaser + cosign + provenance, plus a per-platform Grype scan of the actual published image; see RELEASING.md). `greptile.json` pins Greptile to `skipReview: AUTOMATIC` so it never reviews on its own; `greptile.yml` summons it as an on-demand second opinion when a PR gets the `second-opinion` label, alongside CodeRabbit's automatic review.

### Quality lane history

The scheduled quality lanes print their headline numbers to one run's log and
step summary and nothing else, so a regression that is slow enough to stay
inside its threshold every single week is invisible in every single run. The
`quality-soak-weekly.yml` and `quality-mutation-monthly.yml` lanes each end
with a separate `history` job that appends that run's numbers to
`quality-history`, an orphan branch in this repository holding one append-only
JSONL file per lane (`soak.jsonl`, `mutation.jsonl`, and
`fuzz-nightly.jsonl`/`bench.jsonl` when those lanes adopt it).

It is a separate job on purpose. `contents: write` has to live somewhere for
the push, and the measuring jobs run the product: the soak job drives the agent
under load for four hours, and the Gremlins matrix executes mutated source and
a third-party binary. A write-scoped credential in either would be reachable
through anything those steps could have rewritten, including the append script
itself. The `history` job checks the tree out fresh, downloads the run's
artifacts, and runs nothing but jq and git; the soak numbers travel as job
outputs plus the existing `soak-output.txt` artifact, and each mutation matrix
leg uploads a one-file `mutation-history-<package>-<run_id>` record for it to
read. Downloaded records are data: validated as JSON and passed as arguments,
never sourced.

Read a lane's series with `scripts/quality-history.sh <lane> [--last N]`, which
fetches the branch into a throwaway clone and prints a table; `--json` gives
the raw records. Records are written by `scripts/ci/quality-history-append.sh`,
which is deliberately incapable of failing its caller: an append that cannot
reach the remote warns and exits 0, because a trend surface must never be able
to turn a green quality lane red. Only `schedule` and `workflow_dispatch` runs
append, gated both in the workflow `if:` and in the script.

The branch is never merged and never released. Nothing on a trunk branch
changes when a lane runs, which is what keeps the house rule that committed
generated artifacts only move at a release cut.
`scripts/quality-history-config-test.sh` holds both lanes to the write
scoping, including that the recording job never grows a `setup-go` or a `go
build`, and `scripts/quality-history-script-test.sh` drives the appender
against a real git remote (bootstrap, append, a push rejected by a concurrent
writer, and a repeated identical record that must not become a second row).
