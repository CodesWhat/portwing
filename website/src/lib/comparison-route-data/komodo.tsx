import { Activity, Bot, KeyRound, Network, ShieldCheck, Terminal } from "lucide-react";
import type { ComparisonRouteRawConfig } from "@/lib/comparison-route-data/types";
import { SITE_CONFIG } from "@/lib/site-config";

export const komodoComparisonRouteData = {
  slug: "komodo",
  comparisonTable: `
Remote container control|Yes (Periphery)|Yes|tie
Connection modes|Core-to-Periphery or outbound Periphery WebSocket|Standard HTTP/S or outbound edge WebSocket (Drydock v1.6.0-rc.11+)|tie
Agent authentication|Public-key handshake; automatic per-server key rotation|Ed25519 per-request HTTP signatures; signed edge hello; manual trust-root rotation|tie
Transparent Docker API|No (Komodo resource/command API)|Yes|self
Structured audit|Full controller audit trail|Agent-level API/auth/Compose/exec audit with cursor export|tie
Host metrics|Collected for dashboards and alerts|Prometheus scrape endpoint plus edge metrics|tie
Docker socket policy|Documented deployment mounts the raw socket|Recommended Sockguard path-and-method policy|self
Supply-chain evidence|Not published|Cosign signatures + archive/image CycloneDX SBOMs + SLSA provenance|self
Host shell, builds, automation, and Swarm|Yes|Host shell and Swarm are non-goals; Drydock owns fleet workflows|competitor
MCP server (read-only)|Not documented|Yes|self
License|GPL-3.0|AGPL-3.0|tie
`,
  highlightsTable: `
keyround|Komodo v2 Closed the Auth Gap|Komodo v2 replaced the old passkey-only model with public-key handshakes, onboarding keys, and automatic Periphery key rotation. Portwing's remaining distinction is signature and replay verification on each standard-mode HTTP request.
network|Both Support Outbound Agents|Periphery can now dial Komodo Core over a bidirectional WebSocket. Portwing dials Drydock over its stable edge wire protocol. Outbound/NAT mode is parity, not a Portwing-only feature.
shieldcheck|Last-Mile Socket Policy|Portwing's hardened path places Sockguard between the agent and Docker with method-and-path rules. Komodo's documented Periphery container mounts the Docker socket directly.
activity|Audit at Different Layers|Komodo records a full controller audit trail. Portwing emits structured records at the Docker mediation point and offers cursor-based NDJSON export. Drydock owns user-level audit context.
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
  metadataDescription: `Compare Komodo Periphery v2.2 and Portwing v${SITE_CONFIG.version} across outbound agents, public-key authentication, Docker API compatibility, socket policy, audit, metrics, and product scope.`,
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
  twitterDescription: `Komodo Periphery v2.2 vs Portwing v${SITE_CONFIG.version}: outbound transport, key auth, audit, and socket containment.`,
  competitorName: "Komodo",
  heroTitle: "Komodo vs Portwing",
  heroDescription: (
    <p>
      Komodo v2.2 is a full build, deployment, automation, and Swarm platform. Its Periphery agent
      now supports outbound WebSockets, public-key authentication, and automatic key rotation.
      Portwing v{SITE_CONFIG.version} is narrower and pairs with Drydock, emphasizing{" "}
      <strong className="text-neutral-900 dark:text-neutral-200">
        per-request verification, transparent Docker compatibility, Sockguard containment, and
        agent-level audit export
      </strong>
      . Komodo is production-mature; Portwing v0.9.x is a supported pre-v1 release. Reviewed July
      28, 2026.
    </p>
  ),
  migrationTitle: "Coming from Komodo Periphery?",
  migrationDescription:
    "Treat this as a controller migration, not an agent image swap. Inventory Komodo builds, procedures, host terminals, Swarm resources, secrets, and Git-backed stacks; map controller responsibilities to Drydock first. Then deploy Portwing with Ed25519 keys and the narrowest Sockguard preset that covers the required Docker operations.",
  jsonLdName: "Komodo Periphery vs Portwing — Remote Docker Agent Comparison",
  jsonLdDescription: `Evidence-backed comparison of Komodo Periphery v2.2 and Portwing v${SITE_CONFIG.version} for remote Docker access.`,
} satisfies ComparisonRouteRawConfig;
