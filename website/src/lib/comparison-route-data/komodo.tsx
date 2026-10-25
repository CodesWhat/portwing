// SPDX-License-Identifier: MIT

import { Activity, Bot, KeyRound, Network, ShieldCheck, Terminal } from "lucide-react";
import type { ComparisonRouteRawConfig } from "@/lib/comparison-route-data/types";
import { SITE_CONFIG } from "@/lib/site-config";

export const komodoComparisonRouteData = {
  slug: "komodo",
  comparisonTable: `
Remote container control|Yes (Periphery)|Yes|tie
Connection modes|Core-to-Periphery or outbound Periphery WebSocket|Standard HTTP/S or outbound edge WebSocket (Drydock v1.6.0-rc.11+)|tie
Agent authentication|Public-key handshake; Core-managed rotation for connected eligible servers; legacy inbound passkeys remain supported|Ed25519 per-request HTTP signatures; signed edge hello; opt-in controller-assisted rotation with Drydock v1.8.0-rc.1+|tie
Transparent Docker API|No (Komodo resource/command API)|Yes|self
Structured audit|Controller audit; Periphery JSON logs and OTLP traces|Agent-level API/auth/Compose/exec audit with cursor export|tie
Host metrics|Collected for dashboards and alerts|Prometheus scrape endpoint plus edge metrics|tie
Docker socket policy|Documented deployment mounts the raw socket|Recommended Sockguard path-and-method policy|self
Supply-chain evidence|v2.3.3 has bare release binaries; Periphery images have BuildKit SLSA v1 provenance; no SBOM or Cosign signature found in checked discovery paths|Cosign signatures + archive/image CycloneDX SBOMs + SLSA provenance|self
Host shell, builds, automation, and Swarm|Yes|Host shell and Swarm are non-goals; Drydock owns fleet workflows|competitor
Documented read-only MCP endpoint|Not documented|Yes|self
License|GPL-3.0|MIT|tie
`,
  highlightsTable: `
keyround|Public-Key Authentication and Rotation|Komodo v2 added preferred public-key handshakes and deprecated legacy passkeys, which remain supported on configured inbound connections. Fresh installs create a daily Core rotation procedure for connected eligible servers. Portwing's configured Ed25519 HTTP path binds and verifies each request, with timestamp and nonce replay checks.
network|Both Support Outbound Agents|Periphery can now dial Komodo Core over a bidirectional WebSocket. Portwing dials Drydock over its stable edge wire protocol. Outbound/NAT mode is parity, not a Portwing-only feature. Two open user reports concern outbound behavior. The current connector does not implement HTTP proxy environment handling (#1473). A v2.2.0 user reported reconnect stalls on unstable Tailscale links (#1518); the current release retains a two-second login-message timeout, but that stall has not been independently reproduced here.
shieldcheck|Last-Mile Socket Policy|Portwing's hardened path places Sockguard between the agent and Docker with method-and-path rules. Komodo's documented Periphery container mounts the Docker socket directly.
activity|Audit at Different Layers|Komodo records controller activity; Periphery also supports JSON logs and OTLP traces. Portwing emits structured records at the Docker mediation point and offers cursor-based NDJSON export. Drydock owns user-level audit context.
terminal|Komodo Is the Broader Platform|Komodo includes host terminals, builds, automation, schedules, configuration, and Swarm. Those are real strengths, but most belong in Drydock rather than in a privileged Portwing agent.
bot|Transparent API and MCP|Portwing preserves Docker Engine formats and exposes five read-only MCP inspection tools. Komodo exposes a strong documented platform API, but not a transparent Docker proxy or documented MCP server.
`,
  highlightIconMap: {
    keyround: KeyRound,
    network: Network,
    shieldcheck: ShieldCheck,
    activity: Activity,
    terminal: Terminal,
    bot: Bot,
  },
  metadataTitle: "Komodo Periphery vs Portwing — Remote Docker Agent Comparison",
  metadataDescription: `Compare Komodo Periphery v2.3.3 and Portwing v${SITE_CONFIG.version} across outbound agents, public-key authentication, Docker API compatibility, socket policy, audit, metrics, and product scope.`,
  metadataKeywords: [
    "komodo periphery vs portwing",
    "komodo alternative",
    "periphery agent alternative",
    "remote docker agent comparison",
    "docker agent public key authentication",
    "docker socket policy",
  ],
  openGraphDescription:
    "Komodo v2 and Portwing both support outbound public-key-authenticated agents. Compare transparent Docker access, socket policy, audit placement, and controller scope.",
  twitterDescription: `Komodo Periphery v2.3.3 vs Portwing v${SITE_CONFIG.version}: outbound transport, key auth, audit, and socket containment.`,
  competitorName: "Komodo",
  heroTitle: "Komodo vs Portwing",
  heroDescription: (
    <p>
      Komodo v2.3.3 is a full build, deployment, automation, and Swarm platform. Its Periphery agent
      now supports outbound WebSockets, public-key authentication, and automatic key rotation.
      Portwing v{SITE_CONFIG.version} is narrower and pairs with Drydock, emphasizing{" "}
      <strong className="text-neutral-900 dark:text-neutral-200">
        per-request verification, transparent Docker compatibility, Sockguard containment, and
        agent-level audit export
      </strong>
      . Komodo owns the broader controller workflows; Portwing keeps the access-agent boundary.
      Reviewed October 15, 2026.
    </p>
  ),
  migrationTitle: "Coming from Komodo Periphery?",
  migrationDescription:
    "Treat this as a controller migration, not an agent image swap. Inventory Komodo builds, procedures, host terminals, Swarm resources, secrets, and Git-backed stacks; map controller responsibilities to Drydock first. Then deploy Portwing with Ed25519 keys and the narrowest Sockguard preset that covers the required Docker operations.",
  jsonLdName: "Komodo Periphery vs Portwing — Remote Docker Agent Comparison",
  jsonLdDescription: `Evidence-backed comparison of Komodo Periphery v2.3.3 and Portwing v${SITE_CONFIG.version} for remote Docker access.`,
} satisfies ComparisonRouteRawConfig;
