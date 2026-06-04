# Lookout Competitive Roadmap

> Security-first, platform-agnostic Docker remote agent.

## Positioning

Lookout is a standalone, security-first Docker remote access agent. Drydock is the first supported platform, but the core value — authenticated Docker API proxy, NAT traversal, exec tunneling, Compose management, host metrics — is platform-agnostic.

No other project in this space ships a standalone, security-hardened, open-source Docker remote agent.

---

## Competitive Landscape

### Direct Competitors (Agent-Based Remote Docker Access)

| Capability | Lookout 0.1.0 | Portainer Agent | Dockge Agent | Coolify |
|---|---|---|---|---|
| **Docker API proxy** | Full transparent proxy | Full proxy + cluster aggregation | No (shells out to CLI) | No (SSH tunnel to Docker) |
| **Edge/NAT traversal** | WebSocket outbound | Chisel SSH tunnel + polling | Socket.IO | SSH |
| **Interactive exec** | WebSocket + HTTP hijack | WebSocket | xterm.js + node-pty | SSH |
| **Compose management** | Full lifecycle + security hardening | Stack deploy (Compose/Swarm/K8s) | Compose-only (CLI spawn) | Compose + Dockerfile |
| **Host metrics** | CPU, memory, disk, network, uptime | Via Portainer UI | None | Sentinel agent |
| **Container inventory** | Full with label-based config | Full with Swarm aggregation | Compose stacks only | Per-server |
| **Auto-reconnect** | Exponential backoff + jitter | Polling + on-demand tunnel | Socket.IO reconnect | SSH retry |
| **Multi-platform** | Docker only | Docker, Swarm, K8s, Podman, Nomad | Docker only | Docker only |
| **Binary size** | ~10 MB static Go | ~15 MB Go | ~200 MB Node.js | Laravel + Docker |
| **License** | AGPL-3.0 | zlib (agent), proprietary (BE) | MIT | AGPL-3.0 |

### Indirect Competitors (Docker Management, No Remote Agent)

| Tool | What It Does | Why It's Not a Competitor |
|---|---|---|
| **Watchtower** | Auto-updates containers on a single host | No remote access, no management API, no exec, homelab-only |
| **Diun** | Notifies about image updates | Notification-only, no container management |
| **Lazydocker** | Terminal UI for local Docker | Local-only TUI, no remote agent |
| **CasaOS** | Single-host web dashboard | No remote/multi-host, critical auth bypass CVEs (CVSS 9.8), maintenance mode |
| **Yacht** | Single-host web UI | Abandoned (last release Jan 2023), never left alpha |

### Enterprise Platforms (Different Category)

| Tool | Overlap | Differentiation |
|---|---|---|
| **Rancher** | Multi-host management, agents | Kubernetes-only, heavy (needs K8s cluster to run itself), enterprise complexity |
| **Docker Swarm** | Built-in clustering | No standalone agent, no granular auth, no web management |
| **Cosmos Server** | Reverse proxy + container mgmt | All-in-one monolith, Commons Clause license, single developer |

---

## Security Comparison

This is where Lookout differentiates. Security is the brand.

| Security Feature | Lookout 0.1.0 | Portainer Agent | Dockge | Coolify |
|---|---|---|---|---|
| **Auth mechanism** | Token (timing-safe) | HMAC signature + first-claim | JWT + bcrypt | SSH keys |
| **Token transmission** | SHA-256 hash (edge), header (std) | Base64 HMAC + public key | Cookie | SSH |
| **Rate limiting** | 10 fails/min/IP, 10K IP cap | None on agent | 5 fails/min/IP | None |
| **TLS** | 1.2+ with modern AEAD ciphers | Auto-generated self-signed | None (reverse proxy) | Via Traefik |
| **mTLS** | Not yet | Supported (edge) | No | No |
| **Compose security** | Path traversal protection, env denylist, flag injection prevention | Basic validation | None | None documented |
| **Resource limits** | WS 16MB, response 100MB, exec 100 concurrent | Not documented | Not documented | Not documented |
| **Panic recovery** | Yes (middleware) | Not documented | Not documented | Not documented |
| **CVE history** | None (new project) | 15+ CVEs in 2024-2025 | Agent creds stored plaintext | 11 CVEs Jan 2026 (incl. RCE) |

---

## Roadmap: Closing Gaps & Building Advantages

### Phase 1: Foundation (v0.1.0 — Current)

What we ship today:

- [x] Dual connection modes (standard HTTP + edge WebSocket)
- [x] Transparent Docker API proxy
- [x] Container inventory with dd.* label parsing
- [x] Host metrics (CPU, memory, disk, network, uptime)
- [x] Interactive exec (WebSocket + HTTP hijack)
- [x] Docker Compose lifecycle with security hardening
- [x] SSE backward compatibility for Drydock
- [x] Token auth with timing-safe comparison
- [x] Rate limiting
- [x] TLS 1.2+ with modern ciphers
- [x] Compose path traversal / env var / flag injection protection
- [x] Minimal container image (Wolfi OS, ~10 MB)

### Phase 2: Platform-Agnostic Core (v0.2.0)

Decouple from Drydock. Make Lookout useful standalone.

- [ ] **Adapter architecture**: Extract Drydock protocol into `adapters/drydock`, define adapter interface
- [ ] **Generic REST adapter**: Plain REST/WebSocket API for any client (no Drydock dependency)
- [ ] **Docker event streaming**: Real-time container lifecycle events over WebSocket (generic format)
- [ ] **API documentation**: OpenAPI spec for the generic API surface
- [ ] **Standalone mode**: Useful without any platform — health, info, metrics, proxy, compose

### Phase 3: Security Hardening (v0.3.0)

Close gaps vs. Portainer. Establish security leadership.

- [ ] **mTLS support**: Client certificate authentication for both standard and edge modes
- [ ] **Token rotation**: Support for periodic token refresh via file watch or API
- [ ] **Request signing**: HMAC-based message authentication for WebSocket protocol (anti-replay)
- [ ] **Audit logging**: Structured JSON logs for all authenticated API calls, exec sessions, compose operations
- [ ] **Docker API allowlisting**: Configurable allowlist of permitted Docker API paths (block dangerous endpoints)
- [ ] **Exec restrictions**: Allowlist permitted commands, restrict user field, log all exec sessions
- [ ] **TLS_SKIP_VERIFY warning**: Log loud warnings when enabled, refuse in production mode

### Phase 4: Observability & Monitoring (v0.4.0)

Beat Portainer on metrics. Nobody else ships agent-level observability.

- [ ] **Container-level metrics**: Per-container CPU, memory, network, disk I/O via Docker stats API
- [ ] **Prometheus endpoint**: `/metrics` with standard container and host metrics
- [ ] **Health check monitoring**: Track container health status transitions
- [ ] **Alerting hooks**: Webhook callbacks for container state changes, health failures, resource thresholds
- [ ] **Metrics history**: Short-term in-memory ring buffer for recent metrics (no external DB needed)

### Phase 5: Image Security (v0.5.0)

Unique differentiator — no other agent does this.

- [ ] **Image vulnerability scanning**: Integrate Trivy as a library or sidecar for on-agent image scanning
- [ ] **Image signature verification**: Cosign verification before container start/update
- [ ] **SBOM generation**: Per-container software bill of materials
- [ ] **Security audit endpoint**: `/_lookout/audit` returns security posture of all running containers
- [ ] **Signed releases**: Cosign keyless signing of Lookout container images via GitHub Actions OIDC

### Phase 6: Multi-Platform (v0.6.0)

Expand beyond Docker. Match Portainer's platform reach.

- [ ] **Podman support**: Podman socket compatibility (API is Docker-compatible, minimal changes)
- [ ] **Docker Swarm awareness**: Node info, service discovery, Swarm-specific metrics
- [ ] **Portainer adapter**: Implement Portainer agent protocol for drop-in replacement
- [ ] **Socket proxy mode**: Optional restricted Docker socket proxy (filter allowed API endpoints)

### Phase 7: Enterprise Features (v1.0.0)

Production readiness for teams and organizations.

- [ ] **RBAC**: Role-based endpoint authorization (read-only, operator, admin)
- [ ] **Multi-token support**: Multiple tokens with different permission levels
- [ ] **Configuration API**: Remote configuration updates without restart
- [ ] **Cluster mode**: Multiple Lookout agents coordinating with shared state
- [ ] **Compliance reporting**: CIS Docker Benchmark checks, exportable reports

---

## Key Strategic Decisions

### Why Not Just Fork Portainer Agent?

- Portainer's agent has 15+ CVEs in the last 2 years. The codebase reflects "add features first, secure later."
- Portainer Business Edition locks RBAC and advanced features behind a proprietary license.
- Their bridge network exposure issue (#187) has been open since 2020 — architectural security debt.
- Lookout starts security-first. It's easier to add features to a secure foundation than to retrofit security.

### Why Go Instead of Node.js (Like Dockge)?

- Static binary, zero runtime dependencies, ~10 MB image vs. ~200 MB
- No `node_modules` supply chain attack surface
- Native concurrency for exec session multiplexing
- Cross-compilation to linux/amd64, linux/arm64, linux/arm/v7 with zero friction

### Why AGPL-3.0?

- Ensures improvements flow back to the community
- Prevents proprietary forks that strip security features
- Compatible with the security-first mission — transparency is security
