# Coverage Standard

## The Standard

Portwing targets a **documented ~97% production coverage floor**, not 100%.

Go cannot cleanly reach 100% without production-code distortion. Certain
patterns produce statements that are structurally unreachable or require
impractical test infrastructure:

- `os.Exit` in `main()` — the test harness invokes `run()` directly; covering
  the `os.Exit` line requires exec-subprocess tricks that obscure test intent
- `json.Marshal` error branches on primitive-only structs — the Go JSON encoder
  never errors on `string`, `int`, `bool`, or `map[string]any` with those value
  types; the error return exists as a defensive API contract, not a reachable path
- `crypto/rand.Read` failure branches — cryptographically random bytes failing is
  not testable without kernel-level intervention
- `conn.SetWriteDeadline` error branches on live WebSocket connections — requires
  an OS-level file-descriptor manipulation that would destabilize the test suite
- Long-period cleanup tickers (5-minute `time.Ticker`) — waiting 5 minutes per
  test run is impractical; the ticker path is covered by code inspection
- Compile-time `GOOS=windows` branches — dead code on linux/darwin; cannot be
  compiled or executed in CI

The **enforced floor defaults to 97%** (`COVERAGE_MIN` in
`scripts/ci/go-test.sh`; the env override exists for local experiments, CI
does not set it, so 97 is what every CI run enforces). Achieved total as of
2026-09-02: **97.4%** (run `33684784256`, job `100429523414`, commit
`7f61a77`), with 97.3% on the four commits before it. Keeping a floor below
the achieved total is the org standard for the Go repos: drydock stays at
100% because TypeScript supports line-level `istanbul-ignore`, and Go has no
equivalent.

The floor sat at 96 while the achieved total climbed from 96.4% to 97.4%,
which turned it into 1.4 points of slack a regression could have hidden in.
97 restores about 0.4 points of headroom, roughly 25 statements, which is the
room a change that lands slightly ahead of its tests needs. It is a whole
number rather than the measured total because the floor is here to catch a
real regression, not to pin a figure that moves a tenth of a point whenever a
test file lands. New production code should still land with tests, or the
floor becomes the first thing a refactor trips over.

## Residual Uncovered Blocks

The following blocks are documented as structurally unreachable. They are **not**
coverage gaps that need fixing — do not add contorted tests or refactor production
code to cover them.

### `cmd/portwing/main.go:33`

```go
os.Exit(run(os.Args, os.Stdin, os.Stdout, os.Stderr))
```

`os.Exit` is unreachable from tests. All test coverage goes through `run()`
directly, which is 94.9% covered. Covering `main()` itself would require an
exec-subprocess round-trip that adds no meaningful signal.

### `internal/adapter/drydock/sse.go` — `json.Marshal` error branches

`buildAckPayload` (line ~151), `BroadcastWatcherSnapshot` (line ~220),
`BroadcastContainerAdded` (line ~230), `BroadcastContainerUpdated` (line ~244),
`BroadcastContainerRemoved` (line ~258): all call `json.Marshal` on structs or
`map[string]any` values whose fields are `string`, `int`, `[]byte`, and other
JSON-safe primitives. The Go JSON encoder never returns an error for these types.
The `if err != nil` guard is a defensive API pattern, not a reachable path.

### `internal/adapter/drydock/adapter.go` — `json.Marshal` error in `sendContainerEvent` (line ~665)

```go
data, err := json.Marshal(toDrydockContainer(container))
if err != nil { ... }
```

`adapter.Container` marshals cleanly (all string/int/bool fields). The error
branch is a defensive guard; unreachable in practice.

### `internal/auth/keygen.go` — `crypto/rand` failure branches

`MarshalPrivateKeyPEM` (line 51): `x509.MarshalPKCS8PrivateKey` error on a
valid in-memory Ed25519 key is unreachable.

`GenerateKeyPair` (line 64): `ed25519.GenerateKey(rand.Reader)` can only fail if
`crypto/rand` returns an error — a kernel-level failure not reachable in tests.

`NewNonce` (line 96): same `rand.Read` failure category.

### `internal/auth/keys.go` — `validateCredentialPermissions` GOOS=windows branch (line 199)

```go
if runtime.GOOS == "windows" {
    return nil
}
```

This branch is compile-time dead on linux/darwin. It is the correct defensive
pattern for cross-platform code and is not testable without `GOOS=windows`.

### `internal/auth/enroll.go` — `appendKeyLine` close-error branch (line ~205)

The deferred `f.Close()` error surfacing path is structurally unreachable: the
kernel returns a close error only when a dirty page cannot be flushed (e.g. on
NFS). Not reproducible in unit tests without low-level filesystem mocking.

### `internal/edge/client.go` — `sendMetrics` error branch (line ~1373)

```go
m, err := c.collector.Collect()
if err != nil { ... }
```

`sendMetrics` is called on a live WebSocket connection's timer goroutine.
Covering the error path requires injecting a failing `MetricsCollector`, which
is only wired up in the integration-test binary. The branch is 1 statement.

### `internal/edge/client.go` — `sendPump` `SetWriteDeadline` error (line ~1539)

```go
if err := conn.SetWriteDeadline(...); err != nil {
    c.failConn("set write deadline failed")
    return
}
```

`SetWriteDeadline` on a `*websocket.Conn` only fails if the underlying file
descriptor is already closed. Triggering this without a data race requires
OS-level fd manipulation. Not testable cleanly in unit tests.

### `internal/generic/events.go` — `ServeHTTP` heartbeat branch (line ~112)

```go
case <-heartbeat.C:
```

The heartbeat ticker fires every 30 seconds. Covering this branch in a unit test
requires waiting 30 s or injecting a mock ticker, neither of which is worth the
fragility trade-off. The branch is 3 statements.

### `internal/mcp/mcp.go` — `writeToolResult` json.Marshal error (line ~637)

`writeToolResult` marshals `any` data — in practice always a well-formed struct
or map. The `err != nil` guard is a defensive pattern; not triggered by any
production call site.

### `internal/server/middleware.go` — `cleanup` 5-minute ticker (line ~122)

```go
case <-ticker.C:
```

`cleanup` is a `RateLimiter` maintenance goroutine that sleeps for 5 minutes
between sweeps. Covering the ticker branch in a unit test requires a 5-minute
sleep or a mock ticker injected into unexported state. Not worth the test
fragility for 3 statements.

## How Coverage Is Measured

The reusable `Go CI / Build & Test` job calls:

```sh
./scripts/ci/go-test.sh
```

`internal/banner/gen` is excluded because it contains only generated constants
with no testable logic. The `benchmarks/` subtree is likewise excluded — it
contains load-test harnesses, not production code.

The floor check extracts the `total:` line from `go tool cover -func` and
compares it against `COVERAGE_MIN`.
