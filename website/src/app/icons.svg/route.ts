// SPDX-License-Identifier: MIT

import { ICON_SYMBOLS } from "@/components/icon-symbols";

// Exported as out/icons.svg at build time. Inlining the sprite put ~27 KB in the
// HTML and the same again in the RSC payload of every page.
export const dynamic = "force-static";

export function GET() {
  return new Response(
    `<svg xmlns="http://www.w3.org/2000/svg" aria-hidden="true">${ICON_SYMBOLS}</svg>`,
    { headers: { "Content-Type": "image/svg+xml; charset=utf-8" } },
  );
}
