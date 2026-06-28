"use client";

import { Check, Clock, Minus, Search, ShieldCheck, Terminal, X, Zap } from "lucide-react";
import { useState } from "react";
import { CtaButtons } from "@/components/cta-buttons";
import { GitHubBadges } from "@/components/github-badges";
import { MarketingShell } from "@/components/marketing-shell";
import { PortwingMascot } from "@/components/portwing-mascot";
import { SectionHeading } from "@/components/section-heading";
import { Badge } from "@/components/ui/badge";
import { GITHUB_ORG_URL, SITE_CONFIG } from "@/lib/site-config";
import { comparisonRows } from "./data/comparison-rows";
import { type FeatureCategory, features } from "./data/features";

// ── Data ─────────────────────────────────────────────────────────────────────

const categoryOrder: FeatureCategory[] = ["security", "control", "operations"];

const categoryLabels: Record<FeatureCategory, { label: string; color: string; border: string }> = {
  security: {
    label: "Security",
    color: "text-rose-600 dark:text-rose-400",
    border: "border-rose-500/30",
  },
  control: {
    label: "Control",
    color: "text-indigo-600 dark:text-indigo-400",
    border: "border-indigo-500/30",
  },
  operations: {
    label: "Operations",
    color: "text-amber-600 dark:text-amber-400",
    border: "border-amber-500/30",
  },
};

// ── Ecosystem stack pills ─────────────────────────────────────────────────────

const GH = GITHUB_ORG_URL;

type StackItem = {
  name: string;
  role: string;
  href: string | null;
  current?: boolean;
};

const STACK: StackItem[] = [
  {
    name: "Drydock",
    role: "orchestrator",
    href: `${GH}/drydock`,
  },
  {
    name: "Portwing",
    role: "remote agent",
    href: null,
    current: true,
  },
  {
    name: "Sockguard",
    role: "socket filter",
    href: `${GH}/sockguard`,
  },
];

// ── Quick-start snippets ──────────────────────────────────────────────────────

const dockerRun = `# Generate a token first:
openssl rand -hex 32 > portwing_token.txt

docker run -d \\
  --name portwing \\
  -p 3000:3000 \\
  --read-only \\
  --cap-drop ALL \\
  --security-opt no-new-privileges:true \\
  -v /var/run/docker.sock:/var/run/docker.sock \\
  -e TOKEN_FILE=/run/secrets/portwing_token \\
  -v ./portwing_token.txt:/run/secrets/portwing_token:ro \\
  ${SITE_CONFIG.dockerImage}:latest`;

const dockerCompose = `# Portwing + sockguard — two-layer defense.
# Generate a token first:  openssl rand -hex 32 > portwing_token.txt
services:
  sockguard:
    image: ghcr.io/codeswhat/sockguard:latest
    restart: unless-stopped
    read_only: true
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - ./sockguard.yaml:/etc/sockguard/sockguard.yaml:ro
      - sockguard-socket:/var/run/sockguard
    environment:
      - SOCKGUARD_LISTEN_SOCKET=/var/run/sockguard/sockguard.sock

  portwing:
    image: ${SITE_CONFIG.dockerImage}:latest
    restart: unless-stopped
    depends_on: [sockguard]
    read_only: true
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
    tmpfs: [/tmp]
    ports: ["3000:3000"]
    volumes:
      - sockguard-socket:/var/run/sockguard:ro
      - portwing-stacks:/data/stacks
    environment:
      - DOCKER_SOCKET=/var/run/sockguard/sockguard.sock
      - TOKEN_FILE=/run/secrets/portwing_token
    secrets: [portwing_token]

secrets:
  portwing_token:
    file: ./portwing_token.txt

volumes:
  sockguard-socket:
  portwing-stacks:`;

// ── Comparison cell ───────────────────────────────────────────────────────────

function ComparisonCell({ value, planned }: { value: string; planned?: boolean }) {
  if (planned) {
    return (
      <Badge variant="outline" className="border-amber-500/30 text-amber-600 dark:text-amber-400">
        <Clock className="size-3" />
        {value}
      </Badge>
    );
  }
  if (value === "Yes") {
    return (
      <span className="inline-flex items-center gap-1 text-emerald-600 dark:text-emerald-400">
        <Check className="size-4" />
        Yes
      </span>
    );
  }
  if (value === "No") {
    return (
      <span className="inline-flex items-center gap-1 text-neutral-400 dark:text-neutral-600">
        <X className="size-4" />
        No
      </span>
    );
  }
  if (value === "—") {
    return <Minus className="size-4 mx-auto text-neutral-300 dark:text-neutral-700" />;
  }
  return <span className="text-neutral-600 dark:text-neutral-400">{value}</span>;
}

// ── Quick-start toggle ────────────────────────────────────────────────────────

type Preset = "quick" | "secure";

const PRESETS: { id: Preset; label: string; icon: typeof Zap }[] = [
  { id: "quick", label: "Quick", icon: Zap },
  { id: "secure", label: "With sockguard", icon: ShieldCheck },
];

function CodeCard({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="rounded-2xl border border-neutral-800 bg-neutral-950 shadow-2xl ring-1 ring-black/5 dark:ring-white/10">
      <div className="px-6 pt-5">
        <div className="mb-3 flex items-center gap-2 text-neutral-500">
          <Terminal className="h-4 w-4" />
          <span className="font-mono text-xs font-medium uppercase tracking-wider">{label}</span>
        </div>
        <pre className="overflow-x-auto pb-6 text-sm leading-relaxed">{children}</pre>
      </div>
    </div>
  );
}

function QuickSnippet() {
  return (
    <CodeCard label="Quick start · docker run">
      <code className="text-neutral-300 whitespace-pre">{dockerRun}</code>
    </CodeCard>
  );
}

function SecureSnippet() {
  return (
    <CodeCard label="Hardened · compose.yml">
      <code className="text-neutral-300 whitespace-pre">{dockerCompose}</code>
    </CodeCard>
  );
}

// ── Page ──────────────────────────────────────────────────────────────────────

export default function Home() {
  const [featureQuery, setFeatureQuery] = useState("");
  const [preset, setPreset] = useState<Preset>("quick");

  const q = featureQuery.toLowerCase().trim();
  const filtered = features.filter(
    (f) => q === "" || f.title.toLowerCase().includes(q) || f.description.toLowerCase().includes(q),
  );
  const groups = categoryOrder
    .map((cat) => ({
      cat,
      ...categoryLabels[cat],
      items: filtered.filter((f) => f.category === cat),
    }))
    .filter((g) => g.items.length > 0);

  return (
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
              request, and a tamper-evident audit log records everything it touches, so you control
              your fleet without exposing it.
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

      {/* ── Ecosystem band ──────────────────────────────────────────────────── */}
      <div className="reveal">
        <section className="border-t border-border/60 px-4 py-14">
          <div className="mx-auto max-w-5xl px-4">
            <SectionHeading
              eyebrow="Ecosystem"
              title="Part of the CodesWhat stack"
              subtitle="Three tools, one job each. Built to work together."
              align="left"
            />
            <div className="overflow-hidden rounded-2xl border border-neutral-200 bg-white/50 backdrop-blur-sm dark:border-neutral-800 dark:bg-neutral-900/50">
              <div className="grid gap-0 sm:grid-cols-3 sm:divide-x sm:divide-neutral-200 sm:dark:divide-neutral-800">
                {STACK.map((item) => {
                  const inner = (
                    <div key={item.name} className="flex flex-col gap-1.5 px-8 py-8">
                      <div className="flex items-center gap-2">
                        <span className="font-mono text-base font-semibold text-neutral-900 dark:text-neutral-100">
                          {item.name}
                        </span>
                        {item.current && (
                          <span
                            className="text-base leading-none"
                            role="img"
                            aria-label="You're here"
                          >
                            📍
                          </span>
                        )}
                      </div>
                      <p className="text-xs text-neutral-500 dark:text-neutral-400">{item.role}</p>
                    </div>
                  );

                  return item.href ? (
                    <a
                      key={item.name}
                      href={item.href}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="group block border-b border-neutral-200 transition-colors hover:bg-neutral-50 last:border-0 sm:border-b-0 dark:border-neutral-800 dark:hover:bg-neutral-900/60"
                    >
                      {inner}
                    </a>
                  ) : (
                    <div
                      key={item.name}
                      className="border-b border-neutral-200 last:border-0 sm:border-b-0 dark:border-neutral-800"
                    >
                      {inner}
                    </div>
                  );
                })}
              </div>
            </div>
          </div>
        </section>
      </div>

      {/* ── Features ─────────────────────────────────────────────────────────── */}
      <div className="reveal">
        <section className="border-t border-border/60 px-4 py-16">
          <div className="mx-auto max-w-6xl px-4">
            <SectionHeading
              eyebrow="What it does"
              title="14 reasons your Docker hosts are safer"
              subtitle="Security isn't a checkbox — it's every layer. Here's what we built."
              align="left"
            />

            <div className="mx-auto max-w-2xl">
              {/* Command-palette panel */}
              <div className="overflow-hidden rounded-2xl border border-neutral-200 bg-white/50 shadow-xl shadow-black/5 backdrop-blur-sm dark:border-neutral-800 dark:bg-neutral-900/50 dark:shadow-black/20">
                {/* Search row */}
                <div className="flex items-center gap-3 border-b border-neutral-200 px-4 py-3 dark:border-neutral-800">
                  <Search className="h-4 w-4 shrink-0 text-neutral-400 dark:text-neutral-500" />
                  <input
                    type="text"
                    value={featureQuery}
                    onChange={(e) => setFeatureQuery(e.target.value)}
                    placeholder="Search capabilities…"
                    aria-label="Search capabilities"
                    className="flex-1 bg-transparent text-sm text-neutral-700 placeholder-neutral-400 outline-none dark:text-neutral-300 dark:placeholder-neutral-600"
                    autoComplete="off"
                    spellCheck={false}
                  />
                  <kbd className="shrink-0 rounded border border-neutral-200 bg-neutral-100 px-1.5 py-0.5 font-mono text-[10px] font-semibold text-neutral-400 dark:border-neutral-700 dark:bg-neutral-800 dark:text-neutral-500">
                    ⌘K
                  </kbd>
                </div>

                {/* Results */}
                <div className="max-h-[560px] overflow-y-auto py-2">
                  {groups.length === 0 && (
                    <p className="px-4 py-8 text-center text-sm text-neutral-400 dark:text-neutral-600">
                      No capabilities match &ldquo;{featureQuery}&rdquo;
                    </p>
                  )}
                  {groups.map((group) => (
                    <div key={group.cat}>
                      <div className="px-4 pb-1 pt-3">
                        <span
                          className={`font-mono text-[10px] font-semibold uppercase tracking-widest ${group.color}`}
                        >
                          {group.label}
                        </span>
                      </div>
                      {group.items.map((feature) => (
                        <div
                          key={feature.title}
                          className="group flex cursor-default items-center gap-3 px-3 py-2.5 hover:bg-neutral-100/70 dark:hover:bg-neutral-800/70"
                        >
                          <div
                            className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-lg ${feature.bg}`}
                          >
                            <feature.icon size={15} className={feature.color} />
                          </div>
                          <div className="min-w-0 flex-1">
                            <p className="text-sm font-semibold text-neutral-800 dark:text-neutral-200">
                              {feature.title}
                            </p>
                            <p className="truncate text-xs text-neutral-500 dark:text-neutral-400">
                              {feature.description}
                            </p>
                          </div>
                          <span className="shrink-0 font-mono text-xs text-neutral-300 opacity-0 transition-opacity group-hover:opacity-100 dark:text-neutral-600">
                            ↵
                          </span>
                        </div>
                      ))}
                    </div>
                  ))}
                </div>

                {/* Footer bar */}
                <div className="flex items-center gap-4 border-t border-neutral-200 px-4 py-2.5 dark:border-neutral-800">
                  <span className="font-mono text-[10px] text-neutral-400 dark:text-neutral-600">
                    {filtered.length} of {features.length} capabilities
                  </span>
                  <div className="ml-auto flex items-center gap-3">
                    <span className="flex items-center gap-1 font-mono text-[10px] text-neutral-400 dark:text-neutral-600">
                      <kbd className="rounded border border-neutral-200 bg-neutral-100 px-1 py-px font-mono text-[9px] dark:border-neutral-700 dark:bg-neutral-800">
                        ↑↓
                      </kbd>{" "}
                      navigate
                    </span>
                    <span className="flex items-center gap-1 font-mono text-[10px] text-neutral-400 dark:text-neutral-600">
                      <kbd className="rounded border border-neutral-200 bg-neutral-100 px-1 py-px font-mono text-[9px] dark:border-neutral-700 dark:bg-neutral-800">
                        esc
                      </kbd>{" "}
                      close
                    </span>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </section>
      </div>

      {/* ── Quick Start ───────────────────────────────────────────────────────── */}
      <div className="reveal">
        <section className="border-t border-border/60 px-4 py-20">
          <div className="mx-auto max-w-3xl">
            <SectionHeading
              eyebrow="Get running"
              title="Up in two commands"
              subtitle="Quick path or hardened path — pick one and go."
              align="right"
            />

            {/* Toggle */}
            <div className="mb-5 flex justify-center">
              <div
                role="tablist"
                aria-label="Install preset"
                className="inline-flex gap-1 rounded-xl border border-neutral-200 bg-white/60 p-1 backdrop-blur-sm dark:border-neutral-800 dark:bg-neutral-900/60"
                onKeyDown={(e) => {
                  const currentIndex = PRESETS.findIndex((p) => p.id === preset);
                  if (e.key === "ArrowRight") {
                    e.preventDefault();
                    setPreset(PRESETS[(currentIndex + 1) % PRESETS.length].id);
                  } else if (e.key === "ArrowLeft") {
                    e.preventDefault();
                    setPreset(PRESETS[(currentIndex - 1 + PRESETS.length) % PRESETS.length].id);
                  }
                }}
              >
                {PRESETS.map(({ id, label, icon: Icon }) => {
                  const active = preset === id;
                  return (
                    <button
                      key={id}
                      id={`tab-${id}`}
                      type="button"
                      role="tab"
                      aria-selected={active}
                      aria-controls="preset-panel"
                      tabIndex={active ? 0 : -1}
                      onClick={() => setPreset(id)}
                      className={[
                        "flex items-center gap-1.5 rounded-lg px-4 py-1.5 text-sm font-medium transition-colors",
                        active
                          ? "bg-neutral-900 text-white dark:bg-neutral-100 dark:text-neutral-900"
                          : "text-neutral-500 hover:text-neutral-900 dark:text-neutral-400 dark:hover:text-neutral-100",
                      ].join(" ")}
                    >
                      <Icon className="h-3.5 w-3.5" />
                      {label}
                    </button>
                  );
                })}
              </div>
            </div>

            <div role="tabpanel" id="preset-panel" aria-labelledby={`tab-${preset}`}>
              {preset === "quick" ? <QuickSnippet /> : <SecureSnippet />}

              <div className="mt-4 flex items-start justify-center gap-2 px-2 text-center text-sm">
                {preset === "quick" ? (
                  <p className="flex items-center gap-2 text-neutral-500 dark:text-neutral-400">
                    <Zap className="h-4 w-4 shrink-0 text-amber-500" />
                    Mounts the raw socket — fine for a local try, swap in sockguard for production.
                  </p>
                ) : (
                  <p className="flex items-center gap-2 text-neutral-500 dark:text-neutral-400">
                    <ShieldCheck className="h-4 w-4 shrink-0 text-emerald-500" />
                    Portwing never touches the raw socket. Sockguard enforces a Docker API allowlist
                    at the socket level.
                  </p>
                )}
              </div>
            </div>
          </div>
        </section>
      </div>

      {/* ── Comparison ────────────────────────────────────────────────────────── */}
      <div className="reveal">
        <section id="compare" className="border-t border-border/60 px-4 py-16">
          <div className="mx-auto max-w-5xl px-4">
            <SectionHeading
              eyebrow="Alternatives"
              title="How we compare"
              subtitle="We compared against each tool's published, default behaviour. Not worst-case configurations, not upsell tiers."
              align="left"
            />

            <div className="overflow-x-auto rounded-2xl border border-neutral-200 bg-white/50 backdrop-blur-sm dark:border-neutral-800 dark:bg-neutral-900/50">
              <table className="w-full min-w-[780px] text-sm">
                <thead>
                  <tr className="border-b border-neutral-200 dark:border-neutral-800">
                    <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-neutral-500 sm:px-5 dark:text-neutral-400">
                      Feature
                    </th>
                    {["Portainer", "Komodo", "Hawser", "Watchtower", "Diun"].map((name) => (
                      <th
                        key={name}
                        className="px-3 py-3 text-center text-xs font-semibold uppercase tracking-wider text-neutral-500 dark:text-neutral-400"
                      >
                        {name}
                      </th>
                    ))}
                    <th className="px-3 py-3 text-center text-xs font-semibold uppercase tracking-wider text-neutral-900 dark:text-neutral-100">
                      Portwing
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {comparisonRows.map((row, i) => (
                    <tr
                      key={row.feature}
                      className={
                        i < comparisonRows.length - 1
                          ? "border-b border-neutral-100 transition-colors hover:bg-neutral-50 dark:border-neutral-800/50 dark:hover:bg-neutral-900/30"
                          : "transition-colors hover:bg-neutral-50 dark:hover:bg-neutral-900/30"
                      }
                    >
                      <td className="px-4 py-3 font-medium text-neutral-900 sm:px-5 dark:text-neutral-100">
                        {row.feature}
                      </td>
                      <td className="px-3 py-3 text-center">
                        <ComparisonCell value={row.portainer} />
                      </td>
                      <td className="px-3 py-3 text-center">
                        <ComparisonCell value={row.komodo} />
                      </td>
                      <td className="px-3 py-3 text-center">
                        <ComparisonCell value={row.hawser} />
                      </td>
                      <td className="px-3 py-3 text-center">
                        <ComparisonCell value={row.watchtower} />
                      </td>
                      <td className="px-3 py-3 text-center">
                        <ComparisonCell value={row.diun} />
                      </td>
                      <td className="px-3 py-3 text-center">
                        <ComparisonCell value={row.portwing} planned={row.planned} />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        </section>
      </div>
    </MarketingShell>
  );
}
