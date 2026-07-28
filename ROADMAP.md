# Portwing Roadmap

> Portwing is pre-`v1.0.0` software (currently `v0.8.0`). This roadmap describes
> direction and priorities — not commitments. Items and ordering may change
> between releases. For the authoritative record of what has shipped, see the
> [CHANGELOG](CHANGELOG.md).

## Shipped — edge mode, hardening, and quality gates

The security-hardening, release/supply-chain, test-coverage, and edge-tunnel
work previously tracked here as "in progress" landed across v0.5.0 through
v0.8.0 — see [CHANGELOG.md](CHANGELOG.md) for the itemized history. In
particular:

- **End-to-end edge mode.** Drydock 1.5 first shipped the matching
  `/api/portwing/ws` controller endpoint (Ed25519-only); Drydock 1.6 enables
  the stable `portwing/1.0` endpoint by default. Portwing can dial out and
  manage NAT'd / firewalled hosts with no inbound port.
- **Edge tunnel robustness.** Ordered exec I/O, a single-writer outbound
  backpressure path with per-frame write deadlines and slow-consumer eviction,
  and a dedicated wire-contract test harness (`internal/edge/wire_contract_test.go`).
- **Three-tier fuzzing, soak testing, and benchmark tracking** in CI, at
  parity with sockguard's quality posture — `quality-fuzz-nightly.yml` /
  `quality-fuzz-monthly.yml`, `quality-soak-weekly.yml`
  (`benchmarks/cmd/{mockdocker,loadgen}` + `scripts/soak.sh`), and
  `quality-bench-monthly.yml`.
- **Reproducible base images.** `Dockerfile` and `Dockerfile.release` pin
  every base image by digest, with Dependabot tracking the `docker` ecosystem.

## Toward `v1.0`

The path to `v1.0.0` is gated on concrete, verifiable items rather than a
calendar date:

- **Completed for v0.8 — Edge-mode graduation.** Drydock 1.6 enables
  `/api/portwing/ws` by default, with `DD_EXPERIMENTAL_PORTWING=false` retained
  as an emergency disable. A real cross-repo multi-agent soak covers reconnect
  storms, sustained exec sessions, continuous logs, and controller-side
  backpressure with machine-checked RSS and heap budgets.
- **Completed for v0.8 — Published wire/API stability policy.** Semantic-versioning guarantees for
  the HTTP API surface, the environment-variable surface, and the
  MCP tool surface and `DrydockCompat` wire contract are defined in
  [STABILITY.md](STABILITY.md).
- **Completed for v0.8 — Package-manager distribution.** A Homebrew tap and
  signed/checksummed `deb`/`rpm` packages
  built through the existing GoReleaser pipeline, so installation doesn't
  require a container image or pulling the raw binary off GitHub Releases.
- **Completed for v0.8 — Continuous edge logs.** The `dd:container_log_*`
  namespace now supports correlated stdout/stderr chunks, explicit end/error
  frames, viewer cancellation, bounded agent/controller queues, and a legacy
  one-shot fallback for mixed-version fleets.
- **Completed for v0.8 — Operational ergonomics.** Mode-aware liveness and
  readiness, shared standard/edge metrics, cursor-based NDJSON audit export,
  and ready-to-run Compose/Kubernetes observability examples.

## Non-goals

- **Container orchestration** — Portwing controls a single host's Docker daemon.
  It is not a scheduler and not a Swarm / Kubernetes replacement.
- **A bundled UI** — Portwing is an agent; the control plane (e.g. Drydock) owns
  the user-facing interface.

---

*Detailed internal planning is tracked separately and intentionally not
published here.*
