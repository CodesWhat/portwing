// SPDX-License-Identifier: MIT

"use client";

import { useEffect, useRef, useState } from "react";
import { Ic } from "@/components/icons";

export function CopyButton({ text, label }: { text: string; label: string }) {
  const [state, setState] = useState<"idle" | "copied" | "failed">("idle");
  const reset = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  useEffect(() => () => clearTimeout(reset.current), []);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setState("copied");
    } catch {
      setState("failed");
    }
    clearTimeout(reset.current);
    reset.current = setTimeout(() => setState("idle"), 1800);
  };

  return (
    <button type="button" className="copy" aria-label={label} onClick={copy}>
      <Ic name="xi-copy" />
      <span className="cl" aria-live="polite">
        {state === "copied" ? "Copied" : state === "failed" ? "Copy failed" : "Copy"}
      </span>
    </button>
  );
}
