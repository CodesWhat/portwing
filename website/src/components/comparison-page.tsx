// SPDX-License-Identifier: MIT

import type { ReactNode } from "react";
import { CopyButton } from "@/components/copy-button";
import { CtaButtons } from "@/components/cta-buttons";
import { Footer } from "@/components/footer";
import { Ic } from "@/components/icons";
import { SiteHeader } from "@/components/site-header";
import { SITE_CONFIG } from "@/lib/site-config";

export type ComparisonRow = {
  feature: string;
  competitor: string;
  self: string;
  verdict: "self" | "competitor" | "tie";
};

export type Highlight = {
  /** Phosphor icon id from components/icon-symbols.ts. */
  icon: string;
  title: string;
  description: string;
};

type Props = {
  competitorName: string;
  heroTitle: string;
  heroDescription: ReactNode;
  competitorBadge: string;
  selfBadge: string;
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

type Verdict = { cls: string; icon: string; prefix: string };

const GOOD: Verdict = { cls: "good", icon: "check", prefix: "Stronger: " };
const GAP: Verdict = { cls: "gap", icon: "warning", prefix: "Weaker: " };
const TIE: Verdict = { cls: "tie", icon: "minus", prefix: "On par: " };
const NONE: Verdict = { cls: "no", icon: "x", prefix: "Not available: " };

function Cell({ verdict, text }: { verdict: Verdict; text: string }) {
  return (
    <td>
      <span className={`v ${verdict.cls}`}>
        <Ic name={verdict.icon} />
        <span className="sr">{verdict.prefix}</span>
        <span>{text}</span>
      </span>
    </td>
  );
}

function competitorVerdict(row: ComparisonRow): Verdict {
  if (row.competitor === "No" || row.competitor === "None") return NONE;
  if (row.verdict === "self") return GAP;
  if (row.verdict === "competitor") return GOOD;
  return TIE;
}

function selfVerdict(row: ComparisonRow): Verdict {
  if (row.verdict === "self") return GOOD;
  if (row.verdict === "competitor") return GAP;
  return TIE;
}

// The one command every comparison page offers for moving onto Portwing.
const QUICK_START = `docker run -d \\
  --name portwing \\
  --read-only --cap-drop=ALL \\
  --security-opt no-new-privileges:true \\
  -v /var/run/docker.sock:/var/run/docker.sock:ro \\
  -e TOKEN_HASH="$PORTWING_TOKEN_HASH" \\
  -p 3000:3000 \\
  ${SITE_CONFIG.dockerImage}:${SITE_CONFIG.version}`;

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
  const name = SITE_CONFIG.name;
  return (
    <>
      <JsonLdScript data={jsonLd} />
      <SiteHeader current="compare" />
      <main id="main-content">
        <section className="cmp-hero" aria-labelledby="vs-h1">
          <div className="wrap">
            <h1 className="cp" id="vs-h1">
              {heroTitle}
            </h1>
            <div className="cp-sub">{heroDescription}</div>
            <div className="badges2">
              <span className="chip t-info nocase">
                <Ic name="check" />
                {competitorBadge}
              </span>
              <span className="chip t-primary nocase">
                <Ic name="check" />
                {selfBadge}
              </span>
            </div>
          </div>
        </section>

        <section className="cs" aria-labelledby="vs-ft">
          <div className="wrap">
            <div className="sec-h">
              <h2 className="title" id="vs-ft">
                Feature Comparison
              </h2>
              <p className="sub">Here's how we compare on the features that matter most.</p>
            </div>
            <div className="mx">
              <div
                className="scroll"
                tabIndex={0}
                role="region"
                aria-label={`${competitorName} vs ${name} feature table. Scroll sideways if it does not fit.`}
              >
                <table className="cmp vs mxt">
                  <caption className="sr">
                    {competitorName} vs {name}: {comparisonData.length} features
                  </caption>
                  <thead>
                    <tr>
                      <th scope="col">Feature</th>
                      <th scope="col">{competitorName}</th>
                      <th scope="col" className="me">
                        {name}
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {comparisonData.map((row) => (
                      <tr key={row.feature}>
                        <th scope="row">{row.feature}</th>
                        <Cell verdict={competitorVerdict(row)} text={row.competitor} />
                        <Cell verdict={selfVerdict(row)} text={row.self} />
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          </div>
        </section>

        <section className="cs" aria-labelledby="vs-d">
          <div className="wrap">
            <div className="sec-h">
              <h2 className="title" id="vs-d">
                Comparison Details
              </h2>
              <p className="sub">
                Authentication, socket policy, telemetry, release evidence, and controller scope.
              </p>
            </div>
            <ul className="cards3" style={{ listStyle: "none", margin: 0, padding: 0 }}>
              {highlights.map((highlight) => (
                <li className="hc" key={highlight.title}>
                  <div className="hh">
                    <span className="tn">
                      <Ic name={highlight.icon} />
                    </span>
                    <h3>{highlight.title}</h3>
                  </div>
                  <p>{highlight.description}</p>
                </li>
              ))}
            </ul>
          </div>
        </section>

        <section className="cs" aria-labelledby="vs-m">
          <div className="wrap">
            <div className="mig">
              <span className="tn">
                <Ic name="clock-counter-clockwise" />
              </span>
              <div className="migc" style={{ flex: 1, minWidth: 0 }}>
                <div>
                  <h3 id="vs-m">{migrationTitle}</h3>
                  <p>{migrationDescription}</p>
                  <figure className="cb" style={{ margin: 0 }}>
                    <div className="cb-h">
                      <span className="cb-lang">
                        <Ic name="terminal-window" />
                        Quick start
                      </span>
                      <CopyButton
                        text={QUICK_START}
                        label={`Copy the ${name} quick start command`}
                      />
                    </div>
                    <pre className="pr" tabIndex={0}>
                      <code>{`$ ${QUICK_START}`}</code>
                    </pre>
                  </figure>
                  <p className="src">
                    Needs <code>TOKEN_HASH</code>: an Argon2id hash from{" "}
                    <code>portwing hash-token</code>. For per-client keys, run{" "}
                    <code>portwing keygen</code> and set <code>AUTHORIZED_KEYS</code> instead.
                  </p>
                </div>
              </div>
            </div>
          </div>
        </section>

        <section className="cs" aria-labelledby="vs-cta" style={{ paddingBottom: 72 }}>
          <div className="wrap">
            <div className="cta2">
              <h2 id="vs-cta">Ready to try {name}?</h2>
              <p>
                Security-first, MIT-licensed, no SaaS required. Drop a secure foothold on any Docker
                host in minutes.
              </p>
              <CtaButtons />
            </div>
          </div>
        </section>
      </main>
      <Footer />
    </>
  );
}
