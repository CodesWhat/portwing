import type { ComparisonRouteConfig } from "@/lib/comparison-route";
import { highlightsFromPipeTable, rowsFromPipeTable } from "@/lib/comparison-route";
import type { ComparisonRouteRawConfig } from "@/lib/comparison-route-data/types";
import { diunComparisonRouteData } from "./comparison-route-data/diun";
import { hawserComparisonRouteData } from "./comparison-route-data/hawser";
import { komodoComparisonRouteData } from "./comparison-route-data/komodo";
import { portainerComparisonRouteData } from "./comparison-route-data/portainer";
import { watchtowerComparisonRouteData } from "./comparison-route-data/watchtower";

const comparisonRouteDataBySlug = {
  portainer: portainerComparisonRouteData,
  komodo: komodoComparisonRouteData,
  hawser: hawserComparisonRouteData,
  watchtower: watchtowerComparisonRouteData,
  diun: diunComparisonRouteData,
} satisfies Record<string, ComparisonRouteRawConfig>;

export type ComparisonRouteSlug = keyof typeof comparisonRouteDataBySlug;

function resolveComparisonRouteConfig(routeData: ComparisonRouteRawConfig): ComparisonRouteConfig {
  const { comparisonTable, highlightsTable, highlightIconMap, ...config } = routeData;

  return {
    ...config,
    comparisonData: rowsFromPipeTable(comparisonTable),
    highlights: highlightsFromPipeTable(highlightsTable, highlightIconMap),
  };
}

export function getComparisonRouteConfig(slug: ComparisonRouteSlug): ComparisonRouteConfig;
export function getComparisonRouteConfig(slug: string): ComparisonRouteConfig | undefined;
export function getComparisonRouteConfig(slug: string): ComparisonRouteConfig | undefined {
  const routeData = comparisonRouteDataBySlug[slug as ComparisonRouteSlug];
  if (!routeData) {
    return undefined;
  }

  return resolveComparisonRouteConfig(routeData);
}

export function getComparisonRouteSlugs(): ComparisonRouteSlug[] {
  return Object.keys(comparisonRouteDataBySlug) as ComparisonRouteSlug[];
}
