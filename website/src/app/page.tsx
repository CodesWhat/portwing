// SPDX-License-Identifier: MIT

import type { Metadata } from "next";
import { CompareSection } from "@/components/compare-section";
import { CtaButtons } from "@/components/cta-buttons";
import { Ecosystem } from "@/components/ecosystem";
import { FAQ } from "@/components/faq";
import { FeaturePalette } from "@/components/feature-palette";
import { Footer } from "@/components/footer";
import { GetStarted } from "@/components/get-started";
import { Ic } from "@/components/icons";
import { Roadmap } from "@/components/roadmap";
import { Section } from "@/components/section";
import { SiteHeader } from "@/components/site-header";
import { Terminal } from "@/components/terminal";
import { Topology } from "@/components/topology";
import {
  BASE_URL,
  GITHUB_RELEASES_URL,
  GITHUB_URL,
  REPO_SLUG,
  SITE_CONFIG,
} from "@/lib/site-config";
import { faqItems } from "./data/faq";
import { features } from "./data/features";

export const metadata: Metadata = {
  alternates: {
    canonical: BASE_URL,
  },
};

type Badge = { href: string; src: string; alt: string };

// The README's badges, live. Each link goes where the README links it.
const BADGES: Badge[] = [
  {
    href: `${GITHUB_URL}/blob/main/LICENSE`,
    src: `https://img.shields.io/github/license/${REPO_SLUG}`,
    alt: "License",
  },
  {
    href: `${GITHUB_URL}/actions/workflows/ci-verify.yml`,
    src: `${GITHUB_URL}/actions/workflows/ci-verify.yml/badge.svg?branch=main`,
    alt: "CI",
  },
  {
    href: `https://securityscorecards.dev/viewer/?uri=github.com/${REPO_SLUG}`,
    src: `https://img.shields.io/ossf-scorecard/github.com/${REPO_SLUG}?label=openssf+scorecard&style=flat`,
    alt: "OpenSSF Scorecard",
  },
  {
    href: "https://www.bestpractices.dev/projects/14029",
    src: "https://www.bestpractices.dev/projects/14029/badge",
    alt: "OpenSSF Best Practices",
  },
  {
    href: `https://qlty.sh/gh/${SITE_CONFIG.githubOwner}/projects/${SITE_CONFIG.githubRepo}`,
    src: "https://qlty.sh/badges/0c146428-4c10-46d3-8b6e-0622a7b07720/maintainability.svg",
    alt: "Maintainability",
  },
  {
    href: `${GITHUB_URL}/stargazers`,
    src: `https://img.shields.io/github/stars/${REPO_SLUG}?style=flat`,
    alt: "GitHub stars",
  },
  {
    href: `https://codecov.io/gh/${REPO_SLUG}`,
    src: `https://codecov.io/gh/${REPO_SLUG}/graph/badge.svg`,
    alt: "Coverage",
  },
];

function JsonLd({ data }: { data: Record<string, unknown> }) {
  return (
    <script type="application/ld+json" dangerouslySetInnerHTML={{ __html: JSON.stringify(data) }} />
  );
}

export default function Home() {
  const softwareAppJsonLd = {
    "@context": "https://schema.org",
    "@type": "SoftwareApplication",
    name: SITE_CONFIG.name,
    url: BASE_URL,
    description: SITE_CONFIG.description,
    applicationCategory: "DeveloperApplication",
    operatingSystem: "Linux, macOS, Docker",
    license: SITE_CONFIG.licenseUrl,
    downloadUrl: GITHUB_RELEASES_URL,
    installUrl: `${BASE_URL}/docs/installation`,
    offers: {
      "@type": "Offer",
      price: "0",
      priceCurrency: "USD",
    },
    sameAs: [GITHUB_URL],
    author: {
      "@type": "Organization",
      name: "CodesWhat",
      url: "https://codeswhat.com",
      sameAs: ["https://github.com/CodesWhat"],
    },
    softwareHelp: {
      "@type": "WebPage",
      url: `${BASE_URL}/docs`,
    },
  };

  const websiteJsonLd = {
    "@context": "https://schema.org",
    "@type": "WebSite",
    name: SITE_CONFIG.name,
    url: BASE_URL,
    publisher: {
      "@type": "Organization",
      name: "CodesWhat",
    },
  };

  const faqPageJsonLd = {
    "@context": "https://schema.org",
    "@type": "FAQPage",
    mainEntity: faqItems.map((item) => ({
      "@type": "Question",
      name: item.question,
      acceptedAnswer: {
        "@type": "Answer",
        text: item.answer,
      },
    })),
  };

  return (
    <>
      <JsonLd data={softwareAppJsonLd} />
      <JsonLd data={websiteJsonLd} />
      <JsonLd data={faqPageJsonLd} />
      <SiteHeader home />
      <main id="main-content">
        <section className="hero" id="hero" aria-labelledby="h1" style={{ paddingTop: 64 }}>
          <div className="wrap">
            <div className="grid">
              <div className="lcol">
                <p className="ver">
                  <span className="chip t-neutral">v{SITE_CONFIG.version} · Open Source · MIT</span>
                </p>
                <h1 className="display" id="h1">
                  <span>Remote Docker</span> <span className="mut">Agent</span>
                </h1>
                <p className="lede">
                  We built Portwing to give Drydock a secure foothold on every host. Pair it with
                  sockguard and it never touches the raw Docker socket. Ed25519 auth proves every
                  request, and a structured audit log records everything it touches for export to
                  immutable storage, so you control your fleet without exposing it.
                </p>
                <CtaButtons />
              </div>
              <div className="heroobj">
                <img
                  className="mascot"
                  src="/brand/pigeon-800.webp"
                  alt="Portwing mascot, a slate and violet toy pigeon"
                  width={800}
                  height={800}
                  fetchPriority="high"
                />
              </div>
            </div>
            <div className="proof2">
              <ul className="pstats" aria-label="At a glance">
                <li>
                  <b>{features.length}</b>
                  <span className="micro">capabilities</span>
                </li>
                <li>
                  <b>~10 MB</b>
                  <span className="micro">binary</span>
                </li>
                <li>
                  <b>3</b>
                  <span className="micro">architectures</span>
                </li>
                <li className="sp">
                  <a href="https://github.com/sponsors/CodesWhat">
                    <Ic name="heart" />
                    Sponsor
                  </a>
                </li>
              </ul>
              <ul className="pbadges" aria-label="Project badges">
                {BADGES.map((badge) => (
                  <li key={badge.alt}>
                    <a href={badge.href}>
                      <img
                        src={badge.src}
                        alt={badge.alt}
                        height={20}
                        loading="lazy"
                        decoding="async"
                      />
                    </a>
                  </li>
                ))}
              </ul>
            </div>
          </div>
        </section>

        <Section
          id="features"
          headingId="features-h"
          eyebrow="What it does"
          title={`${features.length} reasons your Docker hosts are safer`}
          subtitle="Security isn't a checkbox — it's every layer. Here's what we built."
          split
        >
          <FeaturePalette />
        </Section>

        <GetStarted />

        <Section
          id="demo"
          headingId="demo-h"
          eyebrow="In the real CLI"
          title="See it in action!"
          subtitle="Recorded from the real CLI: generate a key, boot the agent, sign a request, and watch it reject four kinds of tampering in the audit log."
          strikeEyebrow
          split
        >
          <Terminal />
          <Topology />
        </Section>

        <Roadmap />
        <CompareSection />
        <Ecosystem />
        <FAQ />
      </main>
      <Footer full />
    </>
  );
}
