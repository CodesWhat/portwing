# Lookout Competitive Roadmap

> Security-first, platform-agnostic Docker remote agent.

## Positioning

Lookout is a standalone, security-first Docker remote access agent. Drydock is the first supported platform, but the core value — authenticated Docker API proxy, NAT traversal, exec tunneling, Compose management, host metrics — is platform-agnostic.

As of June 2026 this niche has a direct competitor: Hawser v0.2.44, paired with the Dockhand management UI, ships the same four capabilities (transparent Docker API proxy, outbound WebSocket edge mode, interactive exec, Compose lifecycle). Hawser has 4,800 stars via Dockhand's GUI halo effect. Lookout's first-mover advantage in this exact niche is gone. The differentiation must be articulated, not assumed.

Lookout's structural advantages cannot be replicated by Hawser or any other agent without rebuilding from scratch:

- **Proxy-first architecture**: token auth and request inspection happen before any request reaches the Docker daemon. CVE-2026-23944 (Arcane, auth check after proxy forwarding) and CVE-2026-34040 (Docker AuthZ bypass via body truncation) are structurally impossible in this model.
- **Compose hardening as testable security controls**: path-traversal guard, env denylist, flag injection filter — named, tested, documented, with CVE mappings. No competitor has done this.
- **Stateless proxy, zero state drift**: Lookout keeps no internal database. Every response is what the Docker daemon reports. Portainer's database drifting from actual container state is the top community complaint driving migration. Lookout cannot drift.
- **Two-layer sockguard defense**: Lookout fronts the user-facing API; sockguard fronts the raw Docker socket with request-body inspection. No competitor has a sibling project capable of inspecting POST body payloads at the socket layer.
- **Supply-chain integrity**: Wolfi/scratch base image + cosign keyless signing + SBOM + SLSA L2 provenance. Most competitors ship none of these.
- **AGPL-3.0, no node cap, two static binaries**: Portainer caps free use at 3 nodes. Komodo requires MongoDB. Lookout + Drydock is two binaries with no commercial gating.

---

## Competitive Landscape

> All competitor claims reflect publicly available information as of June 2026.

### Direct Competitors

| Capability | Lookout 0.1.0 | Hawser 0.2.44 | Arcane 2.0.2 | Komodo/Periphery 2.2.0 | Portainer Edge Agent 2.39 | Drydock 1.5.0-rc |
|---|---|---|---|---|---|---|
| Transparent Docker Engine API proxy | **WIN** | Partial (not documented) | No (abstraction layer) | No (Rust SDK calls) | No (abstraction layer) | No |
| Outbound WebSocket edge / NAT traversal | Yes | Yes | Yes (gRPC/WS) | Yes (v2.0+) | Yes (Chisel) | SSE agents (maturing) |
| Interactive exec tunneling | Yes | Yes | Yes | Yes | Yes | No |
| Docker Compose lifecycle | Yes | Yes | Yes | Yes | Yes | Yes (update focus) |
| Compose path-traversal protection | **WIN** | Not documented | Not documented | Not documented | CVE-44885 (failed) | Partial |
| Compose env denylist | **WIN** | Not documented | Not documented | Not documented | Not documented | Partial |
| Compose flag injection protection | **WIN** | Not documented | Not documented | Not documented | Not documented | No |
| /proc-based host metrics | Yes | Yes | No | Yes | No | No |
| Timing-safe token auth before proxy work | **WIN** | No (Dockhand enforces) | Fixed post-CVE-2026-23944 | Yes | Yes | Yes |
| Per-client asymmetric key auth | No | No | mTLS enrollment | **WIN** (Ed25519, auto-rotate) | mTLS | No |
| Rate limiting | Yes | No | Not documented | No | Yes | No |
| Sockguard (request-body inspection) | **WIN** (sibling project) | No | No | No | No | Recommended (Tecnativa) |
| Cosign-signed releases | **WIN** (v0.2.0+) | No | Yes | No | No | No |
| SBOM published | **WIN** (v0.2.0+) | No | Yes | No | No | No |
| Wolfi/scratch image | **WIN** | No (standard base) | Distroless | No | No | No |
| Static binary, zero dependencies | **WIN** | Partial | No (binary + frontend) | No (Rust + MongoDB) | No | No (Node.js) |
| No internal state database | **WIN** | Partial | No (SQLite) | No (MongoDB required) | No (Portainer DB) | No (SQLite) |
| AGPL license, no node cap | **WIN** | MIT | BSD-3-Clause | GPL-3.0 | zlib (BE proprietary) | AGPL |
| CVE history | None | None | CVE-2026-23944 (fixed) | None | 9 in 2026 | None |
| Audit logging | No | No | No | Yes | BE only | Yes |
| MCP server | No | No | No | No | No | No |
| RBAC | No | No (Dockhand) | Yes | **WIN** | Yes (BE) | No |
| Fleet management (multi-host) | No | No (via Dockhand) | Yes | **WIN** | Yes | Yes |

### Security Comparison

| Security Property | Lookout 0.1.0 | Hawser 0.2.44 | Arcane 2.0.2 | Komodo 2.2.0 | Portainer Edge 2.39 |
|---|---|---|---|---|---|
| Auth check before any proxy work | **WIN** | No | Fixed post-CVE | Yes | Yes |
| Timing-safe token comparison | **WIN** | Argon2id (Dockhand side) | Token | Ed25519 (**WIN**) | mTLS (**WIN**) |
| AuthZ plugin bypass immunity | **WIN** (pre-daemon model) | No | No | No | No |
| Rate limiting | Yes | No | Not documented | No | Yes |
| TLS 1.2+ with modern AEAD ciphers | Yes | Not documented | Yes | Yes | Yes |
| mTLS | No (P3) | No | Yes | Noise XX | Yes (edge) |
| Compose path-traversal guard | **WIN** | Not documented | Not documented | Not documented | CVE-44885 |
| Request-body-inspecting socket proxy | **WIN** (sockguard) | No | No | No | No |
| Wolfi/scratch image | **WIN** | No | Distroless | No | No |
| Cosign-signed releases | **WIN** (v0.2.0+) | No | Yes | No | No |
| SBOM | **WIN** (v0.2.0+) | No | Yes | No | No |
| CVE history | None | None | 1 (fixed) | None | 9 in 2026 |

### Indirect Competitors

| Tool | What It Does | Why It's Not a Competitor |
|---|---|---|
| **Watchtower** | Auto-updates containers | Archived December 2025. Drydock + Lookout is the replacement stack. |
| **Dozzle** | Real-time log viewer | Log streaming only, no management API, no exec, no Compose |
| **Lazydocker** | Terminal UI for local Docker | Local-only TUI, no remote agent |
| **CasaOS** | Single-host web dashboard | No remote/multi-host; auth bypass CVEs (CVSS 9.8); maintenance mode |
| **Yacht** | Single-host web UI | Abandoned (last release Jan 2023) |

---

## Roadmap

### Phase 1: Foundation (v0.1.0 — Released)

- [x] Dual connection modes (standard HTTP + edge WebSocket)
- [x] Transparent Docker API proxy
- [x] Container inventory with `dd.*` label parsing
- [x] Host metrics (CPU, memory, disk, network, uptime)
- [x] Interactive exec (WebSocket + HTTP hijack)
- [x] Docker Compose lifecycle with security hardening
- [x] SSE backward compatibility for Drydock
- [x] Token auth with timing-safe comparison
- [x] Rate limiting (10 fails/min/IP)
- [x] TLS 1.2+ with modern cipher suites
- [x] Compose path traversal / env var / flag injection protection
- [x] Minimal container image (Wolfi OS, ~10 MB)

---

### Phase 1.5: Release Integrity & Community Visibility (v0.2.0 — Complete)

Without these, no P2–P7 feature will get evaluated. Supply-chain gaps block enterprise admission controllers. No API docs blocks developer adoption. No metrics endpoint blocks the existing Grafana user base. This is the actual launch phase.

- [x] **Cosign keyless signing**: Container image and release binaries signed via GitHub Actions + Sigstore, zero stored secrets
- [x] **SBOM**: CycloneDX SBOM attached as OCI attestation and release artifact
- [x] **SLSA L2 provenance**: Build provenance from GitHub Actions release pipeline
- [x] **OpenAPI 3.1 specification**: All current endpoints documented; published in-repo and rendered at docs URL
- [x] **Prometheus `/metrics` endpoint**: Host metrics + per-container CPU/memory/net I/O in cAdvisor-compatible format, token-authenticated
- [x] **Security model documentation**: Named controls (proxy-first architecture, Compose path-traversal guard, env denylist, flag injection filter), CVE mapping table, supply-chain verification instructions
- [x] **Watchtower migration guide**: `docs/migrating-from-watchtower.md` covering Lookout + Drydock as the replacement stack, targeting the migrating 36k-star user base
- [x] **Argon2id token-at-rest**: Stored token value hashed with Argon2id, replacing plaintext config storage
- [x] **Full Drydock SSE compatibility**: `dd:watcher-snapshot` emitted after every poll cycle and on connect — zero Drydock-side changes needed (GAP-1 closed)
- [x] **Repo infrastructure parity with drydock/sockguard**: hardened SHA-pinned CI (zizmor, CodeQL, OpenSSF Scorecard, dependency review), Go fuzz targets (5 parsers, tier-1 CI + nightly), integration tests against real dockerd, monthly Gremlins mutation testing, weekly govulncheck/grype/gosec, release-cut dispatch workflow, dependabot, CHANGELOG/CONTRIBUTING/CODE_OF_CONDUCT/RELEASING/AGENTS docs, hardened compose examples incl. sockguard two-layer defense

---

### Phase 3 (top): Asymmetric Auth & Audit Logging (v0.3.0-early)

Pulled before P2 because Komodo shipped Ed25519 PKI in March 2026 and Arcane shipped mTLS enrollment in May 2026. Lookout's shared token is now below the field average for a security-first agent.

- [x] **Per-client Ed25519 key pairs** *(shipped v0.2.0)*: Generate a keypair per
  client; exchange public keys via operator-provisioned `authorized_keys` file
  (Model B) or one-shot enrollment token (Model C). Ed25519 signatures on every
  request with timestamp window + nonce LRU replay protection. Token auth retained
  as fallback. `lookout keygen` CLI subcommand. Edge-mode signed hello.
  See `docs/design/ed25519-auth.md` (Status: Implemented Phase 1).
- [x] **Audit logging** *(shipped v0.2.0)*: Structured JSON log of all authenticated Docker API calls, exec sessions, enrollment attempts, and Compose operations (timestamp, caller identity, endpoint, method, outcome) via `AUDIT_LOG`. Portainer charges for this in BE; Lookout ships it free.

---

### Phase 2: Platform-Agnostic Core (v0.3.0)

- [ ] **MCP server endpoint**: SSE transport, read-only — container inventory, host metrics, Compose stack status. Dokploy, Coolify, and Glances all shipped MCP in 2025–2026. No remote Docker agent has done this.
- [ ] **Drydock agent registration handshake**: Implement the Drydock agent protocol so Lookout appears in Drydock's Agents UI with full status
- [ ] **Generic REST adapter / headless management API**: Plain REST + WebSocket API for any client without Drydock dependency
- [ ] **Docker event streaming**: Real-time container lifecycle events over SSE (generic format)

---

### Phase 4: Observability (v0.4.0)

- [ ] **Per-container metrics ring buffer**: Short-term in-memory ring buffer for recent per-container stats (no external DB)
- [ ] **Health check monitoring**: Track container health status transitions
- [ ] **Alert webhooks**: Callbacks for container state changes, health failures, resource thresholds
- [ ] **cgroup v2 `memory.events` metrics**: Memory pressure and OOM count — cAdvisor v0.57.0 added this in May 2026; matching it completes the cAdvisor-replacement story
- [ ] **S.M.A.R.T. disk health endpoint**: Disk health surface for NAS/homelab operators (Beszel ships this, Lookout does not)

---

### Phase 5: Image Security (v0.5.0)

- [ ] **Trivy scan endpoint**: Optional endpoint that surfaces Trivy/Grype scan results for a named image via Lookout API
- [ ] **Cosign verification endpoint**: Verify a running container's image signature on demand
- [ ] **SBOM endpoint**: Return the SBOM for a running container's image
- [ ] **Drydock Update Bouncer hook**: Before Drydock triggers a container update via Lookout, Lookout can return a scan result for the candidate image — Dockhand/Hawser calls this "safe-pull"; Lookout + Drydock matches it without Lookout owning the scanning UI

---

### Phase 3 (remainder): Security Hardening (v0.5.5)

- [ ] **mTLS listener mode**: Client certificate authentication for both standard and edge modes; closes the Portainer Edge Agent comparison gap
- [ ] **Docker API allowlisting**: Configurable allowlist of permitted Docker API paths; complements sockguard at the path level
- [ ] **Exec restrictions**: Allowlist permitted commands, restrict user field, scope which containers a token can exec into
- [ ] **Credential scrubbing**: Strip secrets from `GET /containers/{id}/json` env arrays and label values before returning to caller — prevents the "Leaky Labels" pattern (Dozzle, Glances CVE-2026-35587)
- [ ] **Docker Engine version healthcheck**: Warn at startup if Docker Engine is below the CVE-2026-34040 patched version; position Lookout as security-aware infrastructure

---

### Phase 6: Multi-Platform (v0.6.0)

- [ ] **Podman socket support**: Podman's Docker-compatible API; minimal changes, high value as the containerd/Podman migration accelerates
- [ ] **Docker Swarm read-only inventory**: Node info, service discovery, Swarm-specific metrics — minimum viable Swarm awareness for fleet context
- [ ] **Dockhand/Hawser compatibility mode**: Respond to Dockhand's agent registration protocol so Dockhand users can substitute Lookout and get its security properties without replacing their UI (Hawser is MIT; protocol is inspectable)

---

### Phase 7: Enterprise (v1.0.0)

- [ ] **RBAC**: Multi-token with per-resource scoping (read-only, operator, admin)
- [ ] **OIDC/OAuth2**: Table-stakes for team and organization evaluation; deferred here due to implementation scope but cannot slip further
- [ ] **Cluster mode**: Multiple Lookout agents coordinating without shared state database
- [ ] **AES-256-GCM at rest**: Encrypt any credentials or config stored on disk — meaningful for regulated environments
- [ ] **CIS Docker Benchmark reports**: Exportable compliance reports — deferred until audit logging ships and produces the data these reports require

---

## Key Strategic Decisions

### Why Not Just Fork Portainer Agent?

Portainer's agent has 9 CVEs in 2026 alone (CVE-44848, -44849, -44850 are all authorization-bypass flaws in Portainer's own Docker API proxy layer). The codebase reflects "add features first, secure later." Portainer Business Edition gates RBAC and audit logging behind a proprietary license. Their bridge network exposure issue (#187) has been open since 2020.

Lookout starts security-first. Adding features to a secure foundation is straightforward; retrofitting security onto a feature-complete codebase produces CVEs.

### Why Go Instead of Node.js (Like Dockge)?

- Static binary, zero runtime dependencies, ~10 MB image vs. ~200 MB
- No `node_modules` supply chain attack surface
- Native concurrency for exec session multiplexing
- Cross-compilation to linux/amd64, linux/arm64, linux/arm/v7 with zero friction

### Why AGPL-3.0?

- Ensures improvements flow back to the community
- Prevents proprietary forks that strip security features
- Closes the network-use loophole that GPL-3.0 (Komodo) does not — AGPL-3.0 requires source disclosure when the software is accessed over a network, which matters for enterprise licensing evaluation
- Compatible with the security-first mission — transparency is security

### Why Statelessness Is a Security Property

Lookout keeps no internal database. Every value it returns is what the Docker daemon reports at the moment of the request. Portainer's internal database drifting from actual Docker state is the top community complaint driving migration. Beyond user experience, stale state in a security tool creates authorization gaps — a container that was removed is still authorized, a new container is not yet blocked. Lookout cannot produce this class of inconsistency. "Lookout cannot drift from Docker state because it keeps none."

### The Two-Layer Sockguard Defense

Lookout fronts the user-facing API and enforces authentication, rate limiting, Compose hardening, and (eventually) audit logging before any request reaches the Docker daemon. Sockguard fronts the raw Docker socket with request-body inspection — it can see inside `POST /containers/create` payloads, which path/method-only proxies (Tecnativa, wollomatic, linuxserver) cannot. CVE-2026-34040 exploited exactly this gap. The recommended deployment runs both: Lookout for authenticated API access, sockguard between Lookout and the Docker socket for defense in depth. No competitor has a sibling project with this capability; it cannot be replicated without rebuilding.

### Why Hawser Is the Threat, Not the Benchmark

Hawser ships the same four core capabilities as Lookout v0.1.0 and has more stars. The instinct is to match Hawser feature-for-feature. That is the wrong response. Hawser's auth is enforced by Dockhand (not the agent itself), Hawser has no request-body inspection, and Dockhand is MIT-licensed without the copyleft guarantee that keeps security improvements public. Lookout's correct play is to widen the security gap — publish the proxy-first security model, ship supply-chain artifacts that Hawser lacks, and become the agent that security-conscious operators choose precisely because Hawser does not make the same guarantees.
