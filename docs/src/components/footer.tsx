// SPDX-License-Identifier: MIT

import type { CtaId } from "@codeswhat/public-analytics";
import { Ic } from "@/components/icons";
import { TrackedLink } from "@/components/tracked-link";
import { GITHUB_ORG_URL, GITHUB_RELEASES_URL, GITHUB_URL, SITE_CONFIG } from "@/lib/site-config";

const YEAR = process.env.NEXT_PUBLIC_COPYRIGHT_YEAR;
const BLURB =
  "Security-first remote Docker agent. Lightweight Go binary, Ed25519 auth, sockguard-ready socket filter, structured audit log.";

type FooterLink = { label: string; href: string; ctaId?: CtaId };

// Same Project column as the marketing site footer. Plain anchors: the docs app runs under
// basePath="/docs", so these must resolve at the origin root.
const PROJECT: FooterLink[] = [
  { label: "Documentation", href: "/docs", ctaId: "docs_root" },
  { label: "Compare", href: "/compare" },
  { label: "GitHub", href: GITHUB_URL, ctaId: "github_repository" },
  { label: "Releases", href: GITHUB_RELEASES_URL },
  { label: "License", href: SITE_CONFIG.licenseUrl },
];

function FooterAnchor({ link }: { link: FooterLink }) {
  const external = link.href.startsWith("https://");
  const target = external ? "_blank" : undefined;
  const rel = external ? "noopener noreferrer" : undefined;
  if (link.ctaId) {
    return (
      <TrackedLink href={link.href} target={target} rel={rel} ctaId={link.ctaId} placement="footer">
        {link.label}
      </TrackedLink>
    );
  }
  return (
    <a href={link.href} target={target} rel={rel}>
      {link.label}
    </a>
  );
}

export function Footer() {
  return (
    <footer id="footer">
      <div className="wrap">
        <div className="fgrid">
          <div>
            <a className="brand" href="/">
              {/* biome-ignore lint/performance/noImgElement: basePath="/docs" makes next/image prepend /docs to root-relative src; this asset lives at the marketing-site origin root, so a plain <img> is required. */}
              <img
                src="/brand/pigeon-64.webp"
                alt=""
                width={28}
                height={28}
                loading="lazy"
                decoding="async"
              />
              <span>{SITE_CONFIG.name}</span>
            </a>
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
          <div>
            <p className="fh micro">Project</p>
            <ul>
              {PROJECT.map((link) => (
                <li key={link.label}>
                  <FooterAnchor link={link} />
                </li>
              ))}
            </ul>
          </div>
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
            {/* biome-ignore lint/performance/noImgElement: basePath="/docs" makes next/image prepend /docs to root-relative src; this asset lives at the marketing-site origin root, so a plain <img> is required. */}
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
