// SPDX-License-Identifier: MIT

import { Ic } from "@/components/icons";
import { MenuDismiss } from "@/components/menu-dismiss";
import { ThemeToggle } from "@/components/theme-toggle";
import { TrackedLink } from "@/components/tracked-link";
import { GITHUB_URL, SITE_CONFIG } from "@/lib/site-config";

// The docs app runs under basePath="/docs". Next <Link> prefixes every href with /docs, which
// would send the logo to the wrong place. All cross-app navigation uses plain <a>
// anchors that ignore basePath and resolve at the origin root.
export function SiteHeader() {
  return (
    <header className="top" id="top">
      <div className="wrap">
        <a className="brand" href="/" aria-label={`${SITE_CONFIG.name} home`}>
          {/* biome-ignore lint/performance/noImgElement: basePath="/docs" makes next/image prepend /docs to root-relative src; this asset lives at the marketing-site origin root, so a plain <img> is required. */}
          <img src="/brand/pigeon-64.webp" alt="" width={28} height={28} />
          <span>{SITE_CONFIG.name}</span>
        </a>
        <nav className="nav" aria-label="Main">
          <TrackedLink href="/docs" ctaId="docs_root" placement="header" aria-current="page">
            <Ic name="book-open" />
            Docs
          </TrackedLink>
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
        <ThemeToggle />
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
            <a href={GITHUB_URL} target="_blank" rel="noopener noreferrer">
              <Ic name="github-logo" />
              GitHub
            </a>
          </div>
        </details>
        <MenuDismiss />
      </div>
    </header>
  );
}
