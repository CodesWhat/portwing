// SPDX-License-Identifier: MIT

import type { Metadata } from "next";
import { CompareMatrix } from "@/components/compare-matrix";
import { Footer } from "@/components/footer";
import { SiteHeader } from "@/components/site-header";
import { BASE_URL, SITE_CONFIG } from "@/lib/site-config";

export const metadata: Metadata = {
  // absolute: this title already carries the brand; opt out of the root template.
  title: { absolute: "Portwing vs Alternatives — Remote Docker Agent Comparisons" },
  description:
    "Evidence-backed comparisons of Portwing with Portainer Agent, Komodo Periphery, Arcane Agent, Hawser, and adjacent Docker tools. Reviewed October 26, 2026.",
  keywords: [
    "portainer agent alternative",
    "komodo periphery alternative",
    "arcane agent alternative",
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
      "Compare Portwing with Portainer Agent, Komodo Periphery, Arcane Agent, Hawser, and adjacent Docker tools using an August 2026 primary-source review.",
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
      "Compare Portwing with Portainer, Komodo, Arcane, Hawser, and adjacent Docker tools using current primary sources.",
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
  { name: "Arcane Agent", slug: "arcane" },
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
          "Evidence-backed comparisons of Portwing with current remote Docker agents and adjacent tools.",
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
      <SiteHeader current="compare" />
      <main id="main-content">
        <section className="cmp-hero" aria-labelledby="ix-h1">
          <div className="wrap">
            <h1 className="cp" id="ix-h1">
              Portwing vs Alternatives
            </h1>
            <p className="cp-sub">
              Current agent capabilities, controller tradeoffs, and deliberate non-goals — sourced
              from published product documentation and reviewed October 26, 2026.
            </p>
          </div>
        </section>
        <section style={{ padding: "20px 0 56px" }}>
          <div className="wrap">
            <CompareMatrix />
          </div>
        </section>
      </main>
      <Footer />
    </>
  );
}
