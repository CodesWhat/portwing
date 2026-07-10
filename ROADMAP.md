# Portwing Roadmap

> Portwing is pre-`v1.0.0` software (currently `v0.6.0`). This roadmap describes
> direction and priorities — not commitments. Items and ordering may change
> between releases. For the authoritative record of what has shipped, see the
> [CHANGELOG](CHANGELOG.md).

## Shipped — edge mode, hardening, and quality gates

The security-hardening, release/supply-chain, test-coverage, and edge-tunnel
work previously tracked here as "in progress" landed across v0.5.0, v0.5.1,
and v0.6.0 — see [CHANGELOG.md](CHANGELOG.md) for the itemized history. In
particular:

- **End-to-end edge mode.** Drydock 1.5 (GA, 2026-06-22) added the matching
  `/api/portwing/ws` controller endpoint (Ed25519-only), so the agent can dial
  out and manage NAT'd / firewalled hosts with no inbound port. Drydock 1.5 is
  released; Portwing itself remains pre-`v1.0.0`, and edge mode is still early
  access — see the edge-mode graduation gate in "Toward `v1.0`" below.
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

- **Edge-mode graduation out of early access.** Drydock currently gates
  `/api/portwing/ws` behind the `DD_EXPERIMENTAL_PORTWING` flag. Flipping that
  flag on by default requires cross-repo load/soak evidence (not just
  Portwing's own single-agent `quality-soak-weekly.yml`) covering reconnect
  storms, sustained exec sessions, and controller-side backpressure against a
  realistic multi-agent fleet.
- **A drafted wire/API stability policy.** Semantic-versioning guarantees for
  the HTTP API surface, the environment-variable surface, and the
  `DrydockCompat` wire contract (`internal/protocol/messages.go`), so
  downstream integrators know what counts as breaking once `v1.0.0` ships.
  Not yet drafted.
- **Package-manager distribution.** A Homebrew tap and `apt`/`rpm` packages
  built through the existing GoReleaser pipeline, so installation doesn't
  require a container image or pulling the raw binary off GitHub Releases.
- **Remaining edge wire-protocol gaps.** The `dd:container_log_*` /
  `dd:container_delete_*` pairs now echo a `requestId` for concurrent-request
  correlation, honor `timestamps`, and serve `follow` as a bounded live window
  (with the log payload de-multiplexed to plain text). The open piece is a
  *true* continuous live tail under the `dd:` namespace: today that requires the
  generic `request`/`stream`/`stream_end` path against
  `GET /containers/{id}/logs?follow=1`. Giving `dd:container_log_request` its own
  streaming variant is a wire-shape decision to pin jointly with the drydock
  controller before either side builds it.
- **Operational ergonomics.** Richer health/metrics, structured audit export,
  and ready-to-run deployment examples for common topologies.

## Non-goals

- **Container orchestration** — Portwing controls a single host's Docker daemon.
  It is not a scheduler and not a Swarm / Kubernetes replacement.
- **A bundled UI** — Portwing is an agent; the control plane (e.g. Drydock) owns
  the user-facing interface.

---

*Detailed internal planning is tracked separately and intentionally not
published here.*
