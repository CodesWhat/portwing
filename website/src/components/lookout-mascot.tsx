"use client";

import Image from "next/image";
import { useEffect, useState } from "react";

// LookoutMascot replaces the ecosystem's stock `animate-wiggle` with a
// purpose-built "lean in and peer" loop: the pigeon cranes up and scales
// toward the viewer (transform-origin at its feet, so the top stretches up
// and the eyes get closer), then — at full size — swaps to the eyes-closed
// blink frame a few times before settling back down.
//
// Both frames are rendered stacked and cross-faded via opacity so neither
// has to lazy-load mid-blink (no flash on the first blink). The whole thing
// is gated behind prefers-reduced-motion: reduce → it just sits there, eyes
// open, no transform.

type Props = {
  size?: number;
  className?: string;
};

export function LookoutMascot({ size = 168, className }: Props) {
  const [big, setBig] = useState(false);
  const [eyes, setEyes] = useState<"open" | "blink">("open");

  useEffect(() => {
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;

    let cancelled = false;
    const timers = new Set<ReturnType<typeof setTimeout>>();
    const wait = (ms: number) =>
      new Promise<void>((resolve) => {
        const t = setTimeout(() => {
          timers.delete(t);
          resolve();
        }, ms);
        timers.add(t);
      });

    (async () => {
      // small initial beat so it doesn't animate the instant it mounts
      await wait(1400);
      while (!cancelled) {
        setBig(true); // lean in / scale up
        await wait(720); // let the crane-up settle before blinking
        if (cancelled) break;

        for (let i = 0; i < 3 && !cancelled; i++) {
          setEyes("blink");
          await wait(150);
          setEyes("open");
          await wait(170);
        }

        await wait(140);
        setBig(false); // settle back down
        await wait(620 + 2600); // return + rest before the next peer
      }
    })();

    return () => {
      cancelled = true;
      for (const t of timers) clearTimeout(t);
    };
  }, []);

  return (
    <div
      className={`lookout-mascot ${big ? "is-big" : ""} ${className ?? ""}`}
      data-eyes={eyes}
      style={{ width: size, height: size }}
    >
      <Image
        src="/lookout.png"
        alt="Lookout"
        width={size}
        height={size}
        priority
        className="lk-frame lk-open"
      />
      <Image
        src="/lookout-blink.png"
        alt=""
        aria-hidden="true"
        width={size}
        height={size}
        priority
        className="lk-frame lk-blink"
      />
    </div>
  );
}
