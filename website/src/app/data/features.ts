import {
  Activity,
  Anchor,
  ArrowRightLeft,
  BadgeCheck,
  Bot,
  Cable,
  Feather,
  FileJson,
  Fingerprint,
  KeyRound,
  LockKeyhole,
  type LucideIcon,
  Network,
  ScrollText,
  ShieldCheck,
} from "lucide-react";

export type FeatureCategory = "security" | "control" | "operations";

interface Feature {
  icon: LucideIcon;
  title: string;
  color: string;
  bg: string;
  description: string;
  category: FeatureCategory;
}

export const features: Feature[] = [
  {
    icon: ShieldCheck,
    title: "Default-Deny Socket",
    color: "text-rose-500 dark:text-rose-400",
    bg: "bg-rose-100 dark:bg-rose-900/50",
    description:
      "Portwing never has to touch the raw Docker socket. Bundle sibling project sockguard and the agent talks to a filtered unix socket instead — even a fully compromised agent is constrained to an explicit Docker API allowlist enforced at the socket level.",
    category: "security",
  },
  {
    icon: Fingerprint,
    title: "Ed25519 Per-Client Auth",
    color: "text-rose-500 dark:text-rose-400",
    bg: "bg-rose-100 dark:bg-rose-900/50",
    description:
      "Signed requests with nonce-based replay protection and no shared secrets on the wire. Enroll client public keys into an authorized_keys file, rotate with SIGHUP, and every enrollment is audited.",
    category: "security",
  },
  {
    icon: KeyRound,
    title: "Argon2id Token Hashing",
    color: "text-rose-500 dark:text-rose-400",
    bg: "bg-rose-100 dark:bg-rose-900/50",
    description:
      "Prefer a shared secret? Store only its Argon2id hash via TOKEN_HASH. Generate one with `portwing hash-token`; comparison is constant-time. Plaintext TOKEN is supported for evaluation but the hash is the recommended posture.",
    category: "security",
  },
  {
    icon: BadgeCheck,
    title: "Signed & Verifiable",
    color: "text-rose-500 dark:text-rose-400",
    bg: "bg-rose-100 dark:bg-rose-900/50",
    description:
      "Every release ships cosign signatures, an SBOM, and SLSA build provenance. Verify the image before you ever trust it with your host — the supply chain is auditable end to end.",
    category: "security",
  },
  {
    icon: LockKeyhole,
    title: "Hardened Runtime",
    color: "text-rose-500 dark:text-rose-400",
    bg: "bg-rose-100 dark:bg-rose-900/50",
    description:
      "Ships to run `read_only`, `cap_drop: ALL`, and `no-new-privileges`, with tokens delivered as mounted secrets rather than env vars. Root-by-default for socket compatibility, with an opt-in non-root UID 65532 path documented honestly.",
    category: "security",
  },
  {
    icon: Anchor,
    title: "Drydock-Native",
    color: "text-indigo-500 dark:text-indigo-400",
    bg: "bg-indigo-100 dark:bg-indigo-900/50",
    description:
      "Speaks Drydock's `dd:*` WebSocket protocol with full SSE compatibility, including watcher-snapshot streaming. Portwing is the remote agent that lets one Drydock controller manage containers across every one of your hosts.",
    category: "control",
  },
  {
    icon: Network,
    title: "Standard & Edge Modes",
    color: "text-indigo-500 dark:text-indigo-400",
    bg: "bg-indigo-100 dark:bg-indigo-900/50",
    description:
      "Run in standard mode behind a TLS reverse proxy (the controller dials in), or — for hosts with no inbound port — outbound edge mode where the agent dials the controller. Edge is implemented agent-side; the controller endpoint is in progress.",
    category: "control",
  },
  {
    icon: Cable,
    title: "Generic REST Adapter",
    color: "text-indigo-500 dark:text-indigo-400",
    bg: "bg-indigo-100 dark:bg-indigo-900/50",
    description:
      "Don't run Drydock? Use Portwing standalone. A generic REST + Bearer-auth surface lets any client list, inspect, start, stop, and stream logs from containers — no controller required.",
    category: "control",
  },
  {
    icon: Bot,
    title: "Read-Only MCP Server",
    color: "text-indigo-500 dark:text-indigo-400",
    bg: "bg-indigo-100 dark:bg-indigo-900/50",
    description:
      "A built-in Model Context Protocol server exposes host and container state to MCP-aware agents — read-only by design, so your assistant can see everything and change nothing.",
    category: "control",
  },
  {
    icon: Activity,
    title: "Prometheus Metrics",
    color: "text-amber-500 dark:text-amber-400",
    bg: "bg-amber-100 dark:bg-amber-900/50",
    description:
      "A zero-dependency `/metrics` endpoint exports container and agent telemetry in cAdvisor-compatible form, so it drops straight into the dashboards and alerts you already run.",
    category: "operations",
  },
  {
    icon: ScrollText,
    title: "Structured Audit Log",
    color: "text-amber-500 dark:text-amber-400",
    bg: "bg-amber-100 dark:bg-amber-900/50",
    description:
      "Every action the agent takes is recorded as structured, tamper-evident JSON — who asked, what ran, and what the daemon answered. Authentication and key-enrollment events are first-class.",
    category: "operations",
  },
  {
    icon: Feather,
    title: "Tiny & Multi-Arch",
    color: "text-amber-500 dark:text-amber-400",
    bg: "bg-amber-100 dark:bg-amber-900/50",
    description:
      "A single static Go binary, ~10 MB, with zero runtime dependencies. amd64 · arm64 · arm/v7 — it runs on the Raspberry Pi at the edge as happily as the server in the rack.",
    category: "operations",
  },
  {
    icon: FileJson,
    title: "OpenAPI 3.1 Spec",
    color: "text-amber-500 dark:text-amber-400",
    bg: "bg-amber-100 dark:bg-amber-900/50",
    description:
      "The full agent API is documented as a machine-readable OpenAPI 3.1 spec — generate clients, drive contract tests, and never guess at the wire format.",
    category: "operations",
  },
  {
    icon: ArrowRightLeft,
    title: "Watchtower Migration",
    color: "text-amber-500 dark:text-amber-400",
    bg: "bg-amber-100 dark:bg-amber-900/50",
    description:
      "Coming from Watchtower? A dedicated migration guide maps the update-on-a-host workflow onto Portwing + Drydock, so you trade a cron-like updater for an auditable, controllable agent.",
    category: "operations",
  },
];
