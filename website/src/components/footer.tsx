// SPDX-License-Identifier: MIT

import type { CtaId } from "@codeswhat/public-analytics";
import Link from "next/link";
import { Ic } from "@/components/icons";
import { TrackedLink } from "@/components/tracked-link";
import {
  GHCR_PACKAGE_URL,
  GITHUB_ORG_URL,
  GITHUB_RELEASES_URL,
  GITHUB_URL,
  SITE_CONFIG,
} from "@/lib/site-config";

const YEAR = process.env.NEXT_PUBLIC_COPYRIGHT_YEAR;
const BLURB =
  "Security-first remote Docker agent. Lightweight Go binary, Ed25519 auth, sockguard-ready socket filter, structured audit log.";
const GO_REFERENCE_URL = "https://pkg.go.dev/github.com/codeswhat/portwing";

type FooterLink = { label: string; href: string; ctaId?: CtaId };

const PROJECT_SHORT: FooterLink[] = [
  { label: "Documentation", href: "/docs", ctaId: "docs_root" },
  { label: "Compare", href: "/compare" },
  { label: "GitHub", href: GITHUB_URL, ctaId: "github_repository" },
  { label: "Releases", href: GITHUB_RELEASES_URL },
  { label: "License", href: SITE_CONFIG.licenseUrl },
];

const PROJECT_FULL: FooterLink[] = [
  { label: "Documentation", href: "/docs", ctaId: "docs_root" },
  { label: "GitHub", href: GITHUB_URL, ctaId: "github_repository" },
  { label: "Releases", href: GITHUB_RELEASES_URL },
  { label: "License", href: SITE_CONFIG.licenseUrl },
  { label: "Discord", href: SITE_CONFIG.discordUrl, ctaId: "community_discord" },
  { label: "GHCR package", href: GHCR_PACKAGE_URL },
  { label: "Go Reference", href: GO_REFERENCE_URL },
];

const COMPARE: FooterLink[] = [
  { label: "All comparisons", href: "/compare" },
  { label: "Portainer vs Portwing", href: "/compare/portainer" },
  { label: "Komodo vs Portwing", href: "/compare/komodo" },
  { label: "Arcane vs Portwing", href: "/compare/arcane" },
  { label: "Hawser vs Portwing", href: "/compare/hawser" },
  { label: "Watchtower vs Portwing", href: "/compare/watchtower" },
  { label: "Diun vs Portwing", href: "/compare/diun" },
];

const DOCS: FooterLink[] = [
  { label: "Introduction", href: "/docs" },
  { label: "Getting started", href: "/docs/getting-started" },
  { label: "Native Installation", href: "/docs/installation" },
  { label: "Connection modes", href: "/docs/connection-modes" },
  { label: "Authentication", href: "/docs/authentication" },
  { label: "Configuration", href: "/docs/configuration" },
  { label: "Stability policy", href: "/docs/stability-policy" },
  { label: "Security model", href: "/docs/security-model" },
  { label: "Drydock integration", href: "/docs/drydock-integration" },
  { label: "Standalone mode", href: "/docs/standalone-mode" },
  { label: "MCP server", href: "/docs/mcp-server" },
  { label: "Observability", href: "/docs/observability" },
  { label: "Audit logging", href: "/docs/audit-logging" },
  { label: "API reference", href: "/docs/api-reference" },
  { label: "Verifying Releases", href: "/docs/verification" },
  { label: "Competitive landscape", href: "/docs/competitive-landscape" },
  { label: "Migrating from Watchtower", href: "/docs/migrating-from-watchtower" },
];

const SECURITY: FooterLink[] = [
  { label: "Security model", href: "/docs/security-model" },
  { label: "Authentication", href: "/docs/authentication" },
  { label: "Audit logging", href: "/docs/audit-logging" },
  { label: "Verifying Releases", href: "/docs/verification" },
  { label: "Report a vulnerability", href: "mailto:security@codeswhat.com" },
];

function FooterAnchor({ link }: { link: FooterLink }) {
  const external = link.href.startsWith("https://");
  if (link.ctaId) {
    return (
      <TrackedLink
        href={link.href}
        target={external ? "_blank" : undefined}
        rel={external ? "noopener noreferrer" : undefined}
        ctaId={link.ctaId}
        placement="footer"
      >
        {link.label}
      </TrackedLink>
    );
  }
  if (link.href.startsWith("/compare")) return <Link href={link.href}>{link.label}</Link>;
  return (
    <a
      href={link.href}
      target={external ? "_blank" : undefined}
      rel={external ? "noopener noreferrer" : undefined}
    >
      {link.label}
    </a>
  );
}

function Column({ heading, links }: { heading: string; links: FooterLink[] }) {
  return (
    <div>
      <p className="fh micro">{heading}</p>
      <ul>
        {links.map((link) => (
          <li key={link.label}>
            <FooterAnchor link={link} />
          </li>
        ))}
      </ul>
    </div>
  );
}

/** `full` is the home-page footer with the compare, docs and security columns. */
export function Footer({ full = false }: { full?: boolean }) {
  return (
    <footer id="footer">
      <div className="wrap">
        <div className="fgrid">
          <div>
            <Link className="brand" href="/">
              <img
                src="/brand/pigeon-64.webp"
                alt=""
                width={28}
                height={28}
                loading="lazy"
                decoding="async"
              />
              <span>{SITE_CONFIG.name}</span>
            </Link>
            <p className="fblurb">{BLURB}</p>
            <div className="icons">
              <TrackedLink
                href={GITHUB_URL}
                target="_blank"
                rel="noopener noreferrer"
                aria-label="GitHub"
                ctaId="github_repository"
                placement="footer"
              >
                <Ic name="github-logo" />
              </TrackedLink>
              <TrackedLink
                href={SITE_CONFIG.discordUrl}
                target="_blank"
                rel="noopener noreferrer"
                aria-label="Discord"
                ctaId="community_discord"
                placement="footer"
              >
                <Ic name="discord-logo" />
              </TrackedLink>
              <TrackedLink
                href="/docs"
                aria-label="Documentation"
                ctaId="docs_root"
                placement="footer"
              >
                <Ic name="book-open" />
              </TrackedLink>
            </div>
          </div>
          <Column heading="Project" links={full ? PROJECT_FULL : PROJECT_SHORT} />
          {full ? (
            <>
              <Column heading="Compare" links={COMPARE} />
              <Column heading="Docs" links={DOCS} />
              <Column heading="Security" links={SECURITY} />
            </>
          ) : null}
        </div>
        <div className="fbot">
          <span>© {YEAR} CodesWhat. Released under the MIT License.</span>
          <TrackedLink
            className="pill"
            href={GITHUB_ORG_URL}
            target="_blank"
            rel="noopener noreferrer"
            ctaId="github_org"
            placement="footer"
          >
            <img
              src="/brand/codeswhat-52.webp"
              alt=""
              width={26}
              height={26}
              loading="lazy"
              decoding="async"
            />
            A CodesWhat project
          </TrackedLink>
        </div>
      </div>
    </footer>
  );
}
