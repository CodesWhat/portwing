import { Activity, Bot, KeyRound, Network, PackageCheck, ShieldCheck } from "lucide-react";
import type { ComparisonRouteRawConfig } from "@/lib/comparison-route-data/types";

export const arcaneComparisonRouteData = {
  slug: "arcane",
  comparisonTable: `
Remote Docker control|Yes (Arcane Agent)|Yes (Portwing agent)|tie
Connection modes|Direct; edge over gRPC/WebSocket; polling option|Standard HTTP/S; persistent edge WebSocket|competitor
Agent authentication|Environment token; optional or required auto-enrolled mTLS|Ed25519 per-request HTTP signatures; signed edge hello|tie
Transparent Docker API|No (Arcane-specific API)|Yes|self
Docker socket hardening|Optional Tecnativa category-level proxy|Recommended Sockguard path-and-method policy|self
Audit location|Controller activities and security audit events|Structured records at the agent mediation point|tie
Release verification|Cosign-verifiable artifacts and images|Cosign + CycloneDX SBOM + SLSA provenance|tie
Fleet UI, RBAC, GitOps, scans, and Swarm|Yes|Owned by Drydock; Swarm is an agent non-goal|competitor
Prometheus agent scrape endpoint|Not documented|Yes|self
MCP server (read-only)|Not documented|Yes|self
License|BSD-3-Clause|AGPL-3.0|tie
`,
  highlightsTable: `
keyround|Different Strong Auth Models|Arcane can automatically enroll and renew mTLS certificates. Portwing signs each standard-mode HTTP request and the edge hello with Ed25519. Both are strong, but they solve credential lifecycle differently.
shieldcheck|Narrower Socket Policy|Arcane documents an optional Tecnativa proxy using broad Docker API categories. Portwing's hardened deployment uses Sockguard rules scoped by HTTP method and path.
network|Transparent Docker Compatibility|Portwing preserves Docker Engine request and response formats for compatible clients. Arcane exposes its own controller API and UI workflows.
activity|Mediation-Point Audit|Portwing records API, authentication, enrollment, Compose, and exec activity at the host agent and supports cursor-based export. Arcane records controller activities and security events.
packagecheck|Both Publish Verifiable Artifacts|Arcane documents Cosign verification for release artifacts and images. Portwing adds a CycloneDX SBOM and SLSA provenance to its signed release set.
bot|Read-Only MCP|Portwing exposes five read-only host and container inspection tools over MCP. Arcane does not document an MCP endpoint in the reviewed v2.5 material.
`,
  highlightIconMap: {
    keyround: KeyRound,
    shieldcheck: ShieldCheck,
    network: Network,
    activity: Activity,
    packagecheck: PackageCheck,
    bot: Bot,
  },
  metadataTitle: "Arcane Agent vs Portwing — Remote Docker Agent Comparison",
  metadataDescription:
    "Compare Arcane Agent v2.5 and Portwing v0.8.1 across edge transport, mTLS and Ed25519 auth, Docker API compatibility, socket policy, auditing, and fleet scope.",
  metadataKeywords: [
    "arcane agent vs portwing",
    "arcane docker alternative",
    "remote docker agent comparison",
    "docker edge agent mtls",
    "docker agent ed25519 auth",
    "docker socket proxy",
  ],
  openGraphDescription:
    "Arcane and Portwing both manage remote Docker hosts. Compare Arcane's polling and mTLS with Portwing's signed requests, transparent API, Sockguard policy, audit export, and MCP.",
  twitterDescription:
    "Arcane Agent v2.5 vs Portwing v0.8.1: transport, authentication, socket policy, audit, and product scope.",
  competitorName: "Arcane",
  heroTitle: "Arcane vs Portwing",
  heroDescription: (
    <p>
      Arcane v2.5 is a broad Docker management platform with direct and edge agents, polling,
      automated mTLS, RBAC, GitOps, scanning, and Swarm. Portwing v0.8.1 is a narrower access agent
      for Drydock, focused on{" "}
      <strong className="text-neutral-900 dark:text-neutral-200">
        replay-resistant signed requests, transparent Docker compatibility, Sockguard policy, and
        mediation-point audit
      </strong>
      . Reviewed July 28, 2026.
    </p>
  ),
  migrationTitle: "Evaluating Arcane Agent?",
  migrationDescription:
    "Choose the controller first. Arcane Agent belongs to Arcane's integrated UI and fleet model; Portwing belongs to Drydock and can also expose a generic REST or transparent Docker API. If moving to Portwing, use the matching Sockguard preset and Ed25519 keys, then validate every required Arcane workflow against Drydock rather than assuming controller-level parity.",
  jsonLdName: "Arcane Agent vs Portwing — Remote Docker Agent Comparison",
  jsonLdDescription:
    "Evidence-backed comparison of Arcane Agent v2.5 and Portwing v0.8.1 for remote Docker access.",
} satisfies ComparisonRouteRawConfig;
