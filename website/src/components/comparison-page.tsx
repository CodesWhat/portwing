import type { LucideIcon } from "lucide-react";
import { AlertTriangle, Check, Clock, Minus, Terminal, X } from "lucide-react";
import { CtaButtons } from "@/components/cta-buttons";
import { MarketingShell } from "@/components/marketing-shell";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { SITE_CONFIG } from "@/lib/site-config";

export type ComparisonRow = {
  feature: string;
  competitor: string;
  self: string;
  verdict: "self" | "competitor" | "tie";
};

export type Highlight = {
  icon: LucideIcon;
  title: string;
  description: string;
};

type CompetitorBadge = {
  icon: LucideIcon;
  label: string;
  className: string;
};

type Props = {
  competitorName: string;
  heroTitle: string;
  heroDescription: React.ReactNode;
  competitorBadge: CompetitorBadge;
  selfBadge: CompetitorBadge;
  comparisonData: ComparisonRow[];
  highlights: Highlight[];
  migrationTitle: string;
  migrationDescription: string;
  jsonLd: Record<string, unknown>;
};

function JsonLdScript({ data }: { data: Record<string, unknown> }) {
  return (
    // biome-ignore lint/security/noDangerouslySetInnerHtml: trusted JSON-LD for SEO schema markup
    <script type="application/ld+json" dangerouslySetInnerHTML={{ __html: JSON.stringify(data) }} />
  );
}

function VerdictIcon({ verdict }: { verdict: ComparisonRow["verdict"] }) {
  if (verdict === "self" || verdict === "competitor") {
    return <Check className="h-4 w-4 text-violet-500" />;
  }
  return <Minus className="h-4 w-4 text-neutral-400" />;
}

function PortwingQuickStart() {
  return (
    <Card className="border-neutral-200 bg-neutral-950 text-left dark:border-neutral-800">
      <CardContent className="pt-4 pb-4">
        <div className="mb-2 flex items-center gap-2 text-neutral-500">
          <Terminal className="h-3.5 w-3.5" />
          <span className="text-xs font-medium uppercase tracking-wider">Quick start</span>
        </div>
        <pre className="overflow-x-auto text-sm">
          <code className="text-neutral-300">
            <span className="text-neutral-500">$</span>{" "}
            <span className="text-[#a78bfa]">docker run</span> -d \{"\n"}
            {"  "}--name portwing \{"\n"}
            {"  "}--read-only --cap-drop=ALL \{"\n"}
            {"  "}--security-opt no-new-privileges:true \{"\n"}
            {"  "}-v /var/run/docker.sock:/var/run/docker.sock:ro \{"\n"}
            {"  "}-e TOKEN_HASH=&quot;$PORTWING_TOKEN_HASH&quot; \{"\n"}
            {"  "}-p 3000:3000 \{"\n"}
            {"  "}
            {SITE_CONFIG.dockerImage}:latest
          </code>
        </pre>
      </CardContent>
    </Card>
  );
}

export function ComparisonPage({
  competitorName,
  heroTitle,
  heroDescription,
  competitorBadge,
  selfBadge,
  comparisonData,
  highlights,
  migrationTitle,
  migrationDescription,
  jsonLd,
}: Props) {
  return (
    <>
      <JsonLdScript data={jsonLd} />
      <MarketingShell>
        {/* Hero */}
        <section className="px-4 pt-16 pb-16">
          <div className="mx-auto max-w-4xl text-center">
            <h1 className="mb-6 text-4xl font-bold tracking-tight text-neutral-900 dark:text-neutral-100 sm:text-5xl lg:text-6xl">
              {heroTitle}
            </h1>

            <div className="mx-auto mb-8 max-w-2xl text-lg text-neutral-600 dark:text-neutral-400">
              {heroDescription}
            </div>

            <div className="flex flex-wrap items-center justify-center gap-3">
              <Badge className={competitorBadge.className}>
                <competitorBadge.icon className="mr-1.5 h-3.5 w-3.5" />
                {competitorBadge.label}
              </Badge>
              <Badge className={selfBadge.className}>
                <selfBadge.icon className="mr-1.5 h-3.5 w-3.5" />
                {selfBadge.label}
              </Badge>
            </div>
          </div>
        </section>

        {/* Comparison Table */}
        <section className="px-4 py-16">
          <div className="mx-auto max-w-5xl">
            <div className="mb-12 text-center">
              <h2 className="mb-4 text-3xl font-bold tracking-tight text-neutral-900 sm:text-4xl dark:text-neutral-50">
                Feature Comparison
              </h2>
              <p className="mx-auto max-w-2xl text-neutral-600 dark:text-neutral-400">
                Here&apos;s how we compare on the features that matter most.
              </p>
            </div>

            <div className="overflow-x-auto rounded-xl border border-neutral-200 bg-white/50 backdrop-blur-sm dark:border-neutral-800 dark:bg-neutral-900/50">
              <table className="w-full min-w-[600px] text-sm">
                <thead>
                  <tr className="border-b border-neutral-200 dark:border-neutral-800">
                    <th className="px-4 py-3 text-left font-semibold text-neutral-900 sm:px-6 dark:text-neutral-100">
                      Feature
                    </th>
                    <th className="px-4 py-3 text-left font-semibold text-neutral-500 sm:px-6 dark:text-neutral-400">
                      {competitorName}
                    </th>
                    <th className="px-4 py-3 text-left font-semibold text-neutral-900 sm:px-6 dark:text-neutral-100">
                      {SITE_CONFIG.name}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {comparisonData.map((row, i) => (
                    <tr
                      key={row.feature}
                      className={
                        i < comparisonData.length - 1
                          ? "border-b border-neutral-100 dark:border-neutral-800/50"
                          : ""
                      }
                    >
                      <td className="px-4 py-3 font-medium text-neutral-900 sm:px-6 dark:text-neutral-100">
                        {row.feature}
                      </td>
                      <td className="px-4 py-3 text-neutral-500 sm:px-6 dark:text-neutral-400">
                        <span className="flex items-center gap-2">
                          {row.competitor === "No" || row.competitor === "None" ? (
                            <X className="h-4 w-4 shrink-0 text-neutral-300 dark:text-neutral-600" />
                          ) : row.verdict === "self" ? (
                            <AlertTriangle className="h-4 w-4 shrink-0 text-violet-400" />
                          ) : row.verdict === "competitor" ? (
                            <Check className="h-4 w-4 shrink-0 text-violet-500" />
                          ) : (
                            <VerdictIcon verdict="tie" />
                          )}
                          {row.competitor}
                        </span>
                      </td>
                      <td className="px-4 py-3 sm:px-6">
                        <span className="flex items-center gap-2 text-neutral-900 dark:text-neutral-100">
                          {row.verdict === "self" ? (
                            <Check className="h-4 w-4 shrink-0 text-violet-500" />
                          ) : row.verdict === "competitor" ? (
                            <AlertTriangle className="h-4 w-4 shrink-0 text-violet-400" />
                          ) : (
                            <VerdictIcon verdict="tie" />
                          )}
                          {row.self}
                        </span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        </section>

        {/* Key Differentiators */}
        <section className="px-4 py-16">
          <div className="mx-auto max-w-5xl">
            <div className="mb-12 text-center">
              <h2 className="mb-4 text-3xl font-bold tracking-tight text-neutral-900 sm:text-4xl dark:text-neutral-50">
                Key Differentiators
              </h2>
              <p className="mx-auto max-w-2xl text-neutral-600 dark:text-neutral-400">
                {`What we built that ${competitorName} doesn't cover.`}
              </p>
            </div>

            <div className="grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
              {highlights.map((item) => (
                <Card
                  key={item.title}
                  className="border-neutral-200 bg-white/50 backdrop-blur-sm dark:border-neutral-800 dark:bg-neutral-900/50"
                >
                  <CardContent className="pt-6">
                    <div className="mb-4 flex items-center gap-3">
                      <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-violet-100 dark:bg-violet-900/50">
                        <item.icon className="h-5 w-5 text-violet-500 dark:text-violet-400" />
                      </div>
                      <h3 className="font-semibold text-neutral-900 dark:text-neutral-100">
                        {item.title}
                      </h3>
                    </div>
                    <p className="text-sm text-neutral-600 dark:text-neutral-400">
                      {item.description}
                    </p>
                  </CardContent>
                </Card>
              ))}
            </div>
          </div>
        </section>

        {/* Migration note */}
        <section className="px-4 py-16">
          <div className="mx-auto max-w-3xl">
            <Card className="border-neutral-200 bg-white/50 backdrop-blur-sm dark:border-neutral-800 dark:bg-neutral-900/50">
              <CardContent className="pt-6">
                <div className="flex items-start gap-4">
                  <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-violet-100 dark:bg-violet-900/50">
                    <Clock className="h-5 w-5 text-violet-500 dark:text-violet-400" />
                  </div>
                  <div className="min-w-0 flex-1">
                    <h2 className="mb-2 text-lg font-semibold text-neutral-900 dark:text-neutral-100">
                      {migrationTitle}
                    </h2>
                    <p className="mb-4 text-sm text-neutral-600 dark:text-neutral-400">
                      {migrationDescription}
                    </p>
                    <PortwingQuickStart />
                  </div>
                </div>
              </CardContent>
            </Card>
          </div>
        </section>

        {/* CTA */}
        <section className="px-4 py-16">
          <div className="mx-auto max-w-3xl text-center">
            <h2 className="mb-4 text-3xl font-bold tracking-tight text-neutral-900 sm:text-4xl dark:text-neutral-50">
              {`Ready to try ${SITE_CONFIG.name}?`}
            </h2>
            <p className="mb-8 text-neutral-600 dark:text-neutral-400">
              Security-first, AGPL-3.0, no SaaS required. Drop a secure foothold on any Docker host
              in minutes.
            </p>

            <CtaButtons />
          </div>
        </section>
      </MarketingShell>
    </>
  );
}
