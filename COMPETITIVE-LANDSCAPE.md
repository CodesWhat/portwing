# Competitive Landscape

> Research snapshot: 2026-07-28. This document compares published behavior,
> not private roadmaps. Re-check the linked primary sources before using it for
> release or purchasing decisions.

Portwing is a remote Docker access agent, not a fleet-management UI. The useful
comparison is therefore two-layered:

1. compare Portwing with the agent installed on each Docker host; and
2. compare Drydock with the controller features that sit above those agents.

Treating every controller feature as a Portwing requirement would expand the
agent's privilege and attack surface without improving its core job.

## Products reviewed

### Direct agent peers

| Product | Release reviewed | Why it is in scope |
| --- | --- | --- |
| Portainer Agent / Edge Agent | Portainer [2.39.5](https://github.com/portainer/portainer/releases/tag/2.39.5) | Mature standard and outbound-edge agents with Docker API proxying, Swarm support, async edge operation, and fleet lifecycle features. |
| Komodo Periphery | [v2.2.0](https://github.com/moghtech/komodo/releases/tag/v2.2.0) | Remote host agent for containers, Compose, builds, host terminals, automation, and Swarm. Komodo v2 added outbound Periphery and public-key authentication. |
| Arcane Agent | [v2.5.0](https://github.com/getarcaneapp/arcane/releases/tag/v2.5.0) | Direct and outbound-edge Docker agent with continuous or polling transport, optional mTLS, Swarm, and a broad controller surface. |
| Hawser | [v0.2.46](https://github.com/Finsys/hawser/releases/tag/v0.2.46) | The closest scope match: a small Go Docker API proxy for Dockhand with standard and outbound WebSocket modes. |

### Adjacent products and baselines

| Product/category | Classification |
| --- | --- |
| Docker SSH or TLS | The native remote-access baseline. Docker warns that an exposed daemon can grant host-level control and documents SSH or TLS protection. |
| Tecnativa, LinuxServer, and Wollomatic socket proxies | Local Docker-socket authorization layers. Sockguard competes here; these are complementary to a remote agent. |
| Distr Docker Agent | A BYOC/on-prem application delivery agent with self-updates, Compose/Swarm reconciliation, logs, and metrics. It is adjacent because it deploys applications rather than exposing a general Docker control surface. |
| Docker Surgeon | A small inbound REST agent for monitoring and restarting unhealthy containers. It is narrower than Portwing but belongs on the watch list. |
| Beszel and Diun | Monitoring/notification agents, not remote Docker control agents. |
| Watchtower | Local container updater, not a remote access agent; its upstream repository is archived. |

## Agent capability matrix

“Not documented” means the reviewed primary documentation did not establish
the capability. It is intentionally different from a definitive “no.”

| Capability | Portwing v0.8.1 | Portainer Agent | Komodo Periphery | Arcane Agent | Hawser |
| --- | --- | --- | --- | --- | --- |
| Inbound mode | HTTP/S with Docker API proxy and higher-level endpoints | Classic Agent | Core-to-Periphery supported | Direct mode on agent port | HTTP/S Docker API proxy |
| Outbound / NAT mode | Persistent WebSocket to Drydock; no inbound port | Edge Agent reverse tunnel | Bidirectional WebSocket; Periphery can dial Core | Edge mode over gRPC or WebSocket | Persistent WebSocket to Dockhand |
| Polling / intermittently connected mode | No | Async Edge | Not documented | Yes (`EDGE_TRANSPORT=poll`) | No |
| Transparent Docker API | Yes | Yes | No; controller-specific command/resource API | No; controller-specific API | Yes |
| Container lifecycle, logs, exec | Yes | Yes | Yes | Yes | Yes |
| Compose lifecycle | Yes | Yes | Yes | Yes | Yes |
| Image builds through the hardened socket profile | Docker build streaming is proxied, but published Sockguard presets intentionally deny `/build` and BuildKit session paths | Yes | Yes | Yes | Full Docker proxy; Dockhand documents builds |
| General host shell or file browser | No; container exec only | Agent exposes file-browse APIs | Host shell supported | Volume/project file operations | No general host shell documented |
| Swarm orchestration | No; explicit non-goal | Yes | Yes | Yes | Not documented |
| Standard-mode agent auth | Ed25519 signature over method, request target, body hash, timestamp, and nonce; token fallback | Claim key exchange; optional shared `AGENT_SECRET` | Public-key authenticated channel | Agent token | Bearer token |
| Edge agent auth | Ed25519-signed hello over TLS | Edge key, revolving password, optional Business mTLS | Public-key handshake with per-server keys | Agent token; optional or required automatically enrolled mTLS | Token over WSS |
| Explicit per-request replay defense | Yes for signed HTTP requests | Not documented | Channel handshake; no per-request scheme documented | TLS/channel authentication; no per-request scheme documented | Not documented |
| Credential rotation | Multiple keys; file update plus SIGHUP; manual operational flow | Revolving Edge password; Edge key lifecycle | Automatic Periphery key rotation | Automatic certificate renewal; environment token can be regenerated | Manual token replacement |
| Docker socket least privilege | Recommended Sockguard path-and-method allowlist; Portwing need not mount the raw socket | Documented deployment mounts the socket and host paths directly | Documented deployment mounts the socket directly | Optional Tecnativa category-level socket proxy; direct socket is the default simple path | Documented deployment mounts the socket directly |
| Agent-level audit trail | Structured API, authentication, enrollment, Compose, and exec records; cursor-based export | Controller activity logs are a Business feature | Controller stores a full audit trail | Controller activities and security audit events | Debug request logs; no structured audit export documented |
| Agent Prometheus endpoint | Yes | No agent scrape endpoint documented | Host metrics and alerts, but no agent scrape endpoint documented | Metrics in the controller; no agent scrape endpoint documented | Host metrics forwarded to Dockhand; no scrape endpoint documented |
| Read-only MCP server | Yes | Not documented | Not documented | Not documented | Not documented |
| Verifiable release artifacts | Cosign signatures, CycloneDX SBOM, and SLSA Build L2 provenance | Not evaluated | Not evaluated | Cosign-verifiable artifacts and images | Checksummed releases; no equivalent set documented |
| Controller-managed agent upgrades | Not shipped; immutable packages/images are upgraded by the operator | Yes, including Edge fleet policies | Auto-update facilities exist; an agent rollout contract was not established in this review | Manager can upgrade agents | Manual deployment update |

## What the review changes

The market has closed several gaps that older Portwing material described as
advantages:

- Komodo v2 now supports outbound Periphery connections and public-key
  authentication with automatic key rotation. It no longer uses the old
  passkey-only model.
- Arcane now supports direct and outbound agents, polling transport, automated
  mTLS enrollment and renewal, optional Docker socket proxying, signed release
  artifacts, and controller-driven upgrades.
- Hawser now matches Portwing's broad topology: lightweight Go binary,
  transparent Docker API, Compose, host metrics, standard mode, and outbound
  edge mode.

Portwing's defensible agent-level advantages are narrower and more concrete:

- replay-resistant Ed25519 signatures on individual standard-mode requests;
- a signed edge hello without a reusable bearer credential on the agent;
- a documented Sockguard path-and-method policy boundary;
- structured audit records generated at the Docker mediation point;
- a native Prometheus endpoint and read-only MCP surface; and
- a deliberately small agent with a three-direct-dependency policy.

## Decisions

### Required before v1.0

1. **Release-artifact integration test.** Validate the tagged Portwing,
   Drydock, and Sockguard artifacts together in standard and edge modes. Cover
   lifecycle, events, snapshots, logs, exec, reconnects, backpressure, and the
   expected allow/deny behavior of every published Sockguard preset.
2. **Credential lifecycle exercise.** Test enrollment, overlapping-key
   rotation, revocation, SIGHUP reload, clock-skew failure, and recovery with a
   real Drydock controller. The cryptography is implemented; the fleet
   operation must be proved and documented.
3. **Published capability boundary.** Keep `COMPATIBILITY.md`, the OpenAPI
   contract, Sockguard presets, and this matrix aligned. Builds must remain
   explicitly denied in hardened examples until BuildKit's `/session` and
   `/grpc` surfaces can be constrained and tested.
4. **Competitive-claim verification.** Comparison pages must state the
   reviewed product version/date and link to this evidence. Unknown behavior
   is “not documented,” not “no.”

The review did not identify a missing container lifecycle, streaming, Compose,
observability, or authentication primitive that requires new Portwing code for
v1.0.

### Candidate work after v1.0 or when demand is proven

| Candidate | Owner | Decision |
| --- | --- | --- |
| Controller-managed Portwing upgrade/rollback waves | Drydock + Portwing packaging | Design after the tagged three-product acceptance pass. Do not add an unaudited self-update endpoint to the agent. |
| Optional mTLS client authentication | Portwing | Defense in depth for certificate-mandated environments; Ed25519 plus TLS remains the supported baseline. |
| Polling/intermittent edge transport | Drydock + Portwing | Defer until an offline/low-bandwidth deployment requires it; the current persistent tunnel is the Drydock contract. |
| Automated controller-assisted key rotation | Drydock + Portwing | Preserve operator-controlled trust roots; design a two-key overlap flow rather than letting a controller silently replace its only trust anchor. |
| BuildKit-aware Sockguard profile | Sockguard + Portwing | Defer until session and gRPC paths can be least-privilege and regression tested. Never fold build access into the base preset. |
| Broader platform support such as Podman or Windows agents | Portwing | Demand-driven; do not dilute Docker/Linux reliability before v1.0. |

### Explicit non-goals for the Portwing agent

- Host shell execution, arbitrary host-file browsing, and volume backup APIs.
- Swarm or Kubernetes orchestration.
- A bundled UI, RBAC database, GitOps engine, scheduler, vulnerability scanner,
  notification system, or secrets vault.
- Automatic workload updates or autonomous mutation of the agent.

Those are legitimate product capabilities, but they belong in Drydock or a
specialized adjacent service. Portwing should expose only the bounded,
observable primitives the controller needs.

## Feature ownership

| Layer | Owns |
| --- | --- |
| Portwing | Authenticated transport, Docker API mediation, streaming fidelity, Compose/exec primitives, health, metrics, audit production, and read-only MCP. |
| Drydock | Users and RBAC, fleet inventory, UI, GitOps, schedules, update policy, alerts, vulnerability workflows, and controlled agent rollout. |
| Sockguard | Last-mile Docker API path/method authorization and default-deny enforcement. |

## Primary sources

- Docker: [Protect the Docker daemon socket](https://docs.docker.com/engine/security/protect-access/)
- Portainer: [Agent repository](https://github.com/portainer/agent), [agent security model](https://docs.portainer.io/faqs/getting-started/how-does-portainer-secure-connectivity-to-and-from-agents-and-edge-agents), [Edge Agent architecture](https://docs.portainer.io/advanced/edge-agent), [Edge features](https://docs.portainer.io/faqs/getting-started/why-do-we-recommend-using-the-edge-agent-instead-of-the-traditional-agent), and [activity logs](https://docs.portainer.io/admin/logs/activity)
- Komodo: [introduction](https://komo.do/docs/intro), [server onboarding and key lifecycle](https://komo.do/docs/setup/connect-servers), [v2 architecture changes](https://komo.do/docs/releases/v2.0.0), [Compose](https://komo.do/docs/deploy/compose), [builds](https://komo.do/docs/build), and [Swarm](https://komo.do/docs/swarm)
- Arcane: [remote environments](https://getarcane.app/docs/features/environments), [edge mTLS](https://getarcane.app/docs/security/edge-mtls), [socket proxy](https://getarcane.app/docs/setup/socket-proxy), [artifact verification](https://getarcane.app/docs/security/verify-artifacts), and [RBAC capability list](https://getarcane.app/docs/security/rbac)
- Hawser: [repository and current feature documentation](https://github.com/Finsys/hawser)
- Distr: [Docker Agent](https://distr.sh/docs/agents/docker-agent/) and [logs and metrics](https://distr.sh/docs/agents/logs-and-metrics/)
- Docker Surgeon: [repository](https://github.com/kRYstall9/docker-surgeon)
- Socket proxies: [Tecnativa](https://github.com/Tecnativa/docker-socket-proxy), [LinuxServer](https://github.com/linuxserver/docker-socket-proxy), and [Wollomatic](https://github.com/wollomatic/socket-proxy)
