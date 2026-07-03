# Coverage Standard

## The Standard

Portwing targets a **documented ~96% production coverage floor**, not 100%.

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

The **enforced floor is 96%** (set in `.github/workflows/ci.yml` as
`COVERAGE_MIN`). Achieved total as of 2026-06: **97.5%**. The floor is set
~1.5% below the achieved total so CI never fails on coverage noise — the org
standard for the Go repos (drydock stays at 100% because TypeScript supports
line-level `istanbul-ignore`; Go has no equivalent).

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

`buildAckPayload` (line ~175), `BroadcastWatcherSnapshot` (line ~204),
`BroadcastContainerAdded` (line ~214), `BroadcastContainerUpdated` (line ~228),
`BroadcastContainerRemoved` (line ~242): all call `json.Marshal` on structs or
`map[string]any` values whose fields are `string`, `int`, `[]byte`, and other
JSON-safe primitives. The Go JSON encoder never returns an error for these types.
The `if err != nil` guard is a defensive API pattern, not a reachable path.

### `internal/adapter/drydock/adapter.go` — `json.Marshal` error in `sendContainerEvent` (line ~308)

```go
data, err := json.Marshal(container)
if err != nil { ... }
```

`adapter.Container` marshals cleanly (all string/int/bool fields). The error
branch is a defensive guard; unreachable in practice.

### `internal/adapter/drydock/adapter.go` — `sendComponentSync` (line ~281)

`sendComponentSync` is called only from the live WebSocket message handler. The
uncovered lines are inside a branch where `json.Marshal` would fail on
`protocol.DDComponentSyncMessage`, which contains only string fields. Same
defensive-guard category as above.

### `internal/auth/keygen.go` — `crypto/rand` failure branches

`MarshalPrivateKeyPEM` (line 50): `x509.MarshalPKCS8PrivateKey` error on a
valid in-memory Ed25519 key is unreachable.

`GenerateKeyPair` (line 63): `ed25519.GenerateKey(rand.Reader)` can only fail if
`crypto/rand` returns an error — a kernel-level failure not reachable in tests.

`NewNonce` (line 95): same `rand.Read` failure category.

### `internal/auth/keys.go` — `checkFilePermissions` GOOS=windows branch (line 190)

```go
if runtime.GOOS == "windows" {
    return nil
}
```

This branch is compile-time dead on linux/darwin. It is the correct defensive
pattern for cross-platform code and is not testable without `GOOS=windows`.

### `internal/auth/enroll.go` — `appendKeyLine` close-error branch (line ~153)

The deferred `f.Close()` error surfacing path is structurally unreachable: the
kernel returns a close error only when a dirty page cannot be flushed (e.g. on
NFS). Not reproducible in unit tests without low-level filesystem mocking.

### `internal/edge/client.go` — `sendMetrics` error branch (line ~727)

```go
m, err := c.collector.Collect()
if err != nil { ... }
```

`sendMetrics` is called on a live WebSocket connection's timer goroutine.
Covering the error path requires injecting a failing `MetricsCollector`, which
is only wired up in the integration-test binary. The branch is 1 statement.

### `internal/edge/client.go` — `sendPump` `SetWriteDeadline` error (line ~798)

```go
if err := conn.SetWriteDeadline(...); err != nil {
    c.failConn("set write deadline failed")
    return
}
```

`SetWriteDeadline` on a `*websocket.Conn` only fails if the underlying file
descriptor is already closed. Triggering this without a data race requires
OS-level fd manipulation. Not testable cleanly in unit tests.

### `internal/generic/events.go` — `ServeHTTP` heartbeat branch (line ~97)

```go
case <-heartbeat.C:
```

The heartbeat ticker fires every 30 seconds. Covering this branch in a unit test
requires waiting 30 s or injecting a mock ticker, neither of which is worth the
fragility trade-off. The branch is 3 statements.

### `internal/mcp/mcp.go` — `writeToolResult` json.Marshal error (line ~517)

`writeToolResult` marshals `any` data — in practice always a well-formed struct
or map. The `err != nil` guard is a defensive pattern; not triggered by any
production call site.

### `internal/server/middleware.go` — `cleanup` 5-minute ticker (line ~89)

```go
case <-ticker.C:
```

`cleanup` is a `RateLimiter` maintenance goroutine that sleeps for 5 minutes
between sweeps. Covering the ticker branch in a unit test requires a 5-minute
sleep or a mock ticker injected into unexported state. Not worth the test
fragility for 3 statements.

## How Coverage Is Measured

The CI coverage job (`.github/workflows/ci.yml`, `test` job) runs:

```sh
go test -race -covermode=atomic -coverprofile=coverage.out \
    $(go list ./internal/... ./cmd/... | grep -v '/internal/banner/gen')
```

`internal/banner/gen` is excluded because it contains only generated constants
with no testable logic. The `benchmarks/` subtree is likewise excluded — it
contains load-test harnesses, not production code.

The floor check extracts the `total:` line from `go tool cover -func` and
compares it against `COVERAGE_MIN`.
