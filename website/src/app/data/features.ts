// SPDX-License-Identifier: MIT

export type FeatureCategory = "security" | "control" | "operations";

/** Icon ids are Phosphor names from components/icon-symbols.ts. */
interface Feature {
  icon: string;
  title: string;
  description: string;
  category: FeatureCategory;
}

export const features: Feature[] = [
  {
    icon: "shield-check",
    title: "Default-Deny Socket",
    description:
      "Pair us with sockguard and we never touch the raw Docker socket. The agent talks to a filtered unix socket instead, so even a fully compromised agent is constrained to an explicit Docker API allowlist enforced at the socket level.",
    category: "security",
  },
  {
    icon: "fingerprint",
    title: "Ed25519 Per-Client Auth",
    description:
      "Signed requests with nonce-based replay protection and no shared secrets on the wire. Enroll client public keys into an authorized_keys file, rotate with SIGHUP, and every enrollment is audited.",
    category: "security",
  },
  {
    icon: "key",
    title: "Argon2id Token Hashing",
    description:
      "Prefer a shared secret? Store only its Argon2id hash via TOKEN_HASH. Generate one with `portwing hash-token`; comparison is constant-time. Plaintext TOKEN is supported for evaluation but the hash is the recommended posture.",
    category: "security",
  },
  {
    icon: "check",
    title: "Signed & Verifiable",
    description:
      "Every release ships cosign signatures, per-archive SBOMs, an image SBOM attestation, and SLSA build provenance. Verify the image before you ever trust it with your host — the supply chain is auditable end to end.",
    category: "security",
  },
  {
    icon: "lock",
    title: "Hardened Runtime",
    description:
      'We ship hardened by default: `read_only`, `cap_drop: ALL`, `no-new-privileges`, and tokens mounted as secrets instead of env vars. The image runs as non-root (UID 65532) out of the box; running as root (`user: "0:0"`) is an explicit opt-in for socket-group compatibility. No security theater.',
    category: "security",
  },
  {
    icon: "anchor",
    title: "Drydock-Native",
    description:
      "We speak Drydock's wire protocol (portwing/1.0 over WebSocket in edge mode; dd:* SSE events in standard mode) with full watcher-snapshot streaming, letting one Drydock controller manage containers across every host we're deployed on.",
    category: "control",
  },
  {
    icon: "plugs-connected",
    title: "Standard & Edge Modes",
    description:
      "Run in standard mode behind a TLS reverse proxy (the controller dials in), or use production-supported outbound edge mode for hosts with no inbound port. The stable portwing/1.0 path is gated by real multi-agent reconnect, exec, log-stream, and backpressure soak tests.",
    category: "control",
  },
  {
    icon: "link",
    title: "Generic REST Adapter",
    description:
      "Don't run Drydock? We work standalone too. Our REST + Bearer-auth surface lets any client list, inspect, start, stop, and stream logs from containers. No controller required.",
    category: "control",
  },
  {
    icon: "cpu",
    title: "Read-Only MCP Server",
    description:
      "We ship a built-in Model Context Protocol server that exposes host and container state to MCP-aware agents. Read-only by design, so your assistant can see everything and change nothing.",
    category: "control",
  },
  {
    icon: "chart-bar",
    title: "Prometheus Metrics",
    description:
      "Our zero-dependency `/metrics` endpoint exports host/container telemetry plus edge connection, reconnect, backpressure, and audit health. Explicit `/health` and `/ready` probes keep dependency outages from becoming restart loops.",
    category: "operations",
  },
  {
    icon: "scroll",
    title: "Structured Audit Log",
    description:
      "Every action is recorded with a stable structured schema: who asked, what ran, and what the daemon answered. A cursor-based NDJSON path detects overwritten history and restarts before exporters ship records to immutable storage.",
    category: "operations",
  },
  {
    icon: "package",
    title: "Tiny & Multi-Arch",
    description:
      "We ship as a single static Go binary, ~10 MB, with zero runtime dependencies. amd64, arm64, arm/v7 — runs on the Raspberry Pi at the edge as happily as the server in the rack.",
    category: "operations",
  },
  {
    icon: "file-code",
    title: "OpenAPI 3.1 Spec",
    description:
      "We publish the full agent API as a machine-readable OpenAPI 3.1 spec. Generate clients, drive contract tests, and never guess at the wire format.",
    category: "operations",
  },
  {
    icon: "arrows-clockwise",
    title: "Watchtower Migration",
    description:
      "Coming from Watchtower? We wrote a migration guide that maps the update-on-a-host workflow onto Portwing + Drydock, so you trade a cron-like updater for an auditable, controllable agent.",
    category: "operations",
  },
];
