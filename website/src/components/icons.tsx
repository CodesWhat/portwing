// SPDX-License-Identifier: MIT

import { ICON_SYMBOLS } from "@/components/icon-symbols";

/** One icon from the shared sprite. `name` is a Phosphor id ("book-open") or "xi-copy". */
export function Ic({ name, className }: { name: string; className?: string }) {
  const id = name.startsWith("xi-") ? name : `ph-${name}`;
  return (
    <svg className={className ? `ic ${className}` : "ic"} aria-hidden="true" focusable="false">
      <use href={`#${id}`} />
    </svg>
  );
}

/** The sprite itself. Rendered once in the root layout; every <Ic> references it. */
export function IconSprite() {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      style={{ display: "none" }}
      aria-hidden="true"
      // biome-ignore lint/security/noDangerouslySetInnerHtml: static sprite generated from vetted Phosphor SVG paths
      dangerouslySetInnerHTML={{ __html: ICON_SYMBOLS }}
    />
  );
}
