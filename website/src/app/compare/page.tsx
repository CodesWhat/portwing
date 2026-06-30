import type { Metadata } from "next";
import { CompareMatrix } from "@/components/compare-matrix";
import { MarketingShell } from "@/components/marketing-shell";
import { BASE_URL, SITE_CONFIG } from "@/lib/site-config";

export const metadata: Metadata = {
  // absolute: this title already carries the brand; opt out of the root template.
  title: { absolute: "Portwing vs Alternatives — Remote Docker Agent Comparisons" },
  description:
    "Compare Portwing to Portainer Agent, Komodo Periphery, Hawser, Watchtower, and Diun. Feature-by-feature breakdowns for remote Docker agent tools.",
  keywords: [
    "portainer agent alternative",
    "komodo periphery alternative",
    "hawser alternative",
    "watchtower alternative",
    "diun alternative",
    "remote docker agent comparison",
    "docker agent ed25519 auth",
    "docker agent audit log",
    "docker agent signed images",
    "portwing comparison",
  ],
  openGraph: {
    title: "Portwing vs Alternatives — Remote Docker Agent Comparisons",
    description:
      "Compare Portwing to Portainer Agent, Komodo Periphery, Hawser, Watchtower, and Diun. Feature-by-feature breakdowns.",
    url: `${BASE_URL}/compare`,
    siteName: SITE_CONFIG.name,
    locale: SITE_CONFIG.locale,
    type: "website",
    images: [{ url: SITE_CONFIG.ogImage, width: 1200, height: 630 }],
  },
  twitter: {
    card: "summary_large_image",
    title: "Portwing vs Alternatives — Remote Docker Agent Comparisons",
    description:
      "Compare Portwing to Portainer Agent, Komodo Periphery, Hawser, Watchtower, and Diun.",
    creator: SITE_CONFIG.twitterCreator,
    images: [SITE_CONFIG.ogImage],
  },
  alternates: {
    canonical: `${BASE_URL}/compare`,
  },
};

const tools = [
  { name: "Portainer Agent", slug: "portainer" },
  { name: "Komodo (Periphery)", slug: "komodo" },
  { name: "Hawser", slug: "hawser" },
  { name: "Watchtower", slug: "watchtower" },
  { name: "Diun", slug: "diun" },
];

function JsonLdScript({ data }: { data: Record<string, unknown> }) {
  return (
    // biome-ignore lint/security/noDangerouslySetInnerHtml: trusted JSON-LD for SEO schema markup
    <script type="application/ld+json" dangerouslySetInnerHTML={{ __html: JSON.stringify(data) }} />
  );
}

export default function ComparePage() {
  const jsonLd = {
    "@context": "https://schema.org",
    "@graph": [
      {
        "@type": "CollectionPage",
        name: "Portwing vs Alternatives — Remote Docker Agent Comparisons",
        description:
          "Compare Portwing to Portainer Agent, Komodo Periphery, Hawser, Watchtower, and Diun.",
        url: `${BASE_URL}/compare`,
        mainEntity: {
          "@type": "ItemList",
          numberOfItems: tools.length,
          itemListElement: tools.map((tool, i) => ({
            "@type": "ListItem",
            position: i + 1,
            url: `${BASE_URL}/compare/${tool.slug}`,
            name: `${tool.name} vs Portwing`,
          })),
        },
      },
      {
        "@type": "BreadcrumbList",
        itemListElement: [
          {
            "@type": "ListItem",
            position: 1,
            name: "Home",
            item: BASE_URL,
          },
          {
            "@type": "ListItem",
            position: 2,
            name: "Compare",
            item: `${BASE_URL}/compare`,
          },
        ],
      },
    ],
  };

  return (
    <>
      <JsonLdScript data={jsonLd} />
      <MarketingShell>
        {/* Hero */}
        <section className="px-4 pt-16 pb-12">
          <div className="mx-auto max-w-4xl text-center">
            <h1 className="mb-4 text-4xl font-bold tracking-tight text-neutral-900 sm:text-5xl dark:text-neutral-100">
              Portwing vs Alternatives
            </h1>
            <p className="mx-auto max-w-2xl text-lg text-neutral-600 dark:text-neutral-400">
              We built Portwing to be the most security-focused remote Docker agent in the stack.
              Click any tool to see exactly how we compare.
            </p>
          </div>
        </section>

        {/* Full comparison matrix */}
        <section className="px-4 pb-24">
          <div className="mx-auto max-w-5xl">
            <CompareMatrix />
          </div>
        </section>
      </MarketingShell>
    </>
  );
}
