// SPDX-License-Identifier: MIT

"use client";

import { useTheme } from "next-themes";
import { useEffect, useState } from "react";
import { Ic } from "@/components/icons";

/**
 * Light/dark switch. next-themes owns the mode: it follows the system scheme until the visitor
 * picks one, then keeps that in localStorage ("theme"), the same key and values website/ reads.
 * The icon and label come from <html data-mode> in CSS, so a saved light choice never flashes
 * dark. aria-pressed waits for mount, because the server cannot know the saved choice and
 * rendering it would not match the first client render.
 */
export function ThemeToggle() {
  const { resolvedTheme, setTheme } = useTheme();
  const [mounted, setMounted] = useState(false);

  useEffect(() => setMounted(true), []);

  const isDark = resolvedTheme === "dark";

  return (
    <button
      type="button"
      className="modetog"
      aria-pressed={mounted ? isDark : undefined}
      aria-label="Dark mode"
      onClick={() => setTheme(isDark ? "light" : "dark")}
    >
      <Ic name="moon" className="i-moon" />
      <Ic name="sun" className="i-sun" />
      <span className="ml l-dark">Dark</span>
      <span className="ml l-light">Light</span>
    </button>
  );
}
