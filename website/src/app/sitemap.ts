import type { MetadataRoute } from "next";
import { BASE_URL } from "@/lib/site-config";

export const dynamic = "force-static";

// Competitor compare page slugs — must match the routes under /compare/[slug].
const COMPARE_SLUGS = ["portainer", "komodo", "hawser", "watchtower", "diun"] as const;

// Docs page slugs enumerated from docs/content/docs/*.mdx.
// index.mdx maps to /docs (listed separately below at priority 0.7).
const DOCS_SLUGS = [
  "getting-started",
  "connection-modes",
  "authentication",
  "audit-logging",
  "configuration",
  "drydock-integration",
  "mcp-server",
  "migrating-from-watchtower",
  "observability",
  "security-model",
  "standalone-mode",
  "verification",
  "api-reference",
] as const;

export default function sitemap(): MetadataRoute.Sitemap {
  const comparePages = COMPARE_SLUGS.map((slug) => ({
    url: `${BASE_URL}/compare/${slug}`,
    lastModified: new Date(),
    changeFrequency: "monthly" as const,
    priority: 0.8,
  }));

  const docPages = DOCS_SLUGS.map((slug) => ({
    url: `${BASE_URL}/docs/${slug}`,
    lastModified: new Date(),
    changeFrequency: "weekly" as const,
    priority: 0.7,
  }));

  return [
    {
      url: BASE_URL,
      lastModified: new Date(),
      changeFrequency: "weekly" as const,
      priority: 1,
    },
    {
      url: `${BASE_URL}/compare`,
      lastModified: new Date(),
      changeFrequency: "monthly" as const,
      priority: 0.9,
    },
    ...comparePages,
    {
      url: `${BASE_URL}/docs`,
      lastModified: new Date(),
      changeFrequency: "weekly" as const,
      priority: 0.7,
    },
    ...docPages,
  ];
}
