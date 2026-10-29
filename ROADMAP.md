# Portwing Roadmap

> Portwing is stable software (currently `v1.0.0`). This roadmap describes
> direction and priorities — not commitments. Items and ordering may change
> between releases. For the authoritative record of what has shipped, see the
> [CHANGELOG](CHANGELOG.md).

This direction covers work through August 2027.
Items without a release assignment remain demand-driven rather than promised.

## Shipped — edge mode, hardening, and quality gates

The security-hardening, release/supply-chain, test-coverage, and edge-tunnel
work previously tracked here as "in progress" landed across v0.5.0 through
v0.9.0 — see [CHANGELOG.md](CHANGELOG.md) for the itemized history. In
particular:

- **End-to-end edge mode.** Drydock 1.5 first shipped the matching
  `/api/portwing/ws` controller endpoint (Ed25519-only); Drydock 1.6 enables
  the stable `portwing/1.0` endpoint by default. Portwing can dial out and
  manage NAT'd / firewalled hosts with no inbound control port published.
- **Edge tunnel robustness.** Ordered exec I/O, a single-writer outbound
  backpressure path with per-frame write deadlines and slow-consumer eviction,
  and a dedicated wire-contract test harness (`internal/edge/wire_contract_test.go`).
- **Three-tier fuzzing, soak testing, and benchmark tracking** in CI, at
  parity with sockguard's quality posture — `quality-fuzz-nightly.yml` /
  `quality-fuzz-monthly.yml`, `quality-soak-weekly.yml`
  (`benchmarks/cmd/{mockdocker,loadgen}` + `scripts/soak.sh`), and
  `quality-bench-monthly.yml`.
- **Reproducible base images.** `Dockerfile`, `Dockerfile.armv7`,
  `Dockerfile.dev`, `Dockerfile.release`, and `.clusterfuzzlite/Dockerfile`
  pin every base image by digest, and Renovate keeps the digests current.
- **Controller-owned watcher/update execution.** Portwing marks the Docker
  watcher as `transport=docker-api`, `execution=controller`, and
  `events=portwing`; Drydock runs its native watcher and update trigger through
  Portwing over Standard HTTP or Edge correlated requests. The complete v0.9
  feature path requires Drydock `v1.6.0-rc.11+` while the stable wire contract
  remains `DrydockCompat` 1.4.0.
- **Shared Edge audit export.** The cursor-based NDJSON exporter is available
  on Edge Mode's private operations listener, with deployment examples that
  isolate its intentionally unauthenticated trust boundary.

## Shipped in `v1.0`

`v1.0.0` shipped once these concrete, verifiable items were met, rather than on
a calendar date:

- **Completed 2026-08-29 (re-verified 2026-10-26): Competitive review gate.**
  The market audit is published at
  [portwing.codeswhat.com/docs/competitive-landscape](https://portwing.codeswhat.com/docs/competitive-landscape). Comparison claims must
  stay tied to primary sources. The audit found no missing container lifecycle,
  streaming, Compose, observability, or authentication primitive that requires
  new Portwing code for v1.0. The release review refreshed primary-source
  evidence for the current peer versions, with edition and artifact limits
  recorded on the canonical page. Recheck claims that change.
- **Completed 2026-08-29: Tagged acceptance matrix.** Published Portwing's
  then-current release, Drydock v1.6.0, and Sockguard v2.0.0 artifacts passed the full
  Standard/Edge matrix in the
  [hosted conformance run](https://github.com/CodesWhat/sockguard/actions/runs/33283283870).
  A real Drydock controller exercised enrollment, overlapping-key rotation,
  revocation, two SIGHUP reloads, and clock-skew rejection and recovery. The
  legacy-floor row passed alongside the current Standard and Edge rows.
- **Completed for v1.0.0-rc.1: Candidate artifact verification.** The exact
  published candidate passed Standard, Edge and legacy-floor rows with Drydock
  [v1.6.0](https://github.com/CodesWhat/sockguard/actions/runs/38836428734) and
  [v1.6.1](https://github.com/CodesWhat/sockguard/actions/runs/38837070094), both
  with Sockguard v2.2.10. These runs did not verify later development work.
- **Completed for rc.2: Final candidate evidence.** The rc.2
  [release run](https://github.com/CodesWhat/portwing/actions/runs/40236426154)
  passed, including Verify Published and the cosign attestation, the
  per-platform Grype scans and the native package checks. The
  [sockguard tri-tool conformance run](https://github.com/CodesWhat/sockguard/actions/runs/40266007939)
  passed with Drydock 1.7.0 and Sockguard 2.2.11. Managed rotation was run on
  the NAS with Drydock 1.8.0-rc.1 against image digest
  `sha256:4ef115970be2ceacfb58cdc603745da509b7637a62501507a40f300633048c3e`,
  and the published image passed a smoke run on the mainframe. The v1.0.0
  artifacts are verified after publication.
- **Completed for v0.8 — Edge-mode graduation.** Drydock 1.6 enables
  `/api/portwing/ws` by default, with `DD_EXPERIMENTAL_PORTWING=false` retained
  as an emergency disable. Drydock's `quality-portwing-fleet-soak.yml` runs the
  real cross-repo multi-agent soak: reconnect storms, sustained exec sessions,
  continuous logs, and controller-side backpressure with machine-checked RSS
  and heap budgets. Portwing's own weekly soak separately covers the
  Standard/generic HTTP path and SSE churn under an RSS-growth budget.
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
- **Completed for v0.9 — Controller-owned update integration.** Additive
  watcher transport/ownership/event markers, component-before-inventory Edge
  ordering, no remote-trigger advertisement, and documented Standard/Edge
  proxy paths are coordinated with Drydock `v1.6.0-rc.11+`.
- **Completed for v0.9 — Edge audit export hardening.** Standard and Edge
  modes share one exporter implementation; public docs and Kubernetes examples
  explicitly isolate Edge Mode's unauthenticated operations listener.
- **Completed for the initial v1.0 candidate: Versioned docs archive.** The `v0.9` docs
  were frozen from the final v0.9 release tag and are served at `/docs/v0.9/...`,
  with their source tree verified by the archive contract. The live
  `docs/content/docs/current/` tree serves at the unversioned `/docs/...` URLs
  and now includes the rotation feature. Preserve frozen archives when
  preparing later release lines. The v0.9 line is no longer supported as of
  v1.0.0.
- **SLSA Build L2 is the ceiling for now.** Provenance attestation (see
  [RELEASING.md](RELEASING.md)) stays at Build L2 because GoReleaser, cosign,
  and attest all run inline inside `release.yml`. Build L3 needs an isolated,
  org-shared reusable release workflow in `CodesWhat/.github`; none exists yet
  for any CodesWhat repo. The trigger is that workflow landing — portwing's
  release job moves onto it then, not before.

**Shipped in v1.0.0, first available in rc.2: controller-assisted
two-key rotation.** Opt-in durable managed credentials, Standard replacement
proof and Edge signed-hello handoff require Drydock `v1.8.0-rc.1+` on the v1.8
line. Operator docs, API and release metadata record the supporting tags together
in [COMPATIBILITY.md](COMPATIBILITY.md#controller-assisted-rotation).

**For later releases:** rerun conformance against the exact new artifacts and
record the successful hosted runs. Earlier evidence does not carry forward. Keep
release/support metadata aligned; the existing wire level does not establish
additive feature support. Follow
[RELEASING.md](RELEASING.md#release-documentation-and-compatibility).
Legacy query-free HTTP signatures have no scheduled removal.

Post-v1 candidates are intentionally demand-driven: controller-managed
Portwing upgrade/rollback waves, optional client-certificate authentication,
and polling/intermittent edge transport.
None should expand the base socket policy or add an unaudited
self-update path merely for feature-table parity.

## Non-goals

- **Container orchestration** — Portwing controls a single host's Docker daemon.
  It is not a scheduler and not a Swarm / Kubernetes replacement.
- **A bundled UI** — Portwing is an agent; the control plane (e.g. Drydock) owns
  the user-facing interface.
- **Host administration** — arbitrary host shells, host-file browsing, volume
  backup, and secrets-vault behavior do not belong in the agent.
- **Fleet product features** — RBAC, GitOps, schedules, alert routing,
  vulnerability workflows, and agent rollout policy belong in Drydock.

---

*Detailed internal planning is tracked separately and intentionally not
published here.*
