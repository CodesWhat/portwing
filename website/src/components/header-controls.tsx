// SPDX-License-Identifier: MIT

"use client";

import { useEffect, useState } from "react";
import { Ic } from "@/components/icons";

type Mode = "dark" | "light";

/** Light/dark switch. The initial mode is set before paint by the inline script in layout.tsx. */
export function ModeToggle() {
  const [mode, setMode] = useState<Mode>("dark");

  useEffect(() => {
    setMode(document.documentElement.getAttribute("data-mode") === "light" ? "light" : "dark");
  }, []);

  const toggle = () => {
    const next: Mode = mode === "dark" ? "light" : "dark";
    document.documentElement.setAttribute("data-mode", next);
    try {
      localStorage.setItem("theme", next);
    } catch {
      // Storage can be blocked; the choice still applies for this page view.
    }
    setMode(next);
  };

  return (
    <button
      type="button"
      className="modetog"
      aria-pressed={mode === "dark"}
      aria-label="Dark mode"
      onClick={toggle}
    >
      <Ic name="moon" className="i-moon" />
      <Ic name="sun" className="i-sun" />
      <span className="ml">{mode === "dark" ? "Dark" : "Light"}</span>
    </button>
  );
}

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
