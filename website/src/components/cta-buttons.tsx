// SPDX-License-Identifier: MIT

import { Ic } from "@/components/icons";
import { TrackedLink } from "@/components/tracked-link";
import { GITHUB_URL } from "@/lib/site-config";

// Placement stays "hero": the analytics contract only allows these CTAs at header/hero/footer.
export function CtaButtons() {
  return (
    <div className="ctas">
      <TrackedLink
        className="btn primary"
        href={GITHUB_URL}
        target="_blank"
        rel="noopener noreferrer"
        ctaId="github_repository"
        placement="hero"
      >
        <Ic name="github-logo" />
        View on GitHub
      </TrackedLink>
      <TrackedLink className="btn" href="/docs" ctaId="docs_root" placement="hero">
        <Ic name="book-open" />
        Read the docs
      </TrackedLink>
    </div>
  );
}
