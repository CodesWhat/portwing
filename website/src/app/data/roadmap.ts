// SPDX-License-Identifier: MIT

export type Milestone = {
  version: string;
  title: string;
  emoji: string;
  status: "released" | "next" | "planned";
  items: string[];
};

// Milestones in chronological order (oldest first).
// The roadmap component reverses this for display — newest/planned at the top.
// HEAD is automatically the last entry with status "released".
export const roadmap: Milestone[] = [
  {
    version: "v0.1.0",
    title: "Foundation",
    emoji: "🛡️",
    status: "released",
    items: [
      "Standard mode: HTTP/SSE inbound server and transparent Docker Engine API proxy",
      "Edge mode: outbound WebSocket tunnel to the Drydock platform for hosts that cannot take inbound connections",
      "Drydock adapter with container sync and SSE event stream at /api/events",
      "Token authentication with timing-safe comparison, plus rate limiting of failed attempts per IP",
      "Health endpoint (/_portwing/health) for Docker HEALTHCHECK directives",
      "Multi-arch image: linux/amd64 + linux/arm64 on a Wolfi base, static CGO-free binary",
    ],
  },
  {
    version: "v0.2.0",
    title: "Security & Observability",
    emoji: "🔒",
    status: "released",
    items: [
      "Ed25519 per-request signatures with nonce LRU replay protection and SIGHUP key reload",
      "Single-use key enrollment (ENROLLMENT_TOKEN) with burn-on-first-use and audit logging",
      "Argon2id token hashing (TOKEN_HASH), Bearer auth, and TRUSTED_PROXIES for rate-limit client IPs",
      "Read-only MCP server at /_portwing/mcp, plus a headless generic REST adapter",
      "Prometheus metrics, structured JSON audit logging, and an OpenAPI 3.1 spec",
      "Cosign keyless signing, CycloneDX SBOMs, and SLSA build provenance on release artifacts",
      "Hardened Docker Compose examples for standard, edge, and sockguard-layered deployments",
    ],
  },
  {
    version: "v0.3.0",
    title: "Rename & Edge Fixes",
    emoji: "🏷️",
    status: "released",
    items: [
      "Renamed from Lookout to Portwing: module path, binary, image, headers, and metrics",
      "Startup banner with a version, mode, and adapter summary",
      "GoReleaser dockers_v2 migration: one multi-arch OCI index per tag with an image SBOM attestation",
      "Edge reconnect backoff restarts from RECONNECT_DELAY after a long-lived session drops",
      "Edge read deadline held at a steady state so a silent controller triggers a reconnect",
      "Fuzz worker cap so the coordinator keeps a core and the fuzz smoke stops flaking",
    ],
  },
  {
    version: "v0.4.0",
    title: "Quality Gates",
    emoji: "🧪",
    status: "released",
    items: [
      "Monthly one-hour deep fuzz pass, completing the smoke, nightly, and monthly tiers",
      "Weekly soak test: resident-set budget under sustained SSE and proxy load",
      "Monthly benchmark tracking: hot-path ns/op and allocs/op kept as a 90-day artifact",
      "Edge tunnel test harness covering hello signing, dispatch, and the exec-session lifecycle",
      "Ordered edge exec I/O: input that arrives before the exec is up is buffered and replayed",
      "Outbound backpressure: bounded write queue, per-frame deadline, slow-consumer eviction",
      "Dependency scanning standardized on Grype and govulncheck, with the Go toolchain pinned to go1.26.4",
    ],
  },
  {
    version: "v0.5.0",
    title: "Hardening",
    emoji: "🔐",
    status: "released",
    items: [
      "Request and application Prometheus metrics: request counts, duration, in-flight, auth failures, rate limits",
      "Audit ring buffer (AUDIT_BUFFER_SIZE) with an authenticated GET /_portwing/audit endpoint",
      "Pre-auth body cap: signed requests buffered through a 1 MB limit before signature verification",
      "Registry-auth and proxy query parameters validated before reaching the daemon",
      "Private-key file-permission check: PRIVATE_KEY_FILE is rejected when group or world accessible",
      "Outbound TLS 1.2 floor, and edge mode now requires PRIVATE_KEY_FILE",
      "Kubernetes DaemonSet examples for standard and edge mode",
    ],
  },
  {
    version: "v0.6.0",
    title: "Non-Root & Cross-Repo Parity",
    emoji: "🔐",
    status: "released",
    items: [
      "Agent info surfaced in the Drydock UI — memoryGb, logLevel, and pollInterval reported on every dd:ack",
      "Edge-mode container deletion — dd:container_delete_request/response over the WebSocket tunnel",
      "Container environment variables in RuntimeDetails, parsed from Docker's Config.Env",
      "COMPATIBILITY.md — canonical cross-repo version matrix for Portwing × Drydock × sockguard",
      "BREAKING: container image now runs as non-root (UID 65532) by default — root is an explicit opt-in",
      "CI egress lockdown — harden-runner egress-policy: block across all jobs in every workflow",
    ],
  },
  {
    version: "v0.7.0",
    title: "Fail-Closed Security",
    emoji: "🛡️",
    status: "released",
    items: [
      "Standard mode fails closed without credentials — unauthenticated development requires explicit loopback and remote opt-ins",
      "Ed25519 HTTP signature v2 binds the escaped path and exact raw query; legacy signatures are query-free only",
      "Cold Argon2id work is capped agent-wide and per IP; raw-token verifiers retain fixed-size digests",
      "Credential files require regular-file descriptors and safe Unix permissions, checked after open",
      "Enrollment bodies are capped and abuse-limited; Compose uploads use root-confined, symlink-resistant writes",
      "Edge request correlation, bounded log-follow options, and terminal hello-rejection classification",
      "Generated CSP and browser security headers cover every exported website and docs route",
    ],
  },
  {
    version: "v0.8.0",
    title: "Toward v1.0",
    emoji: "🧭",
    status: "released",
    items: [
      "Edge mode graduated to production support — enabled by default in Drydock 1.6 and gated by a real cross-repo multi-agent load/soak",
      "Published stability policy — semver guarantees for the HTTP API, env-var surface, MCP tools, and DrydockCompat wire contract",
      "Package-manager distribution — a Homebrew tap and apt/rpm packages via the existing GoReleaser pipeline",
      "Continuous edge live-tail under the dd: namespace, with cancellation, stdout/stderr framing, and bounded backpressure",
      "Mode-aware liveness/readiness, edge/backpressure metrics, stable cursor-based NDJSON audit export, and runnable observability examples",
    ],
  },
  {
    version: "v0.9.0",
    title: "Controller-Owned Updates",
    emoji: "🔄",
    status: "released",
    items: [
      "Drydock watcher/update execution runs controller-side through Portwing's authenticated Docker proxy in Standard and Edge modes",
      "Watcher descriptor declares transport=docker-api, execution=controller, and events=portwing; no remote trigger is advertised",
      "Edge sends component ownership before raw inventory; full feature compatibility requires Drydock v1.6.0-rc.11+ while DrydockCompat remains 1.4.0",
      "Edge mode shares the cursor-based NDJSON audit exporter on its explicitly private, unauthenticated operations listener",
      "Exec and Docker proxy failures preserve bounded daemon/sockguard error details through the controller path",
      "Tagged Portwing × Drydock × Sockguard acceptance matrix passed in standard and edge modes — completed 2026-08-29",
      "Controller-backed enrollment, overlapping-key rotation, revocation, and clock-skew recovery exercised against a real Drydock controller — completed 2026-08-29",
    ],
  },
  {
    version: "v1.0",
    title: "Stable Release",
    emoji: "🚀",
    status: "released",
    items: [
      "Stable HTTP API and environment-variable surface under semantic-versioning guarantees",
      "Stable agent ↔ controller wire protocol — no breaking changes without a major bump",
      "Threat model review; the HIGH+ CVE gate is already enforced on every release image, and v1.0 keeps it",
      "Competitive review refreshed against primary sources",
      "Final v0.9 docs archived from the release tag with byte-for-byte provenance checks",
      "The release candidate passed the Standard/Edge and legacy-floor conformance matrix before GA; each later release needs fresh published-artifact verification",
    ],
  },
  {
    version: "v1.1",
    title: "Enrollment Over TLS",
    emoji: "🔏",
    status: "planned",
    items: [
      "Enrollment requires TLS, either native or through a trusted proxy reporting HTTPS, unless ALLOW_INSECURE_ENROLLMENT explicitly opts in (v1.0.2 only warns)",
    ],
  },
  {
    version: "Post-v1",
    title: "Demand-Driven",
    emoji: "🧭",
    status: "planned",
    items: [
      "Controller-managed Portwing upgrade and rollback waves",
      "Optional client-certificate authentication",
      "Polling and intermittent edge transport",
    ],
  },
];
