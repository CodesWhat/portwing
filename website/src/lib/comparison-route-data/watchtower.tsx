import { Activity, Bot, FileText, Key, PackageCheck, Shield } from "lucide-react";
import type { ComparisonRouteRawConfig } from "@/lib/comparison-route-data/types";

export const watchtowerComparisonRouteData = {
  slug: "watchtower",
  comparisonTable: `
Auto-update containers|Yes (pull + restart on schedule)|No (Drydock handles updates; Portwing is the access agent)|competitor
Remote Docker API proxy|No (local/scheduled, no remote protocol)|Yes (full Docker API proxy with auth)|self
Auth for remote access|No (local only)|Yes (Ed25519 per-request signing)|self
Structured audit log|No|Yes (JSON, built-in)|self
Runtime image signature verification|Basic cosign support (newer builds)|No (controller or admission-control responsibility)|competitor
Published release evidence|Archived upstream project|Cosign signatures + CycloneDX SBOM + SLSA provenance|self
Default-deny socket filter|No|Yes (with sockguard)|self
Prometheus metrics|No|Yes|self
MCP server (AI-native, read-only)|No|Yes|self
Edge / NAT outbound tunnel|No|Yes (Drydock v1.6.0-rc.11+)|self
Single lightweight Go binary|Yes|Yes (~10 MB)|tie
License|Apache-2.0|AGPL-3.0|tie
`,
  highlightsTable: `
key|Remote Auth (Ed25519)|Watchtower runs locally and has no remote access model. Portwing exposes the Docker API over authenticated HTTP, using Ed25519 per-request signing so each client has its own revocable key pair.
shield|Default-Deny Socket Filter|Portwing pairs with sockguard to filter Docker API calls at the socket level. Even if Portwing is compromised, the sockguard allowlist constrains what can be called. Watchtower mounts the socket unfiltered.
filetext|Structured Audit Log|Portwing logs every Docker API call it proxies as structured JSON for export to immutable storage. Watchtower has no audit trail.
packagecheck|Maintained Signed Releases|Watchtower's upstream repository is archived. Portwing remains actively maintained and every release ships per-archive CycloneDX SBOMs, an image SBOM attestation, cosign image signatures, and SLSA build provenance. This verifies Portwing itself; it is not workload image-signature enforcement.
activity|Prometheus Metrics|Portwing exposes agent health, request counts, and latency histograms. Watchtower has no metrics endpoint.
bot|MCP Server (AI-Native)|Portwing ships five read-only MCP tools for container and host inspection. Watchtower has no documented MCP support.
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
      when new images are available. It is a local container updater, not a remote access agent — an
      adjacent product, not a direct general-purpose agent peer — and its upstream repository is now
      archived. Portwing is a{" "}
      <strong className="text-neutral-900 dark:text-neutral-200">remote access agent</strong> — it
      gives the Drydock controller a secure authenticated foothold on a Docker host. These tools
      solve different problems; Drydock can handle centralized update workflows with Portwing as the
      agent on each host.
    </p>
  ),
  migrationTitle: "Using Watchtower today?",
  migrationDescription:
    "If you want centralized update management across multiple hosts, Drydock orchestrates image updates and Portwing gives it the agent foothold on each host. You can run both — Watchtower for local hosts where Drydock doesn't need remote access, and Portwing where you need the full remote management API.",
  jsonLdName: "Watchtower vs Portwing — Docker Agent Comparison",
  jsonLdDescription:
    "Compare Watchtower (container auto-updater) and Portwing (remote Docker access agent). Different tools for different problems.",
} satisfies ComparisonRouteRawConfig;
