// SPDX-License-Identifier: MIT

/** One icon from the shared sprite, a static file so it stays out of every page's HTML and RSC payload. `name` is a Phosphor id ("book-open") or "xi-copy". */
export function Ic({ name, className }: { name: string; className?: string }) {
  const id = name.startsWith("xi-") ? name : `ph-${name}`;
  return (
    <svg className={className ? `ic ${className}` : "ic"} aria-hidden="true" focusable="false">
      <use href={`/icons.svg#${id}`} />
    </svg>
  );
}
