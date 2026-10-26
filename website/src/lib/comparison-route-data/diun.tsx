// SPDX-License-Identifier: MIT

import { Activity, Bot, FileText, Key, PackageCheck, Shield } from "lucide-react";
import type { ComparisonRouteRawConfig } from "@/lib/comparison-route-data/types";

export const diunComparisonRouteData = {
  slug: "diun",
  comparisonTable: `
Image update notifications|Yes (multi-registry polling + 17 notification backends plus Apprise)|No (Drydock notifies; Portwing is the access agent)|competitor
Remote Docker API proxy|No, an image-discovery and notification client|Yes (full Docker API proxy with auth)|self
Provides an authenticated Docker management endpoint|No; Docker provider supports remote endpoints and TLS|Yes, with Ed25519 signing or configured shared secret|self
Management request audit|Notification records; no management proxy|Structured request records and cursor export|tie
Release artifact verification|SPDX SBOMs and SLSA provenance statements; verified GitHub release attestation|Cosign signatures + CycloneDX SBOM + SLSA provenance|tie
Default-deny socket filter|No|Yes (with sockguard)|self
Prometheus metrics|Yes (opt-in)|Yes|tie
Documented read-only MCP endpoint|Not documented|Yes|self
Edge / NAT outbound tunnel|No|Yes (Drydock v1.6.0-rc.11+)|self
Single lightweight Go binary|Yes|Yes|tie
License|MIT|MIT|tie
`,
  highlightsTable: `
key|Remote Auth (Ed25519)|Diun discovers images through providers, including a configurable remote Docker endpoint with TLS settings. It does not expose a management proxy. Portwing provides that proxy with request-bound Ed25519 signing and revocable client keys when configured.
shield|Default-Deny Socket Filter|Portwing pairs with Sockguard to constrain Docker API calls at the socket layer. Diun can discover images through several providers, so its exact Docker socket exposure depends on deployment.
filetext|Structured Audit Log|Portwing emits structured request audit records with cursor-based NDJSON export. Diun records discovery and notification activity, a different surface from management request auditing.
packagecheck|Both Publish Release Evidence|Diun v4.33.1 publishes SPDX SBOMs and SLSA provenance statements for archives and images, and its GitHub immutable-release attestation verified successfully. Portwing publishes Cosign signatures, CycloneDX SBOMs, and SLSA Build L2 provenance. A release attestation is a release/digest binding, not a build assurance level.
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
      Diun v4.33.1 (Docker Image Update Notifier) polls container registries for new tags and fires
      notifications through 17 built-in backends plus Apprise. It is a monitoring/notification
      agent, not a remote Docker control agent — an adjacent product, not a direct general-purpose
      agent peer. Portwing is a{" "}
      <strong className="text-neutral-900 dark:text-neutral-200">remote access agent</strong> — it
      gives the Drydock controller a secure authenticated foothold on a Docker host. These tools are
      complementary: run Diun for registry monitoring and Portwing for remote Docker control from
      Drydock. Reviewed October 26, 2026.
    </p>
  ),
  migrationTitle: "Using Diun today?",
  migrationDescription:
    "Use Diun for registry polling and notifications and Portwing for Drydock's remote Docker access. Diun's connection depends on its provider; Portwing's recommended path goes through Sockguard. Configure and test the access each deployment needs.",
  jsonLdName: "Diun vs Portwing — Docker Image Monitoring vs Remote Agent Comparison",
  jsonLdDescription:
    "Compare Diun (Docker image update notifier) and Portwing (remote Docker access agent). Complementary tools for different problems.",
} satisfies ComparisonRouteRawConfig;
