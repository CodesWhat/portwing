interface ComparisonRow {
  feature: string;
  portainer: string;
  komodo: string;
  hawser: string;
  watchtower: string;
  diun: string;
  portwing: string;
  planned?: boolean;
}

// Competitor columns are scoped to each tool's published, default behaviour
// (Portainer Agent/Edge, Komodo Periphery, Hawser, Watchtower, Diun). Where a
// tool is purpose-built for something else (Watchtower updates, Diun notifies)
// the cell says so rather than scoring it as a failure. Hawser is the closest
// analogue — a remote Docker agent for the Dockhand controller.
export const comparisonRows: ComparisonRow[] = [
  {
    feature: "Remote container control",
    portainer: "Yes",
    komodo: "Yes",
    hawser: "Yes",
    watchtower: "Updates only",
    diun: "Notify only",
    portwing: "Yes",
  },
  {
    feature: "Default-deny socket (no raw socket)",
    portainer: "No",
    komodo: "No",
    hawser: "No",
    watchtower: "No",
    diun: "No",
    portwing: "Yes",
  },
  {
    feature: "Per-client key auth (Ed25519)",
    portainer: "Shared secret",
    komodo: "Passkey",
    hawser: "Shared secret",
    watchtower: "—",
    diun: "—",
    portwing: "Yes",
  },
  {
    feature: "Signed images + SBOM + provenance",
    portainer: "No",
    komodo: "No",
    hawser: "No",
    watchtower: "No",
    diun: "No",
    portwing: "Yes",
  },
  {
    feature: "Hardened runtime defaults",
    portainer: "Partial",
    komodo: "Partial",
    hawser: "Partial",
    watchtower: "No",
    diun: "Partial",
    portwing: "Yes",
  },
  {
    feature: "Structured audit log",
    portainer: "Business tier",
    komodo: "No",
    hawser: "No",
    watchtower: "No",
    diun: "No",
    portwing: "Yes",
  },
  {
    feature: "Prometheus metrics",
    portainer: "Partial",
    komodo: "Partial",
    hawser: "No",
    watchtower: "No",
    diun: "Yes",
    portwing: "Yes",
  },
  {
    feature: "MCP server (AI-native, read-only)",
    portainer: "No",
    komodo: "No",
    hawser: "No",
    watchtower: "No",
    diun: "No",
    portwing: "Yes",
  },
  {
    feature: "Edge / NAT outbound mode",
    portainer: "Yes (Edge)",
    komodo: "No",
    hawser: "Yes",
    watchtower: "—",
    diun: "—",
    portwing: "Agent-side (controller WIP)",
    planned: true,
  },
  {
    feature: "Single static Go binary",
    portainer: "No",
    komodo: "Yes",
    hawser: "Yes",
    watchtower: "Yes",
    diun: "Yes",
    portwing: "Yes (~10 MB)",
  },
  {
    feature: "License",
    portainer: "Zlib",
    komodo: "GPL-3.0",
    hawser: "MIT",
    watchtower: "Apache-2.0",
    diun: "MIT",
    portwing: "AGPL-3.0",
  },
];
