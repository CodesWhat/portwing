// SPDX-License-Identifier: MIT

import { Activity, Bot, FileText, KeyRound, Network, ShieldCheck } from "lucide-react";
import type { ComparisonRouteRawConfig } from "@/lib/comparison-route-data/types";
import { SITE_CONFIG } from "@/lib/site-config";

export const portainerComparisonRouteData = {
  slug: "portainer",
  comparisonTable: `
Remote Docker API proxy|Yes (Agent)|Yes|tie
Connection modes|Classic inbound; outbound Edge; Async Edge (Business Edition)|Standard inbound; persistent outbound edge (Drydock v1.6.0-rc.11+)|competitor
Agent authentication|Non-FIPS controller public key and fixed-message signature headers; optional shared secret; Edge credentials and Business mTLS|Ed25519 per-request HTTP signatures; signed edge hello; token fallback in standard mode|tie
Docker socket policy|Documented agent mounts the Docker socket; host-root mount is optional|Recommended Sockguard path-and-method policy|self
Structured audit|Controller activity log in Business Edition|Agent-level structured audit and cursor export in every build|self
Supply-chain evidence|2.45.3 controller archives have checksums; Agent images have SPDX SBOMs and SLSA v1 provenance; no Cosign signature found in checked discovery paths|Cosign signatures + archive/image CycloneDX SBOMs + SLSA provenance|self
Fleet agent upgrades and policies|Business Edition beta update/rollback for Edge Agents on Docker Standalone environments|Not shipped; belongs in Drydock plus packaging|competitor
Host file APIs and Swarm aggregation|Yes|Intentional non-goals|competitor
Prometheus agent scrape endpoint|Local Kubernetes Edge metrics; equivalent Docker mediation scrape not documented|Yes|tie
Documented read-only MCP endpoint|Not documented for the agent; separate unsupported controller MCP server|Yes|self
License|Zlib agent; proprietary Business features|MIT|tie
`,
  highlightsTable: `
network|Portainer Fleet Operations|Portainer offers Edge stacks, jobs and configuration, plus Business Edition Async Edge. Its built-in agent update/rollback feature is Business Edition beta for Edge Agents on Docker Standalone environments. Portwing provides a persistent edge tunnel; Drydock owns fleet rollout policy.
keyround|Different Authentication Layers|Portainer's non-FIPS Agent verifies signature headers on requests, but the signed value is a fixed message or AGENT_SECRET. Portwing's configured Ed25519 path binds method, exact target, body hash, timestamp and nonce, and signs its edge hello. Portainer also offers Edge credentials and optional Business mTLS; these are different mechanisms, not an overall security ranking.
shieldcheck|Narrow Socket Boundary|Portainer's agent mounts the Docker socket, with optional host-root mounting for host management. Portwing's hardened path puts Sockguard in between and permits only configured HTTP methods and Docker API paths.
activity|Audit at the Agent|Portainer's controller Activity log is a Business feature. Portwing emits structured authentication and Docker mediation records in every build; Drydock can add user-level context above them.
filetext|Host Access Is a Tradeoff|Portainer exposes file-browse APIs and Swarm-wide resource aggregation. Portwing intentionally avoids arbitrary host files and controls one Docker host to keep the privileged surface smaller.
bot|Prometheus and Read-Only MCP|Portwing exposes Docker mediation metrics and five read-only MCP inspection tools. Portainer has a localhost-only Prometheus endpoint populated for Kubernetes Edge. Equivalent Docker mediation scraping and an MCP endpoint were not documented in the reviewed material. Portainer's separate portainer/portainer-mcp server targets the controller API and is marked unsupported for production.
`,
  highlightIconMap: {
    network: Network,
    keyround: KeyRound,
    shieldcheck: ShieldCheck,
    activity: Activity,
    filetext: FileText,
    bot: Bot,
  },
  metadataTitle: "Portainer Agent vs Portwing — Remote Docker Agent Comparison",
  metadataDescription: `Compare Portainer 2.45.3 Agent/Edge Agent and Portwing v${SITE_CONFIG.version} across transport, authentication, socket policy, audit, fleet upgrades, host access, metrics, and MCP.`,
  metadataKeywords: [
    "portainer agent vs portwing",
    "portainer alternative",
    "portainer agent replacement",
    "remote docker agent comparison",
    "docker edge agent",
    "docker agent ed25519 auth",
  ],
  openGraphDescription:
    "Portainer offers fleet and Business Async Edge operations; Portwing focuses on signed requests, narrow socket policy, agent audit export, Prometheus, and MCP.",
  twitterDescription: `Portainer 2.45.3 Agent vs Portwing v${SITE_CONFIG.version}: an evidence-backed comparison of transport, security, fleet operations, and scope.`,
  competitorName: "Portainer",
  heroTitle: "Portainer vs Portwing",
  heroDescription: (
    <p>
      Portainer 2.45.3 offers classic and Edge agents, Business Edition Async Edge, and Swarm. Its
      built-in agent update/rollback is Business Edition beta for Edge Agents on Docker Standalone
      environments. Portwing v{SITE_CONFIG.version} is a narrower Drydock agent focused on{" "}
      <strong className="text-neutral-900 dark:text-neutral-200">
        signed requests, replay defense, Sockguard policy, agent-level audit, Prometheus, and MCP
      </strong>{" "}
      in the MIT-licensed build. Choose the controller workflows and supported platforms that fit
      your deployment. Reviewed October 26, 2026.
    </p>
  ),
  migrationTitle: "Coming from Portainer Agent?",
  migrationDescription:
    "Inventory Edge Stacks, Jobs, Configurations, RBAC, Swarm, host browsing, and agent update policies before moving controllers. Map those responsibilities to Drydock, then deploy Portwing with Ed25519 keys and the narrowest Sockguard preset that permits the workflows you actually need.",
  jsonLdName: "Portainer Agent vs Portwing — Remote Docker Agent Comparison",
  jsonLdDescription: `Evidence-backed comparison of Portainer 2.45.3 Agent/Edge Agent and Portwing v${SITE_CONFIG.version}.`,
} satisfies ComparisonRouteRawConfig;
