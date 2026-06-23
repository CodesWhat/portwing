# Portwing Roadmap

> Portwing is **alpha** software (`v0.5.x`). This roadmap describes direction and
> priorities — not commitments. Items and ordering may change between releases.
> For the authoritative record of what has shipped, see the
> [CHANGELOG](CHANGELOG.md).

## Now — `v0.5.x` (hardening the alpha)

The current line prioritizes production-readiness of the existing feature set
over new surface area.

- **Security hardening** — credential handling, replay protection, per-request
  signing, and resource limits across the Docker proxy and the edge tunnel.
  Active items from the latest hardening pass: tightening pre-auth body limits,
  validating registry-auth and proxy query parameters before they reach the
  daemon, private-key file-permission parity, a consistent outbound TLS posture,
  and goroutine-lifecycle cleanup on shutdown.
- **Release & supply chain** — reproducible multi-arch builds, cosign-signed
  images, SBOMs, build provenance, and a CI-gated tag → release pipeline.
- **Test coverage & quality gates** — broaden unit, integration, and fuzz
  coverage across the auth, MCP, and adapter paths — including the wire-protocol
  envelope parser and the HTTP handlers — and bring the CI quality posture to
  parity with sockguard's:
  - **Three-tier fuzzing** — *shipped.* 60s smoke per PR (`ci.yml go-fuzz`),
    5m nightly (`quality-fuzz-nightly.yml`), and a 1h monthly deep pass
    (`quality-fuzz-monthly.yml`).
  - **Soak testing** — *shipped.* `quality-soak-weekly.yml` drives the agent
    (generic adapter, mock Docker upstream) under a sustained mix of inventory/
    version/proxy reads plus SSE subscriber connect/hold/disconnect churn, and
    fails if its resident set grows past a budget (64 MiB default) over a
    multi-hour soak — the long-lived-agent leak profile the unit/integration
    tiers don't catch. Harness: `benchmarks/cmd/{mockdocker,loadgen}` +
    `scripts/soak.sh`.
  - **Benchmark tracking** — *shipped.* Go benchmarks cover the per-request hot
    paths (auth middleware, Argon2id verify — cold derivation and warm SHA-256
    cache, client-IP extraction, rate limiter) and the parse paths (PHC,
    image-ref, Drydock labels, trusted-proxy CIDRs, MCP dispatch).
    `quality-bench-monthly.yml` reruns them with `-benchmem -count=5` on the
    first of each month and keeps the results as a 90-day artifact, so a ns/op
    or allocs/op regression is visible month over month.
- **Documentation** — keep `SPEC.md`, `README.md`, and the design docs in sync
  with the code as behavior settles. Near-term clean-up of the docs site:
  replace illustrative/placeholder CVE identifiers in the security model with
  described vulnerability classes, correct the security-model control numbering,
  document the nonce-cache replay-window behavior in `SECURITY.md`, and sync the
  docs site and marketing copy to the current release.

## Next — hardening edge mode

- **End-to-end edge mode** — *shipped.* Drydock 1.5 added the matching
  `/api/portwing/ws` controller endpoint (Ed25519-only), so the agent can dial
  out and manage NAT'd / firewalled hosts with no inbound port. Both Drydock 1.5
  and the paired Portwing release are pre-release.
- **Edge tunnel robustness** — *shipped.* Ordered exec I/O (input that races
  ahead of the Docker exec coming up is buffered in arrival order and replayed,
  not dropped), outbound backpressure (a single writer goroutine fronts a
  bounded send queue with a per-frame write deadline and evicts a controller
  that can't keep up, so one slow consumer can't head-of-line-block every
  session or stall the read pump), and a dedicated unit test harness for the
  tunnel (auth hello, request fan-out, exec sessions) built on a consumer-side
  Docker seam.
- **Reproducible base images** — *shipped.* Both `Dockerfile` and
  `Dockerfile.release` pin every base image by digest (`wolfi-base`, `alpine`,
  `golang`), and Dependabot tracks the `docker` ecosystem weekly for updates.

## Later — toward `v1.0`

- **Stable API & wire formats** — freeze the HTTP API, environment-variable
  surface, and the agent ↔ controller protocol under semantic-versioning
  guarantees.
- **Operational ergonomics** — richer health/metrics, structured audit export,
  and ready-to-run deployment examples for common topologies.

## Non-goals

- **Container orchestration** — Portwing controls a single host's Docker daemon.
  It is not a scheduler and not a Swarm / Kubernetes replacement.
- **A bundled UI** — Portwing is an agent; the control plane (e.g. Drydock) owns
  the user-facing interface.

---

*Detailed internal planning is tracked separately and intentionally not
published here.*
