"use client";

import Image from "next/image";
import { useEffect, useState } from "react";

// PortwingMascot replaces the ecosystem's stock `animate-wiggle` with a
// purpose-built "lean in and peer" loop: the pigeon cranes up and scales
// toward the viewer (transform-origin at its feet, so the top stretches up
// and the eyes get closer), then settles back down.
//
// The whole thing is gated behind prefers-reduced-motion: reduce → it just
// sits there, no transform.

type Props = {
  size?: number;
  className?: string;
};

export function PortwingMascot({ size = 168, className }: Props) {
  const [big, setBig] = useState(false);

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
        await wait(1100); // hold at full size before settling
        if (cancelled) break;
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
      className={`portwing-mascot ${big ? "is-big" : ""} ${className ?? ""}`}
      style={{ width: size, height: size }}
    >
      <Image
        src="/portwing.png"
        alt="Portwing"
        width={size}
        height={size}
        priority
        className="lk-frame dark:invert"
      />
    </div>
  );
}
