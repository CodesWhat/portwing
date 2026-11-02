// SPDX-License-Identifier: MIT

import Link from "next/link";
import { MenuDismiss, ModeToggle } from "@/components/header-controls";
import { Ic } from "@/components/icons";
import { TrackedLink } from "@/components/tracked-link";
import { GITHUB_URL, SITE_CONFIG } from "@/lib/site-config";

type Props = { current?: "compare"; home?: boolean };

export function SiteHeader({ current, home = false }: Props) {
  return (
    <header className="top" id="top">
      <div className="wrap">
        <Link className="brand" href="/" aria-label={`${SITE_CONFIG.name} home`}>
          <img src="/brand/pigeon-64.webp" alt="" width={28} height={28} />
          <span>{SITE_CONFIG.name}</span>
        </Link>
        <nav className="nav" aria-label="Main">
          <TrackedLink href="/docs" ctaId="docs_root" placement="header">
            <Ic name="book-open" />
            Docs
          </TrackedLink>
          <Link href="/compare" aria-current={current === "compare" ? "page" : undefined}>
            <Ic name="arrows-clockwise" />
            Compare
          </Link>
          <TrackedLink
            href={GITHUB_URL}
            target="_blank"
            rel="noopener noreferrer"
            ctaId="github_repository"
            placement="header"
          >
            <Ic name="github-logo" />
            GitHub
          </TrackedLink>
        </nav>
        <span className="spacer" />
        <ModeToggle />
        <details className="menu" id="mobile-menu">
          <summary aria-label="Open menu">
            <Ic name="list" />
            Menu
          </summary>
          <div className="panel">
            <a href="/docs">
              <Ic name="book-open" />
              Docs
            </a>
            <Link href="/compare">
              <Ic name="arrows-clockwise" />
              Compare
            </Link>
            <a href={GITHUB_URL} target="_blank" rel="noopener noreferrer">
              <Ic name="github-logo" />
              GitHub
            </a>
            {home ? (
              <>
                <a href="#demo">
                  <Ic name="terminal-window" />
                  See it in action
                </a>
                <a href="#roadmap">
                  <Ic name="git-branch" />
                  Roadmap
                </a>
                <a href="#faq">
                  <Ic name="lifebuoy" />
                  FAQ
                </a>
                <a href="/docs/security-model">
                  <Ic name="shield-check" />
                  Security
                </a>
                <a href={SITE_CONFIG.discordUrl} target="_blank" rel="noopener noreferrer">
                  <Ic name="discord-logo" />
                  Discord
                </a>
              </>
            ) : null}
          </div>
        </details>
        <MenuDismiss />
      </div>
    </header>
  );
}
