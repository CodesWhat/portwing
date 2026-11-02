// SPDX-License-Identifier: MIT

"use client";

import { useEffect } from "react";

/** Closes the open mobile menu on Escape, an outside click, or a link choice. */
export function MenuDismiss() {
  useEffect(() => {
    const open = () => [...document.querySelectorAll<HTMLDetailsElement>("details.menu[open]")];
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      for (const details of open()) {
        details.open = false;
        details.querySelector("summary")?.focus();
      }
    };
    const onClick = (event: MouseEvent) => {
      const target = event.target as Element;
      for (const details of open()) {
        if (!details.contains(target) || target.closest("a")) details.open = false;
      }
    };
    document.addEventListener("keydown", onKey);
    document.addEventListener("click", onClick);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("click", onClick);
    };
  }, []);
  return null;
}
