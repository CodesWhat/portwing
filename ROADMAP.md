# Lookout Roadmap

> Lookout is **alpha** software (`v0.2.x`). This roadmap describes direction and
> priorities — not commitments. Items and ordering may change between releases.
> For the authoritative record of what has shipped, see the
> [CHANGELOG](CHANGELOG.md).

## The ecosystem

Lookout is the **data-plane agent** in a three-tool remote-Docker stack:

- **[sockguard](https://github.com/CodesWhat/sockguard)** — a Docker socket
  firewall. It default-denies the Docker Engine API through a YAML policy and
  exposes a filtered socket, so the agent only ever sees the calls it is allowed
  to make.
- **Lookout** (this project) — runs on each Docker host: proxies the (optionally
  sockguard-filtered) Docker API, tracks container inventory, and serves Prometheus
  metrics, a read-only MCP endpoint, and a control-plane channel.
- **[drydock](https://github.com/CodesWhat/drydock)** — the central control plane
  and UI that manages a fleet of agents.

Lookout reaches the control plane two ways: **standard mode** (drydock dials in to
lookout over HTTP + SSE) and **edge mode** (lookout dials *out* over a WebSocket, so
NAT'd or firewalled hosts need no inbound port). Much of the roadmap below is the
work of making that ecosystem seamless end to end.

## Now — `v0.2.x` (hardening the alpha)

The current line prioritizes production-readiness of the existing feature set over
new surface area.

- **Ecosystem integration (today)** — standard-mode integration with drydock is
  validated end to end: container sync, live events, watchers, triggers, and log
  streaming, over shared-secret or Ed25519 auth. Running behind sockguard works
  today — lookout strips its own auth headers before forwarding to the Docker
  socket, and ships a working `examples/docker-compose.with-sockguard.yml` baseline.
- **Security hardening** — credential handling, replay protection, per-request
  signing, and resource limits across the Docker proxy and the edge tunnel.
- **Release & supply chain** — reproducible multi-arch builds, cosign-signed
  images, SBOMs, build provenance, and a CI-gated tag → release pipeline.
- **Test coverage** — broaden unit, integration, and fuzz coverage, closing gaps
  in the auth, MCP, and adapter paths.
- **Documentation** — keep `SPEC.md`, `README.md`, and the design docs in sync
  with the code as behavior settles.

## Next — completing edge mode & ecosystem onboarding

- **Edge mode (NAT / firewall traversal)** — lookout's outbound WebSocket tunnel is
  fully implemented on the agent side; the matching endpoint on the drydock
  controller is the remaining piece. Once both ends are live, lookout can manage any
  Docker host with outbound HTTPS — no inbound port, VPN, or reverse proxy required.
- **First-class sockguard preset for lookout** — a bundled sockguard policy preset
  covering exactly the Docker API surface lookout needs, with an opt-in exec variant
  for interactive sessions, plus a paired compose example.
- **Exec through sockguard** — a documented, ready-to-use policy for exec sessions
  through a sockguard-filtered socket, in both standard and edge modes.
- **Agent info completeness** — log level, poll interval, and host memory reported
  correctly for every connected agent in the drydock UI.
- **Edge tunnel test harness** — coverage for the auth hello/welcome handshake,
  container sync, exec-session ordering, and backpressure under load.

## Later — toward `v1.0`

- **Stable API & wire formats** — freeze the HTTP API, the environment-variable
  surface, and the agent ↔ controller protocol under semantic-versioning
  guarantees, with the edge framing (`lookout/1.0`) stabilized and published as a
  versioned spec.
- **Drydock self-update through sockguard** — a sockguard preset variant that
  allowlists the finalize-exec paths drydock's self-update lifecycle needs, with the
  policy tradeoffs documented.
- **Operational ergonomics** — richer health/metrics, structured audit export, and
  ready-to-run deployment examples for common topologies (single host, multi-agent,
  NAT-traversed fleet).
- **Reproducible base images** — pin runtime base images by digest with automated
  update tracking.

## Non-goals

- **Container orchestration** — Lookout controls a single host's Docker daemon.
  It is not a scheduler and not a Swarm / Kubernetes replacement.
- **A bundled UI** — Lookout is an agent; the control plane (e.g. Drydock) owns
  the user-facing interface.

---

*Detailed internal planning is tracked separately and intentionally not
published here.*
