import { Activity, Bot, FileText, Key, PackageCheck, Shield } from "lucide-react";
import type { ComparisonRouteRawConfig } from "@/lib/comparison-route-data/types";

export const komodoComparisonRouteData = {
  slug: "komodo",
  comparisonTable: `
Remote container control|Yes (via Periphery agent)|Yes|tie
Full orchestration UI|Yes (Git-integrated stacks, builds, deploys)|No (Drydock + Portwing only)|competitor
Auth model|Time-based passkey (server-synced)|Ed25519 per-request signing|self
Structured audit log|No|Yes (JSON, built-in)|self
Signed images + SBOM + provenance|No|Yes (cosign + SBOM + SLSA)|self
Default-deny socket filter|No|Yes (with sockguard)|self
Prometheus metrics|Partial (some system metrics)|Yes (agent request + health metrics)|self
MCP server (AI-native, read-only)|No|Yes|self
Edge / NAT outbound tunnel|No|Yes (Drydock 1.5+, early access)|self
Single lightweight Go binary|Yes|Yes (~10 MB)|tie
License|GPL-3.0|AGPL-3.0|tie
`,
  highlightsTable: `
key|Ed25519 Per-Request Auth|Komodo Periphery uses a time-based passkey that must stay in sync with the server clock. Portwing uses Ed25519 asymmetric signing on every request — clock-independent, no shared secret on the wire.
shield|Default-Deny Socket Filter|Portwing pairs with sockguard so the agent never touches the raw Docker socket directly. Even a compromised Portwing instance is constrained to the explicit API allowlist in sockguard.yaml. Komodo Periphery mounts the socket unfiltered.
filetext|Structured Audit Log|Portwing writes structured JSON for every proxied Docker API call; immutable external storage provides tamper evidence. Komodo has no built-in agent-level audit trail.
packagecheck|Signed Images + SBOM|Every Portwing release ships a CycloneDX SBOM, cosign image signatures, and SLSA build provenance. Komodo Periphery publishes none of these supply-chain artifacts.
activity|Prometheus Metrics|Portwing exposes a Prometheus metrics endpoint covering agent health, request counts, and latency. Komodo's metrics are limited to system-level data from Periphery.
bot|MCP Server (AI-Native)|Portwing ships a read-only MCP server for AI tool integration. Komodo has no MCP endpoint.
`,
  highlightIconMap: {
    key: Key,
    shield: Shield,
    filetext: FileText,
    packagecheck: PackageCheck,
    activity: Activity,
    bot: Bot,
  },
  metadataTitle: "Komodo Periphery vs Portwing — Remote Docker Agent Comparison",
  metadataDescription:
    "Compare Komodo (Periphery agent) and Portwing. Komodo is a full Git-integrated orchestration platform — see how Portwing adds Ed25519 auth, structured audit logging, signed images, and an MCP server.",
  metadataKeywords: [
    "komodo periphery vs portwing",
    "komodo alternative",
    "periphery agent alternative",
    "komodo docker agent comparison",
    "docker agent ed25519 auth",
    "docker agent audit log",
    "remote docker agent golang",
  ],
  openGraphDescription:
    "Komodo is a powerful Git-integrated orchestration platform. See how Portwing adds Ed25519 auth, structured audit logging, signed images, and an MCP server focused on secure agent access.",
  twitterDescription:
    "Compare Komodo Periphery and Portwing for remote Docker agent access. Portwing: Ed25519, audit log, sockguard.",
  competitorName: "Komodo",
  heroTitle: "Komodo vs Portwing",
  heroDescription: (
    <p>
      Komodo (formerly Mogh) is a self-hosted Git-integrated orchestration platform with Periphery
      as its remote agent. Portwing is purpose-built for Drydock&apos;s remote access use case —
      narrower in scope but stronger on{" "}
      <strong className="text-neutral-900 dark:text-neutral-200">
        auth (Ed25519), audit logging, supply-chain artifacts, and sockguard socket filtering
      </strong>
      . Komodo is production-mature; Portwing is alpha (v0.7.x).
    </p>
  ),
  migrationTitle: "Coming from Komodo Periphery?",
  migrationDescription:
    "If you're moving to Drydock as your controller, deploy Portwing on each host. Set TOKEN_HASH with a token you generate, and point Drydock at the agent URL. Portwing's Go binary is a similar size to Periphery — no runtime dependency changes. Add sockguard for default-deny socket filtering and enable Ed25519 key pairs for the strongest auth.",
  jsonLdName: "Komodo Periphery vs Portwing — Remote Docker Agent Comparison",
  jsonLdDescription:
    "Compare Komodo Periphery and Portwing for remote Docker access, auth model, audit logging, and supply-chain security.",
} satisfies ComparisonRouteRawConfig;
