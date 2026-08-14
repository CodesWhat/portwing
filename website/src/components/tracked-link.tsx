"use client";

import { type CtaId, type CtaPlacement, captureCta } from "@codeswhat/public-analytics";
import { usePathname } from "next/navigation";
import type { ComponentPropsWithoutRef } from "react";

type TrackedLinkProps = ComponentPropsWithoutRef<"a"> & {
  href: string;
  ctaId: CtaId;
  placement: CtaPlacement;
};

export function TrackedLink({ href, ctaId, placement, onClick, ...props }: TrackedLinkProps) {
  const pathname = usePathname();
  return (
    <a
      href={href}
      {...props}
      onClick={(event) => {
        captureCta(pathname, ctaId, placement);
        onClick?.(event);
      }}
    />
  );
}
