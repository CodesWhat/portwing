import { Activity, Bot, FileText, KeyRound, Network, ShieldCheck } from "lucide-react";
import type { ComparisonRouteRawConfig } from "@/lib/comparison-route-data/types";
import { SITE_CONFIG } from "@/lib/site-config";

export const portainerComparisonRouteData = {
  slug: "portainer",
  comparisonTable: `
Remote Docker API proxy|Yes (Agent)|Yes|tie
Connection modes|Classic inbound; outbound Edge; Async Edge|Standard inbound; persistent outbound edge (Drydock v1.6.0-rc.11+)|competitor
Agent authentication|Claim key exchange; optional shared secret; Edge key and optional Business mTLS|Ed25519 per-request HTTP signatures; signed edge hello; token fallback in standard mode|tie
Docker socket policy|Documented agent mounts Docker socket and host paths|Recommended Sockguard path-and-method policy|self
Structured audit|Controller activity log in Business Edition|Agent-level structured audit and cursor export in every build|self
Supply-chain evidence|Not published|Cosign signatures + archive/image CycloneDX SBOMs + SLSA provenance|self
Fleet agent upgrades and policies|Yes|Not shipped; belongs in Drydock plus packaging|competitor
Host file APIs and Swarm aggregation|Yes|Intentional non-goals|competitor
Prometheus agent scrape endpoint|Not documented|Yes|self
MCP server (read-only)|Not documented|Yes|self
Single lightweight binary|No (~300 MB node image)|Yes (~10 MB Go binary)|self
License|Zlib agent; proprietary Business features|AGPL-3.0|tie
`,
  highlightsTable: `
network|Portainer Leads Fleet Operations|Portainer's Edge and Async Edge products include mature fleet policies, edge stacks/jobs/configuration, and controller-managed agent updates. Portwing currently supports a persistent edge tunnel; Drydock owns future rollout policy.
keyround|Different Authentication Layers|Portainer uses an Agent claim process, optional AGENT_SECRET, Edge credentials, and optional Business mTLS. Portwing signs each standard-mode request and its edge hello with Ed25519, with explicit timestamp and nonce replay checks on HTTP.
shieldcheck|Narrow Socket Boundary|Portainer's documented agent deployment mounts the Docker socket and host paths. Portwing's hardened path puts Sockguard in between and permits only configured HTTP methods and Docker API paths.
activity|Audit at the Agent|Portainer's controller Activity log is a Business feature. Portwing emits structured authentication and Docker mediation records in every build; Drydock can add user-level context above them.
filetext|Host Access Is a Tradeoff|Portainer exposes file-browse APIs and Swarm-wide resource aggregation. Portwing intentionally avoids arbitrary host files and controls one Docker host to keep the privileged surface smaller.
bot|Prometheus and Read-Only MCP|Portwing exposes an agent Prometheus endpoint and five read-only MCP inspection tools. Equivalent agent endpoints were not documented in the reviewed Portainer material.
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
  metadataDescription: `Compare Portainer 2.39 Agent/Edge Agent and Portwing v${SITE_CONFIG.version} across transport, authentication, socket policy, audit, fleet upgrades, host access, metrics, and MCP.`,
  metadataKeywords: [
    "portainer agent vs portwing",
    "portainer alternative",
    "portainer agent replacement",
    "remote docker agent comparison",
    "docker edge agent",
    "docker agent ed25519 auth",
  ],
  openGraphDescription:
    "Portainer leads mature fleet and async-edge operations; Portwing focuses on signed requests, narrow socket policy, agent audit export, Prometheus, and MCP.",
  twitterDescription: `Portainer 2.39 Agent vs Portwing v${SITE_CONFIG.version}: an evidence-backed comparison of transport, security, fleet operations, and scope.`,
  competitorName: "Portainer",
  heroTitle: "Portainer vs Portwing",
  heroDescription: (
    <p>
      Portainer 2.39 is the mature fleet-management benchmark, with classic, Edge, and Async Edge
      agents plus Swarm and controller-driven updates. Portwing v{SITE_CONFIG.version} is a narrower
      Drydock agent focused on{" "}
      <strong className="text-neutral-900 dark:text-neutral-200">
        signed requests, replay defense, Sockguard policy, agent-level audit, Prometheus, and MCP
      </strong>{" "}
      — in the free AGPL-3.0 build, without requiring a Business-tier upgrade. Portwing v0.9.x is a
      supported pre-v1 release; Portainer is production-mature. Reviewed July 28, 2026.
    </p>
  ),
  migrationTitle: "Coming from Portainer Agent?",
  migrationDescription:
    "Inventory Edge Stacks, Jobs, Configurations, RBAC, Swarm, host browsing, and agent update policies before moving controllers. Map those responsibilities to Drydock, then deploy Portwing with Ed25519 keys and the narrowest Sockguard preset that permits the workflows you actually need.",
  jsonLdName: "Portainer Agent vs Portwing — Remote Docker Agent Comparison",
  jsonLdDescription: `Evidence-backed comparison of Portainer 2.39 Agent/Edge Agent and Portwing v${SITE_CONFIG.version}.`,
} satisfies ComparisonRouteRawConfig;
