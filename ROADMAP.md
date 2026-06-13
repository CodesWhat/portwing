# Lookout Roadmap

> Lookout is **alpha** software (`v0.2.x`). This roadmap describes direction and
> priorities — not commitments. Items and ordering may change between releases.
> For the authoritative record of what has shipped, see the
> [CHANGELOG](CHANGELOG.md).

## Now — `v0.2.x` (hardening the alpha)

The current line prioritizes production-readiness of the existing feature set
over new surface area.

- **Security hardening** — credential handling, replay protection, per-request
  signing, and resource limits across the Docker proxy and the edge tunnel.
- **Release & supply chain** — reproducible multi-arch builds, cosign-signed
  images, SBOMs, build provenance, and a CI-gated tag → release pipeline.
- **Test coverage** — broaden unit, integration, and fuzz coverage, closing
  gaps in the auth, MCP, and adapter paths.
- **Documentation** — keep `SPEC.md`, `README.md`, and the design docs in sync
  with the code as behavior settles.

## Next — hardening edge mode

- **End-to-end edge mode** — *shipped.* Drydock 1.5 added the matching
  `/api/lookout/ws` controller endpoint (Ed25519-only), so the agent can dial
  out and manage NAT'd / firewalled hosts with no inbound port. Both Drydock 1.5
  and the paired Lookout release are pre-release.
- **Edge tunnel robustness** — ordered exec I/O, backpressure under load, and a
  dedicated test harness for the tunnel (auth hello, request fan-out, exec
  sessions). Lands in v0.2.2.
- **Reproducible base images** — pin runtime base images by digest with
  automated update tracking.

## Later — toward `v1.0`

- **Stable API & wire formats** — freeze the HTTP API, environment-variable
  surface, and the agent ↔ controller protocol under semantic-versioning
  guarantees.
- **Operational ergonomics** — richer health/metrics, structured audit export,
  and ready-to-run deployment examples for common topologies.

## Non-goals

- **Container orchestration** — Lookout controls a single host's Docker daemon.
  It is not a scheduler and not a Swarm / Kubernetes replacement.
- **A bundled UI** — Lookout is an agent; the control plane (e.g. Drydock) owns
  the user-facing interface.

---

*Detailed internal planning is tracked separately and intentionally not
published here.*
