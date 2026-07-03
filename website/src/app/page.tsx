import type { Metadata } from "next";
import { CliDemo } from "@/components/cli-demo";
import { CompareSection } from "@/components/compare-section";
import { CtaButtons } from "@/components/cta-buttons";
import { Ecosystem } from "@/components/ecosystem";
import { FAQ } from "@/components/faq";
import { FeaturesBrowser } from "@/components/features-browser";
import { GetStarted } from "@/components/get-started";
import { GitHubBadges } from "@/components/github-badges";
import { MarketingShell } from "@/components/marketing-shell";
import { PortwingMascot } from "@/components/portwing-mascot";
import { Roadmap } from "@/components/roadmap";
import { SectionHeading } from "@/components/section-heading";
import { StarHistory } from "@/components/star-history";
import { Badge } from "@/components/ui/badge";
import { BASE_URL, GITHUB_RELEASES_URL, GITHUB_URL, SITE_CONFIG } from "@/lib/site-config";
import { faqItems } from "./data/faq";

export const metadata: Metadata = {
  alternates: {
    canonical: BASE_URL,
  },
};

export default function Home() {
  const softwareAppJsonLd = {
    "@context": "https://schema.org",
    "@type": "SoftwareApplication",
    name: SITE_CONFIG.name,
    url: BASE_URL,
    description: SITE_CONFIG.description,
    applicationCategory: "DeveloperApplication",
    operatingSystem: "Docker",
    license: SITE_CONFIG.licenseUrl,
    downloadUrl: GITHUB_RELEASES_URL,
    installUrl: GITHUB_RELEASES_URL,
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
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: JSON.stringify(softwareAppJsonLd) }}
      />
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: JSON.stringify(websiteJsonLd) }}
      />
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: JSON.stringify(faqPageJsonLd) }}
      />
      <MarketingShell aurora="violet">
        {/* ── Hero ────────────────────────────────────────────────────────────── */}
        <section className="relative px-4 py-20">
          {/* Background glow */}
          <div
            aria-hidden="true"
            className="pointer-events-none absolute inset-0 -z-10 overflow-hidden"
          >
            <div className="absolute left-1/2 top-0 h-96 w-96 -translate-x-1/2 -translate-y-1/4 rounded-full bg-[var(--au-glow)] blur-3xl opacity-60" />
          </div>

          <div className="mx-auto max-w-6xl px-4">
            <div className="flex flex-col items-center gap-6 text-center">
              <Badge variant="secondary" className="font-mono text-xs">
                v{SITE_CONFIG.version} &middot; Open Source &middot; AGPL-3.0
              </Badge>

              <h1 className="max-w-3xl text-6xl font-bold tracking-tight text-neutral-900 dark:text-neutral-100 sm:text-7xl lg:text-8xl">
                Remote Docker
                <br />
                <span className="text-neutral-400 dark:text-neutral-500">Agent</span>
              </h1>

              <p className="max-w-2xl text-lg text-neutral-600 dark:text-neutral-400">
                We built Portwing to give Drydock a secure foothold on every host. Pair it with
                sockguard and it never touches the raw Docker socket. Ed25519 auth proves every
                request, and a tamper-evident audit log records everything it touches, so you
                control your fleet without exposing it.
              </p>

              <CtaButtons align="center" />
            </div>

            {/* Mascot moment */}
            <div className="relative mt-12 flex justify-center">
              <div
                aria-hidden="true"
                className="pointer-events-none absolute inset-0 -z-10 flex items-center justify-center"
              >
                <div className="h-72 w-72 rounded-full bg-[var(--au-glow)] opacity-50 blur-3xl" />
              </div>
              <PortwingMascot size={200} />
            </div>

            {/* Badges */}
            <div className="mt-12">
              <GitHubBadges />
            </div>
          </div>
        </section>

        {/* ── CLI Demo ──────────────────────────────────────────────────────────── */}
        <div className="reveal" suppressHydrationWarning>
          <section className="border-t border-border/60 px-4 py-16">
            <div className="mx-auto max-w-5xl">
              <SectionHeading
                eyebrow="In the real CLI"
                title="See it work"
                subtitle="A looping recreation of the real CLI — boot the agent in standard and edge mode and watch it stream structured audit logs."
              />
              <CliDemo />
            </div>
          </section>
        </div>

        {/* ── Features ──────────────────────────────────────────────────────────── */}
        <div className="reveal" suppressHydrationWarning>
          <section className="border-t border-border/60 px-4 py-16">
            <div className="mx-auto max-w-6xl px-4">
              <SectionHeading
                eyebrow="What it does"
                title="14 reasons your Docker hosts are safer"
                subtitle="Security isn't a checkbox — it's every layer. Here's what we built."
              />
              <FeaturesBrowser />
            </div>
          </section>
        </div>

        {/* ── Get Started ───────────────────────────────────────────────────────── */}
        <div className="reveal" suppressHydrationWarning>
          <GetStarted />
        </div>

        {/* ── Roadmap ───────────────────────────────────────────────────────────── */}
        <div className="reveal" suppressHydrationWarning>
          <Roadmap />
        </div>

        {/* ── Star History ──────────────────────────────────────────────────────── */}
        <div className="reveal" suppressHydrationWarning>
          <StarHistory />
        </div>

        {/* ── Compare ───────────────────────────────────────────────────────────── */}
        <div className="reveal" suppressHydrationWarning>
          <CompareSection />
        </div>

        {/* ── Ecosystem ─────────────────────────────────────────────────────────── */}
        <div className="reveal" suppressHydrationWarning>
          <Ecosystem />
        </div>

        {/* ── FAQ ───────────────────────────────────────────────────────────────── */}
        <div className="reveal" suppressHydrationWarning>
          <FAQ />
        </div>
      </MarketingShell>
    </>
  );
}
