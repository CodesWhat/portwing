import { notFound } from "next/navigation";
import { createComparisonRoute } from "@/lib/comparison-route";
import {
  type ComparisonRouteSlug,
  getComparisonRouteConfig,
  getComparisonRouteSlugs,
} from "@/lib/comparison-route-data";

export function generateStaticParams() {
  return getComparisonRouteSlugs().map((slug) => ({ competitor: slug }));
}

export async function generateMetadata({ params }: { params: Promise<{ competitor: string }> }) {
  const { competitor } = await params;
  const config = getComparisonRouteConfig(competitor as ComparisonRouteSlug);
  if (!config) {
    return {};
  }
  const { metadata } = createComparisonRoute(config);
  return metadata;
}

export default async function CompetitorPage({
  params,
}: {
  params: Promise<{ competitor: string }>;
}) {
  const { competitor } = await params;
  const config = getComparisonRouteConfig(competitor as ComparisonRouteSlug);
  if (!config) {
    notFound();
  }
  const { RoutePage } = createComparisonRoute(config);
  return <RoutePage />;
}
