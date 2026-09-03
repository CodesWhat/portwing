# Portwing Benchmarks

This document exists for one purpose: a **regression baseline**. Every hot
path an agent runs on every request — auth, token verification, the
Docker-facing parsers, the MCP dispatch loop — has a Go benchmark, and the
monthly `quality-bench-monthly.yml` CI lane runs all of them, compares the
result against a prior run with `benchstat`, and fails when a metric got
measurably worse. This file publishes the methodology behind that lane and
the most recent numbers it produced, so the baseline isn't something you can
only see by downloading a 90-day CI artifact.

It is **not** a competitive benchmark. See
[What this does not measure](#what-this-does-not-measure) for why.

## What gets measured, and by what

Two different things in this repository are both called "benchmarks," and
they measure different failure modes:

- **The `internal/**/*_bench_test.go` files** are standard Go
  `testing.B` benchmarks: `BenchmarkParsePHC`, `BenchmarkArgon2idParamsVerify`,
  `BenchmarkArgon2VerifierVerify`, `BenchmarkRawTokenVerifierVerify`,
  `BenchmarkAuthMiddleware`, `BenchmarkClientIP`,
  `BenchmarkParseTrustedProxies`, `BenchmarkAgentToken`,
  `BenchmarkRateLimiter` (all in `internal/server`), `BenchmarkMCPHandler`
  (`internal/mcp`), `BenchmarkParseImageRef` (`internal/adapter`), and
  `BenchmarkParseLabels` (`internal/adapter/drydock`). These are
  microbenchmarks of single functions and request paths — ns/op, B/op,
  allocs/op — and they're what `quality-bench-monthly.yml` runs and gates.
  **This document's results table is these benchmarks.**
- **The `benchmarks/` directory** (`benchmarks/cmd/loadgen`,
  `benchmarks/cmd/mockdocker`) is a different tool: an HTTP load generator and
  a mock Docker daemon, both built and driven by `scripts/soak.sh`. That's
  the harness behind the *weekly* `quality-soak-weekly.yml` lane, which holds
  the agent under sustained load for hours and watches for RSS growth — a
  leak detector, not a latency benchmark. It has no `testing.B` functions of
  its own and doesn't feed into the table below. If you're looking for the
  soak harness, this isn't it; see `scripts/soak.sh` and
  `quality-soak-weekly.yml`.

## The monthly lane

`quality-bench-monthly.yml` runs on the 1st of every month (and on manual
dispatch):

1. `go test -run='^$' -bench=. -benchmem -count=5 -timeout=20m ./...` —
   `-count=5` is the workflow's chosen sample count, picked for stability
   against a shared runner's noise: with five samples on both sides,
   `benchstat`'s significance test needs the two sample sets to barely
   overlap before it prints a percentage at all, printing `~` otherwise.
2. Fetches the `benchmark-results.txt` artifact from the newest prior
   successful run of this same workflow on the default branch (`gh run
   download`; artifacts expire at 90 days against a monthly cadence, so a
   handful of candidates are normally available). No baseline is committed to
   the repository — a scheduled job writing to the tree is exactly what the
   house rule against cron-generated commits exists to prevent.
3. Runs [`scripts/benchstat-gate.sh`](scripts/benchstat-gate.sh) against each
   candidate, newest first, via the walk-back loop in
   [`scripts/benchstat-walk-baselines.sh`](scripts/benchstat-walk-baselines.sh).
   `benchstat` groups results by the `goos`/`goarch`/`pkg`/`cpu` line Go
   prints, so a run on a different CPU model produces no comparison — the
   walk-back keeps trying older candidates until it finds one on matching
   hardware, or gives up and warns rather than gate on a cross-hardware
   delta.
4. Fails the job when `sec/op` or `B/op` regressed by more than 10% on a
   metric `benchstat` calls statistically significant (p < 0.05). `allocs/op`
   is measured and reported but never gates: it's usually a small integer, so
   a percentage threshold says nothing useful about it. A zero-allocation
   path that starts allocating (`benchstat` prints `?`, an undefined ratio)
   counts as a regression even though it has no percentage.
5. Uploads `benchmark-results.txt` and the comparison summary as a 90-day
   artifact and writes the comparison into the run summary.

An intentional slowdown doesn't have to stay red forever: dispatching the
workflow with `accept_new_baseline: true` skips the comparison once and lets
that run's numbers become the new baseline — but only when the dispatch runs
on the default branch. The "Fetch baseline candidates" step filters
`gh run list` to `--branch` on the default branch, so a run accepted from a
dev branch is never picked up as a candidate by a later run.

Full design reasoning — why a fetched artifact instead of a committed file,
why hardware-matched walk-back, why `sec/op`/`B/op` gate and `allocs/op`
doesn't, why the significance threshold can't usefully be tightened — is in
the comment block at the top of
[`.github/workflows/quality-bench-monthly.yml`](.github/workflows/quality-bench-monthly.yml).

## Baseline

- **Source:** GitHub Actions run
  [33509348993](https://github.com/CodesWhat/portwing/actions/runs/33509348993),
  the most recent successful `quality-bench-monthly.yml` run at the time this
  document was written.
- **Commit:** `bfc8772` on `main`.
- **Run date:** 2026-09-01 (UTC).
- **Runner:** GitHub-hosted `ubuntu-24.04`, `cpu: Intel(R) Xeon(R) 6973P-C`
  (`goos: linux`, `goarch: amd64`).
- **Go:** `go1.26.6` — the toolchain `go.mod` pinned on `main` at that commit.
  `dev/v0.9` has since moved to `toolchain go1.27.0`; the next monthly run
  will record numbers on that toolchain, and `benchstat`'s hardware-match
  walk-back means a run on a new toolchain still compares fine as long as the
  CPU model matches.
- **Command:** `go test -run='^$' -bench=. -benchmem -count=5 -timeout=20m
  ./...` (the exact invocation `quality-bench-monthly.yml` runs).

This run predates the `benchstat` comparison gate landing on `main` (it's
still working its way through the `dev/v0.9` → `main` promotion queue as of
this writing), so it has no comparison to report — only the raw numbers,
which is what the table below reproduces. `-count=5` samples were collapsed
to their median by `benchstat`; with only one run to summarize (not two to
compare), `benchstat` also can't report a confidence interval — that needs
at least 6 samples per side.

### `internal/server` — auth, tokens, PHC parsing

| Benchmark | sec/op | B/op | allocs/op |
| --- | --- | --- | --- |
| `ParsePHC/valid` | 298.6n | 352 | 8 |
| `ParsePHC/wrong_prefix` | 20.63n | 16 | 1 |
| `ParsePHC/malformed` | 162.8n | 144 | 3 |
| `Argon2idParamsVerify/correct` | 15.47m | 19.00Mi | 23 |
| `Argon2idParamsVerify/wrong` | 15.63m | 19.00Mi | 23 |
| `Argon2VerifierVerify/warm_cache_hit` | 99.08n | 32 | 1 |
| `Argon2VerifierVerify/reject` | 15.61m | 19.00Mi | 24 |
| `RawTokenVerifierVerify/match` | 85.49n | 0 | 0 |
| `RawTokenVerifierVerify/mismatch` | 83.52n | 0 | 0 |
| `AuthMiddleware/authorized_raw` | 433.4n | 280 | 6 |
| `AuthMiddleware/rejected_raw` | 722.3n | 1.031Ki | 11 |
| `AuthMiddleware/passthrough_no_auth` | 192.4n | 232 | 5 |
| `ClientIP/direct_no_proxies` | 30.45n | 0 | 0 |
| `ClientIP/trusted_proxy_xff_chain` | 214.9n | 48 | 1 |
| `ClientIP/untrusted_peer` | 46.08n | 0 | 0 |
| `ParseTrustedProxies/cidrs` | 305.8n | 240 | 13 |
| `ParseTrustedProxies/bare_ips` | 512.7n | 312 | 16 |
| `AgentToken/bearer` | 28.98n | 0 | 0 |
| `AgentToken/portwing_header` | 50.77n | 0 | 0 |
| `AgentToken/drydock_secret` | 81.08n | 0 | 0 |
| `RateLimiter/is_rate_limited` | 34.99n | 0 | 0 |
| `RateLimiter/record_failure` | 38.41n | 0 | 0 |
| `RateLimiter/is_rate_limited_parallel` | 76.90n | 0 | 0 |

The `Argon2idParamsVerify`/`Argon2VerifierVerify` rows in the low milliseconds
and ~19 MiB/op are the Argon2id KDF doing its job — that cost is the point of
the algorithm, not a regression candidate in the usual sense. `ParsePHC` and
`ParseTrustedProxies` are startup-time parsers: `NewServer`
(`internal/server/http.go`) calls each once while building the server, not on
the request path. Everything else in this package is nanosecond-scale,
on-path per-request logic — the middleware and handler benchmarks
(`AuthMiddleware`, `ClientIP`, `AgentToken`, `RateLimiter`) run on every
request.

### `internal/mcp` — MCP request dispatch

| Benchmark | sec/op | B/op | allocs/op |
| --- | --- | --- | --- |
| `MCPHandler/initialize` | 6.992µ | 12.27Ki | 84 |
| `MCPHandler/tools_list` | 15.71µ | 21.02Ki | 201 |
| `MCPHandler/ping` | 4.947µ | 10.48Ki | 54 |
| `MCPHandler/parse_error` | 2.762µ | 7.121Ki | 31 |
| `MCPHandler/method_not_found` | 5.240µ | 10.56Ki | 56 |

### `internal/adapter` — image reference parsing

| Benchmark | sec/op | B/op | allocs/op |
| --- | --- | --- | --- |
| `ParseImageRef/bare_name` | 26.58n | 32 | 1 |
| `ParseImageRef/name_tag` | 30.88n | 32 | 1 |
| `ParseImageRef/registry_org_tag` | 36.28n | 32 | 1 |
| `ParseImageRef/ghcr` | 35.30n | 32 | 1 |
| `ParseImageRef/registry_port` | 39.21n | 32 | 1 |
| `ParseImageRef/digest` | 31.50n | 32 | 1 |

### `internal/adapter/drydock` — container label parsing

| Benchmark | sec/op | B/op | allocs/op |
| --- | --- | --- | --- |
| `ParseLabels/empty` | 11.13n | 0 | 0 |
| `ParseLabels/full` | 35.14n | 0 | 0 |

## What this does not measure

- **A competitor.** There is no head-to-head comparison against another
  Docker-proxy agent in this document, and that's deliberate rather than an
  oversight — a head-to-head competitor comparison is tracked on the project
  roadmap. sockguard's `BENCHMARKS.md` can run a
  same-class comparison against `wollomatic/socket-proxy` because both are
  stateless HTTP reverse proxies answering the same request/response shape.
  Portwing is a persistent agent that holds a WebSocket tunnel to a
  controller, negotiates capabilities, and streams Docker events and exec
  sessions — there's no comparably-shaped project to put through the same
  harness. Building one would mean picking apart which piece of that
  architecture to compare (the tunnel? the Docker proxy path alone?) rather
  than running an existing tool against an existing tool, which is a
  different, larger undertaking than a `BENCHMARKS.md` update.
- **End-to-end request latency.** These are function-level microbenchmarks.
  None of them go through the HTTP server, the WebSocket tunnel, or an actual
  Docker daemon. A `BenchmarkAuthMiddleware` number says nothing about total
  request latency once routing, TLS, and the Docker round trip are included.
- **Memory growth over time.** That's what `quality-soak-weekly.yml` and
  `benchmarks/cmd/loadgen` measure, not this lane. A benchmark that looks
  clean here can still leak under sustained load; the soak lane is where
  that would show up.
- **Concurrent throughput.** `-bench=.` runs each benchmark function
  single-threaded (`RateLimiter/is_rate_limited_parallel` is the one
  exception, using `b.RunParallel`). Nothing here characterizes how the
  server behaves under many simultaneous connections. The weekly
  `quality-soak-weekly.yml` lane does run `scripts/soak.sh`, which builds
  `benchmarks/cmd/loadgen` and drives sustained concurrent load — inventory,
  version, proxy, health, and SSE-churn scenarios at once — against a live
  portwing on a schedule, but it gates on RSS growth and a 429 budget, not on
  throughput. No lane gates on loadgen throughput numbers.
- **A real Docker daemon.** The parsers and middleware benchmarked here don't
  talk to Docker at all; they operate on values already extracted from
  requests or config. Docker-facing behavior is exercised by the integration
  tests (`go test -tags=integration`), not by this benchmark suite.

## Reproducing a run

```bash
go test -run='^$' -bench=. -benchmem -count=5 -timeout=20m ./...
```

To compare against a saved baseline the way CI does, install the same
`benchstat` version CI pins as `BENCHSTAT_VERSION` in
[`quality-bench-monthly.yml`](.github/workflows/quality-bench-monthly.yml)
(that env var is the source of truth; don't install `@latest`, which can
drift from what CI actually ran):

```bash
go install golang.org/x/perf/cmd/benchstat@"$BENCHSTAT_VERSION"
scripts/benchstat-gate.sh --baseline old-results.txt --current new-results.txt
```

`scripts/benchstat-gate.sh --help` documents the threshold and gated-units
flags; `scripts/benchstat-gate-script-test.sh` and
`scripts/benchstat-walk-baselines-test.sh` are its self-tests.
