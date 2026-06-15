import {
  BookOpen,
  BookOpenText,
  Check,
  ChevronDown,
  Clock,
  Minus,
  Terminal,
} from "lucide-react";
import Image from "next/image";
import Link from "next/link";
import { PortwingMascot } from "@/components/portwing-mascot";
import { ThemeToggle } from "@/components/theme-toggle";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { comparisonRows } from "./data/comparison-rows";
import { type FeatureCategory, features } from "./data/features";

const REPO = "https://github.com/CodesWhat/portwing";
const DOCS = "/docs";

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
    image: ghcr.io/codeswhat/portwing:latest
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
        <Minus className="size-4" />
        No
      </span>
    );
  }
  return <span className="text-neutral-600 dark:text-neutral-400">{value}</span>;
}

export default function Home() {
  return (
    <main className="relative min-h-screen bg-gradient-to-br from-background to-secondary">
      {/* Background Pattern */}
      <div className="bg-grid-neutral-200/50 dark:bg-grid-neutral-800/50 fixed inset-0" />

      <div className="relative z-10">
        {/* Theme Toggle */}
        <div className="fixed top-4 right-4 z-50">
          <ThemeToggle />
        </div>

        {/* Hero Section */}
        <section className="relative flex min-h-screen flex-col items-center justify-center px-4 py-10">
          <div className="pointer-events-none absolute inset-0 bg-[radial-gradient(ellipse_at_center,_hsl(var(--background))_18%,_transparent_70%)]" />

          <div className="relative z-10 flex flex-col items-center">
            <div className="mb-8">
              <PortwingMascot size={168} />
            </div>

            <Badge variant="secondary" className="mb-6 px-4 py-1.5 text-sm font-medium">
              Open Source &middot; AGPL-3.0 &middot; Alpha
            </Badge>

            <div className="max-w-4xl text-center">
              <h1 className="mb-4 text-5xl font-bold tracking-tight text-foreground sm:text-6xl lg:text-7xl">
                Remote Docker
                <br />
                <span className="text-primary">Agent</span>
              </h1>

              <p className="mx-auto mb-10 max-w-2xl text-lg text-muted-foreground sm:text-xl">
                Control your containers from anywhere, safely. A security-first agent that talks to
                Docker over a <strong className="text-foreground">default-deny socket</strong>, with
                Ed25519 per-client auth, signed supply chain, and a tamper-evident audit log — the
                remote agent for{" "}
                <a
                  href="https://github.com/CodesWhat/drydock"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-foreground underline decoration-primary/40 underline-offset-2 hover:decoration-primary"
                >
                  Drydock
                </a>
                .
              </p>

              <div className="flex flex-col items-center justify-center gap-4 sm:flex-row">
                <Button size="lg" asChild>
                  <a href={REPO} target="_blank" rel="noopener noreferrer">
                    <svg
                      className="h-4 w-4"
                      viewBox="0 0 24 24"
                      fill="currentColor"
                      aria-label="GitHub"
                      role="img"
                    >
                      <path d="M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z" />
                    </svg>
                    View on GitHub
                  </a>
                </Button>
                <Button variant="outline" size="lg" asChild>
                  <a href={DOCS} target="_blank" rel="noopener noreferrer">
                    <BookOpen className="h-4 w-4" />
                    Documentation
                  </a>
                </Button>
              </div>

              {/* Distribution Badges */}
              <div className="mt-10 flex flex-wrap items-center justify-center gap-2">
                <a
                  href="https://github.com/CodesWhat/portwing/pkgs/container/portwing"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  {/* biome-ignore lint/performance/noImgElement: external badge */}
                  <img
                    src="https://img.shields.io/badge/GHCR-image-2ea44f?logo=github&logoColor=white"
                    alt="GHCR"
                  />
                </a>
                <a
                  href="https://github.com/orgs/CodesWhat/packages/container/package/portwing"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  {/* biome-ignore lint/performance/noImgElement: external badge */}
                  <img
                    src="https://img.shields.io/badge/platforms-amd64%20%7C%20arm64%20%7C%20arm%2Fv7-informational?logo=linux&logoColor=white"
                    alt="Multi-arch"
                  />
                </a>
                <a
                  href="https://github.com/orgs/CodesWhat/packages/container/package/portwing"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  {/* biome-ignore lint/performance/noImgElement: external badge */}
                  <img
                    src="https://img.shields.io/badge/image%20size-~10%20MB-informational?logo=docker&logoColor=white"
                    alt="Image size"
                  />
                </a>
                <a href={`${REPO}/blob/main/LICENSE`} target="_blank" rel="noopener noreferrer">
                  {/* biome-ignore lint/performance/noImgElement: external badge */}
                  <img
                    src="https://img.shields.io/badge/license-AGPL--3.0-C9A227"
                    alt="License AGPL-3.0"
                  />
                </a>
              </div>
              {/* Community Badges */}
              <div className="mt-3 flex flex-wrap items-center justify-center gap-2">
                <a href={`${REPO}/stargazers`} target="_blank" rel="noopener noreferrer">
                  {/* biome-ignore lint/performance/noImgElement: external badge */}
                  <img
                    src="https://img.shields.io/github/stars/CodesWhat/portwing?style=flat"
                    alt="Stars"
                  />
                </a>
                <a href={`${REPO}/forks`} target="_blank" rel="noopener noreferrer">
                  {/* biome-ignore lint/performance/noImgElement: external badge */}
                  <img
                    src="https://img.shields.io/github/forks/CodesWhat/portwing?style=flat"
                    alt="Forks"
                  />
                </a>
                <a href={`${REPO}/issues`} target="_blank" rel="noopener noreferrer">
                  {/* biome-ignore lint/performance/noImgElement: external badge */}
                  <img
                    src="https://img.shields.io/github/issues/CodesWhat/portwing?style=flat"
                    alt="Issues"
                  />
                </a>
                <a href={`${REPO}/commits/main`} target="_blank" rel="noopener noreferrer">
                  {/* biome-ignore lint/performance/noImgElement: external badge */}
                  <img
                    src="https://img.shields.io/github/last-commit/CodesWhat/portwing?style=flat"
                    alt="Last commit"
                  />
                </a>
              </div>
              {/* Quality Badges */}
              <div className="mt-3 flex flex-wrap items-center justify-center gap-2">
                <a
                  href={`${REPO}/actions/workflows/ci.yml`}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  {/* biome-ignore lint/performance/noImgElement: external badge */}
                  <img
                    src="https://github.com/CodesWhat/portwing/actions/workflows/ci.yml/badge.svg?branch=main"
                    alt="CI"
                  />
                </a>
                <a
                  href="https://goreportcard.com/report/github.com/codeswhat/portwing"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  {/* biome-ignore lint/performance/noImgElement: external badge */}
                  <img
                    src="https://goreportcard.com/badge/github.com/codeswhat/portwing"
                    alt="Go Report Card"
                  />
                </a>
                <a
                  href="https://pkg.go.dev/github.com/codeswhat/portwing"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  {/* biome-ignore lint/performance/noImgElement: external badge */}
                  <img
                    src="https://pkg.go.dev/badge/github.com/codeswhat/portwing.svg"
                    alt="Go Reference"
                  />
                </a>
              </div>
            </div>

            {/* Scroll Indicator */}
            <div className="mt-20 animate-bounce">
              <ChevronDown className="h-10 w-10 text-primary drop-shadow-[0_0_8px_hsl(var(--primary)/0.5)]" />
            </div>
          </div>
        </section>

        {/* Ecosystem band */}
        <section className="border-y border-border bg-card/40 px-4 py-10 backdrop-blur-sm">
          <div className="mx-auto flex max-w-5xl flex-col items-center gap-2 text-center">
            <span className="font-mono text-xs uppercase tracking-widest text-muted-foreground">
              Part of the CodesWhat stack
            </span>
            <p className="text-sm text-muted-foreground sm:text-base">
              <a
                href="https://github.com/CodesWhat/drydock"
                target="_blank"
                rel="noopener noreferrer"
                className="font-semibold text-foreground hover:text-primary"
              >
                Drydock
              </a>{" "}
              orchestrates ·{" "}
              <span className="font-semibold text-foreground">Portwing</span> is the agent on every
              host ·{" "}
              <a
                href="https://github.com/CodesWhat/sockguard"
                target="_blank"
                rel="noopener noreferrer"
                className="font-semibold text-foreground hover:text-primary"
              >
                Sockguard
              </a>{" "}
              filters the socket
            </p>
          </div>
        </section>

        {/* Features Section */}
        <section className="px-4 py-24">
          <div className="mx-auto max-w-5xl">
            <div className="relative mb-16 text-center">
              <div className="pointer-events-none absolute inset-0 bg-[radial-gradient(ellipse_at_center,_hsl(var(--background))_20%,_transparent_50%)]" />
              <h2 className="relative text-3xl font-bold tracking-tight text-foreground sm:text-4xl">
                Features
              </h2>
              <p className="relative mt-4 text-muted-foreground">
                A watchtower, not a back door — a lean Go binary that proves what it did
              </p>
            </div>

            <div className="overflow-hidden rounded-xl border border-border">
              <div className="flex items-center gap-2 border-b border-border bg-secondary px-5 py-3">
                <div className="h-2.5 w-2.5 rounded-full bg-emerald-500" />
                <span className="font-mono text-xs text-muted-foreground">portwing capabilities</span>
                <span className="ml-auto font-mono text-xs text-muted-foreground/60">
                  {features.length} modules
                </span>
              </div>
              <div className="divide-y divide-border bg-card">
                {features.map((feature, i) => {
                  const cat = categoryLabels[feature.category];
                  return (
                    <div
                      key={feature.title}
                      className="group flex items-center gap-5 px-5 py-4 transition-colors hover:bg-secondary/60"
                    >
                      <span className="w-6 shrink-0 text-right font-mono text-xs tabular-nums text-muted-foreground/40">
                        {String(i + 1).padStart(2, "0")}
                      </span>
                      <div
                        className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-md ${feature.bg}`}
                      >
                        <feature.icon className={`h-4 w-4 ${feature.color}`} />
                      </div>
                      <div className="min-w-0 flex-1">
                        <div className="flex items-baseline gap-3">
                          <h3 className="text-sm font-semibold text-foreground">{feature.title}</h3>
                          <span
                            className={`rounded-full border px-2 py-0.5 font-mono text-[10px] uppercase tracking-wider ${cat.border} ${cat.color}`}
                          >
                            {cat.label}
                          </span>
                        </div>
                        <p className="mt-0.5 text-xs text-muted-foreground">
                          {feature.description}
                        </p>
                      </div>
                    </div>
                  );
                })}
              </div>
            </div>
          </div>
        </section>

        {/* Quick Start Section */}
        <section className="px-4 py-24">
          <div className="mx-auto max-w-3xl">
            <div className="relative mb-16 text-center">
              <div className="pointer-events-none absolute inset-0 bg-[radial-gradient(ellipse_at_center,_hsl(var(--background))_20%,_transparent_50%)]" />
              <h2 className="relative text-3xl font-bold tracking-tight text-foreground sm:text-4xl">
                Quick Start
              </h2>
              <p className="relative mt-4 text-muted-foreground">
                Two hardened containers sharing a filtered socket — and you&apos;re done
              </p>
            </div>

            <div className="overflow-hidden rounded-xl border border-neutral-800 bg-neutral-950 shadow-2xl">
              <div className="flex items-center gap-2 border-b border-neutral-800 px-4 py-3">
                <Terminal className="h-4 w-4 text-neutral-500" />
                <span className="text-xs font-medium text-neutral-500">
                  docker-compose.with-sockguard.yml
                </span>
              </div>
              <pre className="overflow-x-auto p-6 font-[family-name:var(--font-mono)] text-sm leading-relaxed text-neutral-300">
                {dockerCompose}
              </pre>
            </div>
          </div>
        </section>

        {/* Comparison Section */}
        <section className="px-4 py-24">
          <div className="mx-auto max-w-5xl">
            <div className="relative mb-16 text-center">
              <div className="pointer-events-none absolute inset-0 bg-[radial-gradient(ellipse_at_center,_hsl(var(--background))_20%,_transparent_50%)]" />
              <h2 className="relative text-3xl font-bold tracking-tight text-foreground sm:text-4xl">
                Comparison
              </h2>
              <p className="relative mt-4 text-muted-foreground">
                How Portwing stacks up against other remote Docker agents
              </p>
            </div>

            <div className="overflow-x-auto rounded-xl border border-border bg-card/50 backdrop-blur-sm">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-border">
                    <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                      Feature
                    </th>
                    <th className="px-4 py-3 text-center text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                      Portainer
                    </th>
                    <th className="px-4 py-3 text-center text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                      Komodo
                    </th>
                    <th className="px-4 py-3 text-center text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                      Hawser
                    </th>
                    <th className="px-4 py-3 text-center text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                      Watchtower
                    </th>
                    <th className="px-4 py-3 text-center text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                      Diun
                    </th>
                    <th className="px-4 py-3 text-center text-xs font-semibold uppercase tracking-wider text-foreground">
                      Portwing
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {comparisonRows.map((row) => (
                    <tr
                      key={row.feature}
                      className="border-b border-border/60 transition-colors hover:bg-secondary/50 last:border-0"
                    >
                      <td className="px-4 py-3 font-medium text-foreground">{row.feature}</td>
                      <td className="px-4 py-3 text-center">
                        <ComparisonCell value={row.portainer} />
                      </td>
                      <td className="px-4 py-3 text-center">
                        <ComparisonCell value={row.komodo} />
                      </td>
                      <td className="px-4 py-3 text-center">
                        <ComparisonCell value={row.hawser} />
                      </td>
                      <td className="px-4 py-3 text-center">
                        <ComparisonCell value={row.watchtower} />
                      </td>
                      <td className="px-4 py-3 text-center">
                        <ComparisonCell value={row.diun} />
                      </td>
                      <td className="px-4 py-3 text-center">
                        <ComparisonCell value={row.portwing} planned={row.planned} />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        </section>

        {/* Footer */}
        <footer className="px-4 py-8">
          <div className="mx-auto flex max-w-5xl items-center justify-between">
            <div className="flex items-center gap-3">
              <a href="https://github.com/CodesWhat" target="_blank" rel="noopener noreferrer">
                <Image
                  src="/codeswhat-logo.png"
                  alt="CodesWhat"
                  width={28}
                  height={28}
                  className="dark:invert"
                />
              </a>
              <span className="text-sm text-muted-foreground">
                &copy; 2026 CodesWhat. AGPL-3.0 License.
              </span>
            </div>
            <div className="flex items-center gap-4">
              {/* biome-ignore lint/a11y/useAnchorContent: aria-label provides accessible name */}
              <a
                href={REPO}
                target="_blank"
                rel="noopener noreferrer"
                className="text-muted-foreground transition-colors hover:text-foreground"
                aria-label="GitHub"
              >
                <svg className="h-5 w-5" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
                  <path d="M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z" />
                </svg>
              </a>
              <a
                href={DOCS}
                target="_blank"
                rel="noopener noreferrer"
                className="text-muted-foreground transition-colors hover:text-foreground"
                aria-label="Documentation"
              >
                <BookOpenText className="h-5 w-5" />
              </a>
            </div>
          </div>
        </footer>
      </div>
    </main>
  );
}
