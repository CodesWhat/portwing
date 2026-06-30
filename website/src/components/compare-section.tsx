import { ArrowRight, Check, Minus, X } from "lucide-react";
import Link from "next/link";
import { type CellValue, ComparisonCellIcon } from "@/components/comparison-cell-icon";
import { SectionHeading } from "@/components/section-heading";
import { SITE_CONFIG } from "@/lib/site-config";

// Mini comparison teaser — 6 rows, 3 rivals (closest peers: Portainer Agent, Hawser, Komodo).

interface FeatureRow {
  label: string;
  portwing: CellValue;
  portainer: CellValue;
  hawser: CellValue;
  komodo: CellValue;
}

const featureRows: FeatureRow[] = [
  {
    label: "Remote container control",
    portwing: "yes",
    portainer: "yes",
    hawser: "yes",
    komodo: "yes",
  },
  {
    label: "Per-client key auth (Ed25519)",
    portwing: "yes",
    portainer: "partial",
    hawser: "partial",
    komodo: "partial",
  },
  {
    label: "Default-deny socket filter",
    portwing: "yes",
    portainer: "no",
    hawser: "no",
    komodo: "no",
  },
  {
    label: "Structured audit log",
    portwing: "yes",
    portainer: "partial",
    hawser: "no",
    komodo: "no",
  },
  {
    label: "Signed images + SBOM",
    portwing: "yes",
    portainer: "no",
    hawser: "no",
    komodo: "no",
  },
  {
    label: "MCP server (AI-native)",
    portwing: "yes",
    portainer: "no",
    hawser: "no",
    komodo: "no",
  },
];

function ViewAllLink() {
  return (
    <Link
      href="/compare"
      className="group inline-flex items-center gap-2 rounded-lg border border-neutral-200 bg-white/50 px-6 py-3 font-medium text-neutral-900 backdrop-blur-sm transition-all hover:border-neutral-300 hover:bg-white/80 dark:border-neutral-800 dark:bg-neutral-900/50 dark:text-neutral-100 dark:hover:border-neutral-700 dark:hover:bg-neutral-900/80"
    >
      View all comparisons
      <ArrowRight className="h-4 w-4 transition-transform group-hover:translate-x-0.5" />
    </Link>
  );
}

const tools = [SITE_CONFIG.name, "Portainer", "Hawser", "Komodo"] as const;
type Tool = (typeof tools)[number];

function cellValue(row: FeatureRow, tool: Tool): CellValue {
  const map: Record<Tool, CellValue> = {
    [SITE_CONFIG.name]: row.portwing,
    Portainer: row.portainer,
    Hawser: row.hawser,
    Komodo: row.komodo,
  };
  return map[tool];
}

export function CompareSection() {
  return (
    <section className="border-t border-border/60 py-20">
      <div className="mx-auto max-w-4xl px-4">
        <SectionHeading
          eyebrow={`Why ${SITE_CONFIG.name}`}
          title="How we compare"
          subtitle="A quick look at what Portwing ships that Portainer Agent, Hawser, and Komodo Periphery don't."
          align="right"
        />

        <div className="overflow-hidden rounded-xl border border-neutral-200 bg-white/50 backdrop-blur-sm dark:border-neutral-800 dark:bg-neutral-900/50">
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-neutral-200 dark:border-neutral-800">
                  <th className="px-5 py-3 text-left font-medium text-neutral-500 dark:text-neutral-400">
                    Feature
                  </th>
                  {tools.map((tool) => (
                    <th
                      key={tool}
                      className={[
                        "px-4 py-3 text-center font-semibold",
                        tool === SITE_CONFIG.name
                          ? "bg-violet-500/10 text-violet-700 dark:bg-violet-400/10 dark:text-violet-300"
                          : "text-neutral-500 dark:text-neutral-400",
                      ].join(" ")}
                    >
                      {tool}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {featureRows.map((row, i) => (
                  <tr
                    key={row.label}
                    className={[
                      "border-b border-neutral-100 last:border-0 dark:border-neutral-800/60",
                      i % 2 === 1 ? "bg-neutral-50/50 dark:bg-neutral-800/20" : "",
                    ].join(" ")}
                  >
                    <td className="px-5 py-3 text-neutral-700 dark:text-neutral-300">
                      {row.label}
                    </td>
                    {tools.map((tool) => (
                      <td
                        key={tool}
                        className={[
                          "px-4 py-3 text-center",
                          tool === SITE_CONFIG.name ? "bg-violet-500/5" : "",
                        ].join(" ")}
                      >
                        <ComparisonCellIcon value={cellValue(row, tool)} />
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div className="border-t border-neutral-200 px-5 py-4 text-xs text-neutral-500 dark:border-neutral-800 dark:text-neutral-500">
            <span className="inline-flex items-center gap-1.5">
              <Check className="h-3 w-3 text-violet-500" /> Yes
            </span>
            <span className="mx-3 inline-flex items-center gap-1.5">
              <Minus className="h-3 w-3 text-fuchsia-400" /> Partial
            </span>
            <span className="inline-flex items-center gap-1.5">
              <X className="h-3 w-3 text-neutral-400" /> No
            </span>
          </div>
        </div>

        <div className="mt-8 text-center">
          <ViewAllLink />
        </div>
      </div>
    </section>
  );
}
