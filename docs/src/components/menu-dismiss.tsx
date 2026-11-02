// SPDX-License-Identifier: MIT

"use client";

import { useEffect } from "react";

const DRAWER = "#nd-sidebar-mobile";

/**
 * Closes the open mobile menu on Escape, an outside click, or a link choice. It also covers the
 * Fumadocs mobile docs drawer: Escape closes it and returns focus to "Open Sidebar", and opening
 * it moves focus to the drawer's own close button, because the button that opened it sits in
 * #nd-subnav and is covered by the drawer.
 */
export function MenuDismiss() {
  useEffect(() => {
    const open = () => [...document.querySelectorAll<HTMLDetailsElement>("details.menu[open]")];
    const drawerClose = () =>
      document.querySelector<HTMLButtonElement>(`${DRAWER} button[aria-label="Close Sidebar"]`);
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      const close = drawerClose();
      if (close) {
        close.click();
        // The trigger flips back to "Open Sidebar" once the drawer's state updates.
        requestAnimationFrame(() =>
          document.querySelector<HTMLElement>("#nd-subnav button")?.focus(),
        );
        return;
      }
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
    let drawerWasOpen = false;
    const observer = new MutationObserver(() => {
      const close = drawerClose();
      if (close && !drawerWasOpen) close.focus();
      drawerWasOpen = close !== null;
    });
    observer.observe(document.body, { childList: true, subtree: true });
    document.addEventListener("keydown", onKey);
    document.addEventListener("click", onClick);
    return () => {
      observer.disconnect();
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("click", onClick);
    };
  }, []);
  return null;
}
