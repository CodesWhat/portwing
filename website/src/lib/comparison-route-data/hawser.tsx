import { Activity, Bot, FileText, Globe, Key, PackageCheck } from "lucide-react";
import type { ComparisonRouteRawConfig } from "@/lib/comparison-route-data/types";

export const hawserComparisonRouteData = {
  slug: "hawser",
  comparisonTable: `
Remote container control|Yes (Dockhand agent)|Yes (Drydock agent)|tie
Compatible controller|Dockhand only|Drydock (+ standalone mode)|self
Auth model|Shared secret|Ed25519 per-request signing|self
Structured audit log|No|Yes (JSON, built-in)|self
Signed images + SBOM + provenance|No|Yes (cosign + SBOM + SLSA)|self
Default-deny socket filter|No|Yes (with sockguard)|self
Prometheus metrics|No|Yes|self
MCP server (AI-native, read-only)|No|Yes|self
Edge / NAT outbound tunnel|Yes (Dockhand edge)|Yes (Drydock 1.5+, early access)|tie
Single lightweight Go binary|Yes|Yes (~10 MB)|tie
License|MIT|AGPL-3.0|tie
`,
  highlightsTable: `
key|Ed25519 Per-Request Auth|Hawser uses a shared secret passed per-request. Portwing uses Ed25519 asymmetric signing — no secret on the wire, revocable per-client keys, and clock-independent verification.
globe|Standalone Mode|Portwing runs in standalone mode without a Drydock controller, exposing Docker API endpoints for any compatible client. Hawser requires the Dockhand controller.
filetext|Structured Audit Log|Portwing writes structured JSON for every proxied Docker API call; immutable external storage provides tamper evidence. Hawser has no built-in audit trail.
packagecheck|Signed Images + SBOM|Every Portwing release ships a CycloneDX SBOM, cosign image signatures, and SLSA build provenance. Hawser publishes no supply-chain artifacts.
activity|Prometheus Metrics|Portwing exposes agent health, request counts, and latency histograms. Hawser has no metrics endpoint.
bot|MCP Server (AI-Native)|Portwing ships a read-only MCP server for AI tool integration. Hawser has no MCP support.
`,
  highlightIconMap: {
    key: Key,
    globe: Globe,
    filetext: FileText,
    packagecheck: PackageCheck,
    activity: Activity,
    bot: Bot,
  },
  metadataTitle: "Hawser vs Portwing — Remote Docker Agent Comparison",
  metadataDescription:
    "Compare Hawser (Dockhand agent) and Portwing. Both are lightweight Go remote Docker agents with outbound edge tunnels. See how Portwing adds Ed25519 auth, structured audit logging, signed images, and an MCP server.",
  metadataKeywords: [
    "hawser vs portwing",
    "hawser alternative",
    "dockhand agent alternative",
    "remote docker agent comparison",
    "docker agent ed25519 auth",
    "docker agent audit log",
    "docker agent outbound tunnel",
  ],
  openGraphDescription:
    "Hawser and Portwing are both lightweight Go remote Docker agents with outbound edge modes. See how Portwing adds Ed25519 auth, structured audit logging, signed images, and an MCP server.",
  twitterDescription:
    "Compare Hawser (Dockhand agent) and Portwing. Portwing: Ed25519, audit log, sockguard, MCP server.",
  competitorName: "Hawser",
  heroTitle: "Hawser vs Portwing",
  heroDescription: (
    <p>
      Hawser is the remote Docker agent for the Dockhand controller — the closest peer to Portwing
      in scope. Both are lightweight Go binaries, both support an outbound edge tunnel for
      NAT/firewalled hosts. Portwing adds{" "}
      <strong className="text-neutral-900 dark:text-neutral-200">
        Ed25519 per-request signing, sockguard integration, structured audit logging, and an MCP
        server
      </strong>
      . Hawser ships today as a more mature option; Portwing is alpha (v0.7.x).
    </p>
  ),
  migrationTitle: "Coming from Hawser?",
  migrationDescription:
    "Hawser and Portwing are near-peers in scope. Map your Hawser shared secret to Portwing's TOKEN_HASH, swap the image, and point Drydock at the agent URL. Add sockguard alongside for default-deny socket filtering, then upgrade to Ed25519 key pairs at your own pace for the strongest auth posture.",
  jsonLdName: "Hawser vs Portwing — Remote Docker Agent Comparison",
  jsonLdDescription:
    "Compare Hawser and Portwing for remote Docker access, auth model, audit logging, and supply-chain security.",
} satisfies ComparisonRouteRawConfig;
