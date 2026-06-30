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
      "Standard mode: HTTP/SSE inbound server, Docker Engine API proxy",
      "Token authentication — plaintext, file-based, and Argon2id hash-at-rest",
      "Docker Compose stack lifecycle (up / down / pull / ps)",
      "Exec tunnel: interactive container sessions streamed over HTTP",
      "Structured JSON audit log with in-memory ring buffer and pull endpoint",
      "Health endpoint (/_portwing/health) for Docker HEALTHCHECK directives",
      "Multi-arch image: linux/amd64 + linux/arm64, read_only + cap_drop:ALL runtime defaults",
    ],
  },
  {
    version: "v0.3.0",
    title: "Edge Mode",
    emoji: "🌐",
    status: "released",
    items: [
      "Edge mode: outbound WebSocket tunnel to Drydock — no inbound port required",
      "Ed25519 per-request signatures with nonce LRU replay protection",
      "Ordered exec I/O — input that arrives before the exec is up is buffered and replayed",
      "Outbound backpressure: bounded write queue, per-frame deadline, slow-consumer eviction",
      "Reproducible base images: Dockerfile and Dockerfile.release pinned by digest",
      "Three-tier fuzz harness: 60s PR smoke, 5m nightly, 1h monthly deep pass",
      "Weekly soak test: resident-set budget under sustained SSE+proxy+exec load",
    ],
  },
  {
    version: "v0.5.0",
    title: "Security Hardening",
    emoji: "🔒",
    status: "released",
    items: [
      "Pre-auth body limits tightened — oversized requests blocked before auth runs",
      "Registry-auth and proxy query parameters validated before reaching the daemon",
      "Private-key file-permission parity — startup fails on world-readable keys",
      "Consistent outbound TLS posture across all HTTP client paths",
      "Goroutine-lifecycle cleanup on shutdown — no leaks under SIGTERM",
      "Benchmark tracking: hot-path ns/op + allocs/op kept as a 90-day artifact",
      "Cosign-signed images, SBOMs, and build provenance on every release",
    ],
  },
  {
    version: "v0.6",
    title: "Operational Ergonomics",
    emoji: "📊",
    status: "next",
    items: [
      "Richer health/metrics endpoint — uptime, active connections, queue depth",
      "Structured audit export sink parity (file / stdout / stderr fully consistent)",
      "Ready-to-run deployment examples for common topologies (Traefik, Caddy, Kubernetes)",
      "Docs site and marketing copy synced to current release; security-model numbering corrected",
    ],
  },
  {
    version: "v1.0",
    title: "Stable Release",
    emoji: "🚀",
    status: "planned",
    items: [
      "Stable HTTP API and environment-variable surface under semantic-versioning guarantees",
      "Stable agent ↔ controller wire protocol — no breaking changes without a major bump",
      "Threat model review, CVE-clean base image, published security policy at security@codeswhat.com",
    ],
  },
];
