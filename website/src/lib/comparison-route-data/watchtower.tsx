import { Activity, Bot, FileText, Key, PackageCheck, Shield } from "lucide-react";
import type { ComparisonRouteRawConfig } from "@/lib/comparison-route-data/types";

export const watchtowerComparisonRouteData = {
  slug: "watchtower",
  comparisonTable: `
Auto-update containers|Yes (pull + restart on schedule)|No (Drydock handles updates; Portwing is the access agent)|competitor
Remote Docker API proxy|No (local/scheduled, no remote protocol)|Yes (full Docker API proxy with auth)|self
Auth for remote access|No (local only)|Yes (Ed25519 per-request signing)|self
Structured audit log|No|Yes (JSON, built-in)|self
Image signature verification|Basic cosign support (newer builds)|Yes (cosign + SBOM + SLSA)|self
Default-deny socket filter|No|Yes (with sockguard)|self
Prometheus metrics|No|Yes|self
MCP server (AI-native, read-only)|No|Yes|self
Edge / NAT outbound tunnel|No|Yes (Drydock 1.5+, early access)|self
Single lightweight Go binary|Yes|Yes (~10 MB)|tie
License|Apache-2.0|AGPL-3.0|tie
`,
  highlightsTable: `
key|Remote Auth (Ed25519)|Watchtower runs locally and has no remote access model. Portwing exposes the Docker API over authenticated HTTP, using Ed25519 per-request signing so each client has its own revocable key pair.
shield|Default-Deny Socket Filter|Portwing pairs with sockguard to filter Docker API calls at the socket level. Even if Portwing is compromised, the sockguard allowlist constrains what can be called. Watchtower mounts the socket unfiltered.
filetext|Structured Audit Log|Portwing logs every Docker API call it proxies as structured JSON for export to immutable storage. Watchtower has no audit trail.
packagecheck|Supply-Chain Artifacts|Every Portwing release ships a CycloneDX SBOM, cosign image signatures, and SLSA build provenance. Watchtower publishes no supply-chain artifacts.
activity|Prometheus Metrics|Portwing exposes agent health, request counts, and latency histograms. Watchtower has no metrics endpoint.
bot|MCP Server (AI-Native)|Portwing ships a read-only MCP server so AI tools can inspect containers and events. Watchtower has no MCP support.
`,
  highlightIconMap: {
    key: Key,
    shield: Shield,
    filetext: FileText,
    packagecheck: PackageCheck,
    activity: Activity,
    bot: Bot,
  },
  metadataTitle: "Watchtower vs Portwing — Docker Agent Comparison",
  metadataDescription:
    "Watchtower auto-updates containers on a schedule. Portwing is a remote access agent for Drydock. Different tools, different problems — understand which one you actually need.",
  metadataKeywords: [
    "watchtower vs portwing",
    "watchtower alternative",
    "docker auto-update vs remote agent",
    "watchtower remote access",
    "docker container update agent",
    "drydock remote agent",
    "watchtower drydock comparison",
  ],
  openGraphDescription:
    "Watchtower auto-updates containers. Portwing is a secure remote access agent for Drydock. Different tools — here's when you need each one.",
  twitterDescription:
    "Watchtower auto-updates containers. Portwing gives Drydock secure remote access. Different jobs — here's the comparison.",
  competitorName: "Watchtower",
  heroTitle: "Watchtower vs Portwing",
  heroDescription: (
    <p>
      Watchtower is an automated container updater — it polls registries and restarts containers
      when new images are available. Portwing is a{" "}
      <strong className="text-neutral-900 dark:text-neutral-200">remote access agent</strong> — it
      gives the Drydock controller a secure authenticated foothold on a Docker host. These tools
      solve different problems; Drydock can handle the update automation that Watchtower provides,
      with Portwing as the agent on each host.
    </p>
  ),
  migrationTitle: "Using Watchtower today?",
  migrationDescription:
    "If you want centralized update management across multiple hosts, Drydock orchestrates image updates and Portwing gives it the agent foothold on each host. You can run both — Watchtower for local hosts where Drydock doesn't need remote access, and Portwing where you need the full remote management API.",
  jsonLdName: "Watchtower vs Portwing — Docker Agent Comparison",
  jsonLdDescription:
    "Compare Watchtower (container auto-updater) and Portwing (remote Docker access agent). Different tools for different problems.",
} satisfies ComparisonRouteRawConfig;
