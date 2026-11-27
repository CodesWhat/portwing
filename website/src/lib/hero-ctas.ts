// SPDX-License-Identifier: MIT

import type { CtaId, CtaPlacement } from "@codeswhat/public-analytics";

type HeroCta = { ctaId: CtaId; placement: CtaPlacement; href: string; label: string };

// One primary action leads the hero. The rest are secondary, and every one
// reports through the same "cta activated" event (cta_id + placement).
export const HERO_PRIMARY_CTA: HeroCta = {
  ctaId: "docs_getting_started",
  placement: "hero",
  href: "/docs/getting-started",
  label: "Get started",
};

export const HERO_SECONDARY_CTA: HeroCta = {
  ctaId: "docs_root",
  placement: "hero",
  href: "/docs",
  label: "Read the docs",
};
