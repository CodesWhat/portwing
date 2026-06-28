"use client";

import Image from "next/image";

// PortwingMascot — the Portwing bird hops left, beats, hops back right,
// beats, then repeats. Implemented as a pure CSS keyframe animation
// (bird-hop in globals.css) applied to the wrapper div.
//
// transform-origin is set to 50% 100% (feet) so the body lifts upward
// on each hop rather than rotating around its own centre.
//
// Reduced motion: the @media (prefers-reduced-motion: reduce) block in
// globals.css zeroes the animation; this component needs no JS guard.
//
// The image is purely decorative — aria-hidden, empty alt.

type Props = {
  size?: number;
  className?: string;
};

export function PortwingMascot({ size = 168, className }: Props) {
  return (
    <div
      className={`portwing-mascot animate-bird-hop ${className ?? ""}`}
      style={{ width: size, height: size }}
      aria-hidden="true"
    >
      <Image
        src="/portwing.png"
        alt=""
        width={size}
        height={size}
        priority
        className="lk-frame dark:invert"
      />
    </div>
  );
}
