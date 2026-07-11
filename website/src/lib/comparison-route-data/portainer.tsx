import { Activity, Bot, Key, Lock, PackageCheck, Shield } from "lucide-react";
import type { ComparisonRouteRawConfig } from "@/lib/comparison-route-data/types";

export const portainerComparisonRouteData = {
  slug: "portainer",
  comparisonTable: `
Remote container control|Yes (via Portainer Agent)|Yes|tie
Full management UI|Yes (Portainer UI; RBAC requires Business)|No (Drydock controller only)|competitor
Auth model|Shared secret / Edge key|Ed25519 per-request signing|self
Structured audit log|Business tier only ($)|Yes (built-in, free, AGPL-3.0)|self
Signed images + SBOM + provenance|No|Yes (cosign + SBOM + SLSA build provenance)|self
Default-deny socket filter|No|Yes (with sockguard)|self
Prometheus metrics|Business tier only ($)|Yes|self
MCP server (AI-native, read-only)|No|Yes|self
Edge / NAT outbound tunnel|Yes (Edge Agent, mature)|Yes (Drydock 1.5+, early access)|competitor
Single lightweight binary|No (~300 MB node image)|Yes (~10 MB Go binary)|self
License|Zlib (core) / proprietary (Business)|AGPL-3.0|tie
`,
  highlightsTable: `
key|Ed25519 Per-Request Auth|Portainer agents authenticate with a shared secret or Edge key. Portwing uses Ed25519 asymmetric signing on every request — no secret on the wire, and each client gets its own key pair.
shield|Structured Audit Log (Free)|Portainer locks audit logging to its Business tier. Portwing ships tamper-evident, structured JSON audit logging in every build under AGPL-3.0 — no paid upgrade required.
packagecheck|Signed Images + SBOM|Portwing's build pipeline generates a CycloneDX SBOM, cosign image signatures, and SLSA build provenance on every release. Portainer has no supply-chain artifacts.
activity|Prometheus Metrics (Free)|Portainer exposes metrics only in its Business tier. Portwing includes a Prometheus metrics endpoint in the base build, covering agent health, request counts, and latency.
bot|MCP Server (AI-Native)|Portwing ships a read-only MCP server so AI tools can inspect containers, images, and events without a browser. Portainer has no MCP integration.
lock|Narrow Scope by Design|Portainer is a full management platform. Portwing is a security-first foothold agent — no bundled UI, no extra attack surface, just the Docker API behind auth and a sockguard socket filter.
`,
  highlightIconMap: {
    key: Key,
    shield: Shield,
    packagecheck: PackageCheck,
    activity: Activity,
    bot: Bot,
    lock: Lock,
  },
  metadataTitle: "Portainer Agent vs Portwing — Remote Docker Agent Comparison",
  metadataDescription:
    "Compare Portainer Agent and Portwing. Portainer is a full management platform — see how Portwing adds Ed25519 auth, free audit logging, signed images, and an MCP server without a paid Business tier.",
  metadataKeywords: [
    "portainer agent vs portwing",
    "portainer alternative",
    "portainer agent replacement",
    "remote docker agent comparison",
    "docker agent ed25519 auth",
    "docker agent audit log",
    "portainer business tier alternative",
  ],
  openGraphDescription:
    "Portainer locks audit logs and metrics to Business tier. See how Portwing ships Ed25519 auth, structured audit logging, signed images, and an MCP server in the free AGPL-3.0 build.",
  twitterDescription:
    "Compare Portainer Agent and Portwing for remote Docker access. Portwing: Ed25519 auth, free audit log, signed images.",
  competitorName: "Portainer",
  heroTitle: "Portainer vs Portwing",
  heroDescription: (
    <p>
      Portainer is the most widely-deployed Docker management platform, and its agent bridges remote
      hosts. Portwing is narrowly scoped — a{" "}
      <strong className="text-neutral-900 dark:text-neutral-200">
        secure foothold agent without the management overhead
      </strong>{" "}
      — and ships Ed25519 auth, audit logging, and Prometheus metrics in the free AGPL-3.0 build
      without requiring a Business-tier upgrade. Portwing is alpha (v0.6.x); Portainer is
      production-mature.
    </p>
  ),
  migrationTitle: "Coming from Portainer Agent?",
  migrationDescription:
    "If you're switching to Drydock as your controller, deploy Portwing on each host in place of the Portainer Agent. Generate a token hash, set TOKEN_HASH, and point Drydock at the agent URL. Add sockguard alongside for default-deny socket filtering, and enable Ed25519 key pairs when you're ready for the strongest auth posture.",
  jsonLdName: "Portainer Agent vs Portwing — Remote Docker Agent Comparison",
  jsonLdDescription:
    "Compare Portainer Agent and Portwing for remote Docker access, auth, audit logging, and supply-chain security.",
} satisfies ComparisonRouteRawConfig;
