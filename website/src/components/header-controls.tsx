// SPDX-License-Identifier: MIT

"use client";

import { useEffect, useSyncExternalStore } from "react";
import { Ic } from "@/components/icons";

type Mode = "dark" | "light";

// The colour mode lives on <html data-mode>. The inline script in layout.tsx sets it before
// paint; this store keeps React in step with it and follows the system scheme until the
// visitor picks a mode themselves.
const listeners = new Set<() => void>();
let chosenThisSession = false;

const readMode = (): Mode =>
  document.documentElement.getAttribute("data-mode") === "light" ? "light" : "dark";

function hasSavedChoice(): boolean {
  if (chosenThisSession) return true;
  try {
    const saved = localStorage.getItem("theme");
    return saved === "light" || saved === "dark";
  } catch {
    return false;
  }
}

function emit() {
  for (const listener of listeners) listener();
}

function setMode(next: Mode) {
  chosenThisSession = true;
  document.documentElement.setAttribute("data-mode", next);
  try {
    localStorage.setItem("theme", next);
  } catch {
    // Storage can be blocked; the choice still applies for this page view.
  }
  emit();
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  const query = window.matchMedia("(prefers-color-scheme: light)");
  const onSystemChange = (event: MediaQueryListEvent) => {
    if (hasSavedChoice()) return;
    document.documentElement.setAttribute("data-mode", event.matches ? "light" : "dark");
    emit();
  };
  query.addEventListener("change", onSystemChange);
  return () => {
    listeners.delete(listener);
    query.removeEventListener("change", onSystemChange);
  };
}

/** Light/dark switch. */
export function ModeToggle() {
  const mode = useSyncExternalStore(subscribe, readMode, () => "dark" as Mode);

  return (
    <button
      type="button"
      className="modetog"
      aria-pressed={mode === "dark"}
      aria-label="Dark mode"
      onClick={() => setMode(mode === "dark" ? "light" : "dark")}
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
