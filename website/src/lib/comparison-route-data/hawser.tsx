// SPDX-License-Identifier: MIT

import { Activity, Bot, Gauge, KeyRound, Network, ShieldCheck } from "lucide-react";
import type { ComparisonRouteRawConfig } from "@/lib/comparison-route-data/types";
import { SITE_CONFIG } from "@/lib/site-config";

export const hawserComparisonRouteData = {
  slug: "hawser",
  comparisonTable: `
Remote Docker API proxy|Yes|Yes|tie
Connection modes|Inbound HTTP/S and outbound Dockhand WebSocket|Inbound HTTP/S and outbound Drydock WebSocket (Drydock v1.6.0-rc.11+)|tie
Container, image, network, volume, logs, and exec|Yes|Yes|tie
Compose lifecycle|Yes|Yes|tie
Agent authentication|Bearer token; optional server TLS / WSS|Ed25519 per-request signatures or token in standard mode; signed edge hello|self
Edge listener bind address|Health-only HTTP listener defaults to 0.0.0.0:2376; BIND_ADDRESS=127.0.0.1 restricts it to loopback|Defaults to loopback; config loader refuses a non-loopback bind unless ALLOW_UNAUTHENTICATED_REMOTE=true is set|self
Docker socket policy|Documented deployment mounts the raw socket|Recommended Sockguard path-and-method policy|self
Host metrics|Forwarded to Dockhand every 30 seconds in edge mode|Prometheus scrape endpoint and edge metrics|tie
Request audit|Debug request logging|Structured audit records with cursor export|self
Supply-chain evidence|Checksums and verified GitHub immutable-release attestation; image SBOM/build provenance not found in inspected evidence|Cosign signatures + archive/image CycloneDX SBOMs + SLSA provenance|self
Documented read-only MCP endpoint|Not documented|Yes|self
License|MIT|MIT|tie
`,
  highlightsTable: `
network|The Closest Scope Match|Hawser and Portwing are both small Go agents, transparent Docker proxies, Compose runners, metrics collectors, and outbound WebSocket clients. Neither product should pretend the topology itself is unique.
keyround|Signed Edge Authentication|Hawser authenticates with a bearer token over WSS. Portwing signs the edge hello with an Ed25519 private key and signs individual standard-mode requests with timestamp bounds and nonce replay checks.
shieldcheck|Default-Deny Socket Boundary|Hawser's documented deployment mounts the Docker socket directly. Portwing's production path uses a separate Sockguard process with method-and-path allowlist rules. Hawser also starts an unauthenticated health-only listener, defaulting to 0.0.0.0:2376; BIND_ADDRESS=127.0.0.1 restricts it to loopback. That container listener is not necessarily published on the host, and management still connects outbound. Portwing's edge health listener defaults to loopback and requires an explicit opt-out for a non-loopback bind.
activity|Structured Agent Audit|Hawser can log Docker requests at debug level. Portwing emits stable structured events for API access, auth failures, enrollment, Compose, and exec, with cursor-based NDJSON export.
gauge|Different Metrics Surfaces|Hawser sends host metrics to Dockhand in edge mode. Portwing supports controller metrics and a Prometheus endpoint that can be scraped independently.
bot|Read-Only MCP|Portwing exposes list_containers, inspect_container, container_logs, host_metrics, and container_stats. Hawser does not document an MCP endpoint.
`,
  highlightIconMap: {
    network: Network,
    keyround: KeyRound,
    shieldcheck: ShieldCheck,
    activity: Activity,
    gauge: Gauge,
    bot: Bot,
  },
  metadataTitle: "Hawser vs Portwing — Remote Docker Agent Comparison",
  metadataDescription: `Compare Hawser v0.2.52 and Portwing v${SITE_CONFIG.version}. Both are lightweight Go Docker proxies with standard and edge modes; compare auth, socket policy, auditing, metrics, and release evidence.`,
  metadataKeywords: [
    "hawser vs portwing",
    "hawser alternative",
    "dockhand agent alternative",
    "remote docker agent comparison",
    "docker agent ed25519 auth",
    "docker agent outbound tunnel",
  ],
  openGraphDescription:
    "Hawser and Portwing have closely matched agent scope. Compare bearer-token vs signed-key auth, raw socket vs Sockguard, audit export, metrics, and MCP.",
  twitterDescription: `Hawser v0.2.52 vs Portwing v${SITE_CONFIG.version}: two close Go agent peers compared without stale marketing claims.`,
  competitorName: "Hawser",
  heroTitle: "Hawser vs Portwing",
  heroDescription: (
    <p>
      Hawser v0.2.52 is Dockhand&apos;s remote Docker agent and the closest Portwing peer in scope.
      Both are lightweight Go binaries with transparent Docker proxying, Compose, metrics, and
      outbound edge transport. Portwing v{SITE_CONFIG.version} emphasizes{" "}
      <strong className="text-neutral-900 dark:text-neutral-200">
        signed-key authentication, Sockguard containment, structured audit export, Prometheus, and
        read-only MCP
      </strong>
      . Hawser integrates with Dockhand; Portwing integrates with Drydock. Reviewed October 26,
      2026.
    </p>
  ),
  migrationTitle: "Coming from Hawser?",
  migrationDescription:
    "The agent capabilities map closely, but the wire controllers do not. Move the environment to Drydock, deploy Portwing with Ed25519 keys, choose the smallest Sockguard preset that covers the Dockhand workflows you used, and test Compose paths and streaming behavior against tagged artifacts.",
  jsonLdName: "Hawser vs Portwing — Remote Docker Agent Comparison",
  jsonLdDescription: `Evidence-backed comparison of Hawser v0.2.52 and Portwing v${SITE_CONFIG.version} for remote Docker access.`,
} satisfies ComparisonRouteRawConfig;
