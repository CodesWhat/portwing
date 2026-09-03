import { Activity, Bot, KeyRound, Network, PackageCheck, ShieldCheck } from "lucide-react";
import type { ComparisonRouteRawConfig } from "@/lib/comparison-route-data/types";
import { SITE_CONFIG } from "@/lib/site-config";

export const arcaneComparisonRouteData = {
  slug: "arcane",
  comparisonTable: `
Remote Docker control|Yes (Arcane Agent)|Yes (Portwing agent)|tie
Connection modes|Direct; edge over gRPC/WebSocket; polling option|Standard HTTP/S; persistent edge WebSocket|competitor
Agent authentication|Environment token; optional auto-enrolled mTLS, and since v2.10.0 a newly generated edge CA issues ML-DSA-87 post-quantum certificates|Ed25519 per-request HTTP signatures and signed edge hello; classical only, no post-quantum option|competitor
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
keyround|Arcane Signs Post-Quantum|Arcane v2.10.0 moved its signing surfaces, including edge mTLS, to ML-DSA-87 (FIPS 204). mTLS stays opt-in and the agent token still bootstraps enrollment, but a freshly generated edge CA is post-quantum. Portwing verifies Ed25519-signed requests in standard mode, falling back to bearer auth when a request carries no signature, and signs its edge hello with Ed25519. Both paths are classical, with no post-quantum option today.
shieldcheck|Narrower Socket Policy|Arcane documents an optional Tecnativa proxy using broad Docker API categories. Portwing's hardened deployment uses Sockguard rules scoped by HTTP method and path.
network|Transparent Docker Compatibility|Portwing preserves Docker Engine request and response formats for compatible clients. Arcane exposes its own controller API and UI workflows.
activity|Mediation-Point Audit|Portwing records API, authentication, enrollment, Compose, and exec activity at the host agent and supports cursor-based export. Arcane records controller activities and security events.
packagecheck|Both Publish Verifiable Artifacts|Arcane documents Cosign verification for release artifacts and images. Portwing adds a CycloneDX SBOM and SLSA provenance to its signed release set.
bot|Read-Only MCP|Portwing exposes five read-only host and container inspection tools over MCP. Arcane does not document an MCP endpoint in the reviewed v2.10.1 material.
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
  metadataDescription: `Compare Arcane Agent v2.10.1 and Portwing v${SITE_CONFIG.version} across edge transport, ML-DSA-87 mTLS and Ed25519 auth, Docker API compatibility, socket policy, auditing, and fleet scope.`,
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
  twitterDescription: `Arcane Agent v2.10.1 vs Portwing v${SITE_CONFIG.version}: transport, authentication, socket policy, audit, and product scope.`,
  competitorName: "Arcane",
  heroTitle: "Arcane vs Portwing",
  heroDescription: (
    <p>
      Arcane v2.10.1 is a broad Docker management platform with direct and edge agents, polling,
      RBAC, GitOps, scanning, Swarm, and opt-in edge mTLS that issues ML-DSA-87 post-quantum
      certificates from a freshly generated CA. An existing ECDSA P-384 CA keeps issuing P-384
      client certificates. Portwing v{SITE_CONFIG.version} is a narrower access agent for Drydock,
      focused on{" "}
      <strong className="text-neutral-900 dark:text-neutral-200">
        replay-resistant signed requests, transparent Docker compatibility, Sockguard policy, and
        mediation-point audit
      </strong>
      . Reviewed September 2, 2026.
    </p>
  ),
  migrationTitle: "Evaluating Arcane Agent?",
  migrationDescription:
    "Choose the controller first. Arcane Agent belongs to Arcane's integrated UI and fleet model; Portwing belongs to Drydock and can also expose a generic REST or transparent Docker API. If moving to Portwing, use the matching Sockguard preset and Ed25519 keys, then validate every required Arcane workflow against Drydock rather than assuming controller-level parity.",
  jsonLdName: "Arcane Agent vs Portwing — Remote Docker Agent Comparison",
  jsonLdDescription: `Evidence-backed comparison of Arcane Agent v2.10.1 and Portwing v${SITE_CONFIG.version} for remote Docker access.`,
} satisfies ComparisonRouteRawConfig;
