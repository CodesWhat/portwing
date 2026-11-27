// SPDX-License-Identifier: MIT

import { Ic } from "@/components/icons";
import { TrackedLink } from "@/components/tracked-link";
import { HERO_PRIMARY_CTA, HERO_SECONDARY_CTA } from "@/lib/hero-ctas";
import { GITHUB_URL } from "@/lib/site-config";

// Placement stays "hero": the analytics contract only allows these CTAs at header/hero/footer.
export function CtaButtons() {
  return (
    <div className="ctas">
      <TrackedLink
        className="btn primary"
        href={HERO_PRIMARY_CTA.href}
        ctaId={HERO_PRIMARY_CTA.ctaId}
        placement={HERO_PRIMARY_CTA.placement}
      >
        <Ic name="lightning" />
        {HERO_PRIMARY_CTA.label}
      </TrackedLink>
      <TrackedLink
        className="btn"
        href={HERO_SECONDARY_CTA.href}
        ctaId={HERO_SECONDARY_CTA.ctaId}
        placement={HERO_SECONDARY_CTA.placement}
      >
        <Ic name="book-open" />
        {HERO_SECONDARY_CTA.label}
      </TrackedLink>
      <TrackedLink
        href={GITHUB_URL}
        target="_blank"
        rel="noopener noreferrer"
        ctaId="github_repository"
        placement="hero"
      >
        View on GitHub
      </TrackedLink>
    </div>
  );
}
