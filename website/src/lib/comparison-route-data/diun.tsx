import { Activity, Bot, FileText, Key, PackageCheck, Shield } from "lucide-react";
import type { ComparisonRouteRawConfig } from "@/lib/comparison-route-data/types";

export const diunComparisonRouteData = {
  slug: "diun",
  comparisonTable: `
Image update notifications|Yes (multi-registry polling + 20+ notifiers)|No (Drydock notifies; Portwing is the access agent)|competitor
Remote Docker API proxy|No (monitoring only, no remote control)|Yes (full Docker API proxy with auth)|self
Auth for remote access|No (local only)|Yes (Ed25519 per-request signing)|self
Structured audit log|No|Yes (tamper-evident, built-in)|self
Image signature verification|No|Yes (cosign + SBOM + SLSA)|self
Default-deny socket filter|No|Yes (with sockguard)|self
Prometheus metrics|Yes|Yes|tie
MCP server (AI-native, read-only)|No|Yes|self
Edge / NAT outbound tunnel|No|Yes (Drydock 1.5+, early access)|self
Single lightweight Go binary|Yes|Yes (~10 MB)|tie
License|MIT|AGPL-3.0|tie
`,
  highlightsTable: `
key|Remote Auth (Ed25519)|Diun has no remote access model — it runs locally and pushes notifications outward. Portwing exposes the Docker API over authenticated HTTP with Ed25519 per-request signing so each client gets a revocable key pair.
shield|Default-Deny Socket Filter|Portwing pairs with sockguard to constrain Docker API calls at the socket level. Diun mounts the raw socket and has no filtering layer.
filetext|Structured Audit Log|Portwing logs every Docker API call it proxies in a tamper-evident JSON format. Diun has no audit trail beyond its own notification records.
packagecheck|Image Signature Verification|Portwing verifies image signatures via cosign and ships CycloneDX SBOMs and SLSA build provenance on its own releases. Diun detects new tags but does not verify image signatures.
bot|MCP Server (AI-Native)|Portwing ships a read-only MCP server so AI tools can inspect containers, images, and events. Diun has no MCP support.
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
      notifications to 20+ channels. Portwing is a{" "}
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
