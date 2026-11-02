// SPDX-License-Identifier: MIT

import type { ComparisonRouteRawConfig } from "@/lib/comparison-route-data/types";
import { SITE_CONFIG } from "@/lib/site-config";

export const arcaneComparisonRouteData = {
  slug: "arcane",
  comparisonTable: `
Remote Docker control|Yes (Arcane Agent)|Yes (Portwing agent)|tie
Connection modes|Direct; persistent edge; poll-managed on-demand tunnel|Standard HTTP/S; persistent edge WebSocket|competitor
Agent authentication|Environment credential; optional auto-enrolled mTLS with ML-DSA-87 certificates from new edge CAs|Request-bound Ed25519 HTTP signatures or configured shared secret; signed edge hello; classical signatures|tie
Transparent Docker API|No (Arcane-specific API)|Yes|self
Docker socket hardening|Recommended wollomatic method/path proxy for the manager; raw-socket remote-agent examples|Recommended Sockguard method/path presets for the agent|tie
Audit location|Agent/controller events; OTLP logs and traces|Structured agent request audit with cursor-based NDJSON export|tie
Release verification|Verified Cosign images/checksums; SPDX SBOMs and build provenance|Cosign + CycloneDX SBOM + SLSA Build L2 provenance|tie
Fleet UI, RBAC, GitOps, scans, and Swarm|Yes|Owned by Drydock; Swarm is an agent non-goal|competitor
Prometheus agent scrape endpoint|Yes, opt-in exporter; also OTLP|Yes|tie
Documented read-only MCP endpoint|Not documented|Yes|self
License|BSD-3-Clause|MIT|tie
`,
  highlightsTable: `
keyround|Different Authentication Options|Arcane offers opt-in edge mTLS with ML-DSA-87 certificates from newly generated CAs. Existing P-384 CAs remain classical, and the environment credential remains part of the edge flow. This does not establish post-quantum key exchange. Portwing uses classical Ed25519 for request-bound HTTP signatures and its signed edge hello; standard mode can also use configured shared-secret authentication.
shieldcheck|Compare the Actual Socket Policy|Arcane recommends a wollomatic method/path allowlist for its manager, including builds and Swarm. Its remote-agent examples mount the raw socket. Portwing recommends Sockguard presets scoped to the agent's needs, with builds denied by default and explicit opt-in build presets.
network|Transparent Docker Compatibility|Portwing preserves Docker Engine request and response formats for compatible clients. Arcane exposes its own controller API and UI workflows.
activity|Mediation-Point Audit|Portwing records API, authentication, enrollment, Compose, and exec activity at the host agent and supports cursor-based export. Arcane persists agent and controller events and can export logs and traces through OTLP.
packagecheck|Both Publish Verifiable Artifacts|Arcane image signatures and signed checksums verified successfully; its images also carry SPDX SBOMs and BuildKit/GitHub provenance statements. Portwing publishes Cosign signatures, CycloneDX SBOMs, and SLSA Build L2 provenance. SBOM format is a difference, not evidence that Arcane lacks provenance.
bot|Read-Only MCP|Portwing exposes five read-only host and container inspection tools over MCP. Arcane does not document an MCP endpoint in the reviewed v2.17.1 material.
`,
  highlightIconMap: {
    keyround: "key",
    shieldcheck: "shield-check",
    network: "plugs-connected",
    activity: "heartbeat",
    packagecheck: "package",
    bot: "cpu",
  },
  metadataTitle: "Arcane Agent vs Portwing — Remote Docker Agent Comparison",
  metadataDescription: `Compare Arcane Agent v2.17.1 and Portwing v${SITE_CONFIG.version} across edge transport, ML-DSA-87 mTLS and Ed25519 auth, Docker API compatibility, socket policy, auditing, and fleet scope.`,
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
  twitterDescription: `Arcane Agent v2.17.1 vs Portwing v${SITE_CONFIG.version}: transport, authentication, socket policy, audit, and product scope.`,
  competitorName: "Arcane",
  heroTitle: "Arcane vs Portwing",
  heroDescription: (
    <p>
      Arcane v2.17.1 is a broad Docker management platform with direct and edge agents, poll-managed
      tunnels, RBAC, GitOps, scanning, Swarm, and opt-in edge mTLS that issues ML-DSA-87
      post-quantum certificates from a freshly generated CA. An existing ECDSA P-384 CA keeps
      issuing P-384 client certificates. Portwing v{SITE_CONFIG.version} is a narrower access agent
      for Drydock, focused on{" "}
      <strong>
        replay-resistant signed requests, transparent Docker compatibility, Sockguard policy, and
        mediation-point audit
      </strong>
      . Reviewed October 26, 2026.
    </p>
  ),
  migrationTitle: "Evaluating Arcane Agent?",
  migrationDescription:
    "Choose the controller first. Arcane Agent belongs to Arcane's integrated UI and fleet model; Portwing belongs to Drydock and can also expose a generic REST or transparent Docker API. If moving to Portwing, use the matching Sockguard preset and Ed25519 keys, then validate every required Arcane workflow against Drydock rather than assuming controller-level parity.",
  jsonLdName: "Arcane Agent vs Portwing — Remote Docker Agent Comparison",
  jsonLdDescription: `Evidence-backed comparison of Arcane Agent v2.17.1 and Portwing v${SITE_CONFIG.version} for remote Docker access.`,
} satisfies ComparisonRouteRawConfig;
