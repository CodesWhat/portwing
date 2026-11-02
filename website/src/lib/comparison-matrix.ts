// SPDX-License-Identifier: MIT

import { SITE_CONFIG } from "@/lib/site-config";

// Owner of the capability marks shown on the home page teaser and the /compare matrix.
// Per-competitor detail lives in lib/comparison-route-data/.

export type CellValue = "yes" | "partial" | "no" | "undocumented";

export const FEATURES = [
  { key: "remoteControl", label: "Remote control" },
  { key: "standardEdge", label: "Inbound + edge" },
  { key: "asymmetricIdentity", label: "Asymmetric identity" },
  { key: "leastPrivilege", label: "Socket policy" },
  { key: "agentAudit", label: "Agent audit" },
  { key: "transparentApi", label: "Docker API" },
  { key: "metrics", label: "Agent Prometheus" },
  { key: "mcpServer", label: "MCP server" },
] as const;

export type FeatureKey = (typeof FEATURES)[number]["key"];

export type Tool = {
  name: string;
  slug: string | null;
  cells: Record<FeatureKey, CellValue>;
};

const cells = (
  remoteControl: CellValue,
  standardEdge: CellValue,
  asymmetricIdentity: CellValue,
  leastPrivilege: CellValue,
  agentAudit: CellValue,
  transparentApi: CellValue,
  metrics: CellValue,
  mcpServer: CellValue,
): Record<FeatureKey, CellValue> => ({
  remoteControl,
  standardEdge,
  asymmetricIdentity,
  leastPrivilege,
  agentAudit,
  transparentApi,
  metrics,
  mcpServer,
});

export const TOOLS: Tool[] = [
  {
    name: SITE_CONFIG.name,
    slug: null,
    cells: cells("yes", "yes", "yes", "yes", "yes", "yes", "yes", "yes"),
  },
  {
    name: "Portainer",
    slug: "portainer",
    cells: cells("yes", "yes", "partial", "no", "partial", "yes", "undocumented", "undocumented"),
  },
  {
    name: "Komodo",
    slug: "komodo",
    cells: cells("yes", "yes", "yes", "no", "partial", "no", "partial", "undocumented"),
  },
  {
    name: "Arcane",
    slug: "arcane",
    cells: cells("yes", "yes", "partial", "partial", "partial", "no", "partial", "undocumented"),
  },
  {
    name: "Hawser",
    slug: "hawser",
    cells: cells("yes", "yes", "no", "no", "partial", "yes", "partial", "undocumented"),
  },
  {
    name: "Watchtower",
    slug: "watchtower",
    cells: cells("partial", "no", "no", "no", "no", "no", "yes", "undocumented"),
  },
  {
    name: "Diun",
    slug: "diun",
    cells: cells("partial", "no", "no", "no", "no", "no", "yes", "undocumented"),
  },
];

/** Home page teaser: the four closest remote-agent peers across six capabilities. */
export const TEASER_TOOLS = TOOLS.slice(0, 5);
export const TEASER_ROWS: { label: string; key: FeatureKey }[] = [
  { label: "Inbound + outbound edge modes", key: "standardEdge" },
  { label: "Asymmetric agent identity", key: "asymmetricIdentity" },
  { label: "Documented socket policy layer", key: "leastPrivilege" },
  { label: "Structured agent-level audit", key: "agentAudit" },
  { label: "Transparent Docker API", key: "transparentApi" },
  { label: "Read-only MCP server", key: "mcpServer" },
];
