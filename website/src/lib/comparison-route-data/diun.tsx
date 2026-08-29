import { Activity, Bot, FileText, Key, PackageCheck, Shield } from "lucide-react";
import type { ComparisonRouteRawConfig } from "@/lib/comparison-route-data/types";

export const diunComparisonRouteData = {
  slug: "diun",
  comparisonTable: `
Image update notifications|Yes (multi-registry polling + 20+ notifiers)|No (Drydock notifies; Portwing is the access agent)|competitor
Remote Docker API proxy|No (monitoring only, no remote control)|Yes (full Docker API proxy with auth)|self
Auth for remote access|No (local only)|Yes (Ed25519 per-request signing)|self
Structured audit log|No|Yes (JSON, built-in)|self
Release artifact verification|Not evaluated|Cosign signatures + CycloneDX SBOM + SLSA provenance|self
Default-deny socket filter|No|Yes (with sockguard)|self
Prometheus metrics|Yes|Yes|tie
MCP server (AI-native, read-only)|No|Yes|self
Edge / NAT outbound tunnel|No|Yes (Drydock v1.6.0-rc.11+)|self
Single lightweight Go binary|Yes|Yes (~10 MB)|tie
License|MIT|AGPL-3.0|tie
`,
  highlightsTable: `
key|Remote Auth (Ed25519)|Diun has no remote access model — it runs locally and pushes notifications outward. Portwing exposes the Docker API over authenticated HTTP with Ed25519 per-request signing so each client gets a revocable key pair.
shield|Default-Deny Socket Filter|Portwing pairs with Sockguard to constrain Docker API calls at the socket layer. Diun can discover images through several providers, so its exact Docker socket exposure depends on deployment.
filetext|Structured Audit Log|Portwing logs every Docker API call it proxies as structured JSON for export to immutable storage. Diun has no audit trail beyond its own notification records.
packagecheck|Portwing Release Evidence|Portwing's own releases ship Cosign signatures, CycloneDX SBOMs, and SLSA provenance. This verifies Portwing artifacts; workload image policy remains a controller or admission-control responsibility.
bot|MCP Server (AI-Native)|Portwing ships five read-only MCP tools for container and host inspection. Diun has no documented MCP support.
activity|Complementary, Not Competing|Diun and Portwing solve different problems and run happily side-by-side. Diun monitors registries and notifies; Portwing gives Drydock secure remote control. You likely want both.
`,
  highlightIconMap: {
    key: Key,
    shield: Shield,
    filetext: FileText,
    packagecheck: PackageCheck,
    bot: Bot,
    activity: Activity,
  },
  metadataTitle: "Diun vs Portwing — Docker Image Monitoring vs Remote Agent Comparison",
  metadataDescription:
    "Diun monitors registries for new image tags and sends notifications. Portwing is a secure remote access agent for Drydock. Different tools, complementary purposes — understand when you need each.",
  metadataKeywords: [
    "diun vs portwing",
    "diun alternative",
    "docker image monitoring vs remote agent",
    "diun drydock comparison",
    "docker update notification agent",
    "remote docker agent golang",
    "diun portwing comparison",
  ],
  openGraphDescription:
    "Diun monitors registries and sends notifications. Portwing gives Drydock secure remote Docker access. Different tools that complement each other — here's the comparison.",
  twitterDescription:
    "Diun watches for new image tags. Portwing gives Drydock secure remote access. Different jobs — here's when you need each.",
  competitorName: "Diun",
  heroTitle: "Diun vs Portwing",
  heroDescription: (
    <p>
      Diun (Docker Image Update Notifier) polls container registries for new tags and fires
      notifications to 20+ channels. It is a monitoring/notification agent, not a remote Docker
      control agent — an adjacent product, not a direct general-purpose agent peer. Portwing is a{" "}
      <strong className="text-neutral-900 dark:text-neutral-200">remote access agent</strong> — it
      gives the Drydock controller a secure authenticated foothold on a Docker host. These tools are
      complementary: run Diun for registry monitoring and Portwing for remote Docker control from
      Drydock.
    </p>
  ),
  migrationTitle: "Using Diun today?",
  migrationDescription:
    "Diun and Portwing are designed to coexist. Deploy Portwing on any host where you want Drydock to have remote access; keep Diun running alongside it for registry polling and notifications. They mount the same Docker socket independently and don't conflict.",
  jsonLdName: "Diun vs Portwing — Docker Image Monitoring vs Remote Agent Comparison",
  jsonLdDescription:
    "Compare Diun (Docker image update notifier) and Portwing (remote Docker access agent). Complementary tools for different problems.",
} satisfies ComparisonRouteRawConfig;
