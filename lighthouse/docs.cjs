// SPDX-License-Identifier: MIT

module.exports = {
  site: "docs",
  outputRoot: "docs/out",
  mountPath: "/docs",
  url: "http://127.0.0.1:{PORT}/docs/",
  numberOfRuns: 5,
  // Re-recorded 2026-11-18 after the site-fit pass: PostHog (~158 KB of
  // script) now loads after the load event. Ceilings keep the ~7% margin over
  // baseline. This build has no PostHog project key, so the numbers below do
  // not include it; in production it loads once the page is idle.
  //
  // Why 0.90 is not the bar for performance: docs ship the Next client runtime
  // plus the Fumadocs layout (about 730 KB of script on the wire) and a 97 KB
  // stylesheet, and the simulated slow-4G run downloads all of it before the
  // LCP. The markup itself is 112 KB. Getting to 0.90 means replacing the docs
  // framework, not trimming it, which is a different project.
  //
  // As with marketing, each field is the worst case across the two
  // environments this gate runs in. Byte weight here happens to be identical
  // locally and on the runner; the performance score is not (0.70 on the
  // runner vs 0.74 locally last recorded), so the recorded score is the
  // runner's, estimated from the 0.73 measured locally for this build and
  // that same 0.04 offset, with a point of rounding margin.
  baseline: {
    performance: 0.7,
    accessibility: 1,
    totalByteWeight: 1_013_960,
    scriptTransferBytes: 726_595,
  },
  // performanceMin tracks the baseline within 0.05, asserted by
  // scripts/lighthouse-config.test.mjs.
  performanceMin: 0.65,
  // The accessibility gate is 0.90 regardless of where the page measures. The
  // page scores 1.00.
  accessibilityMin: 0.9,
  totalByteWeightMax: 1_085_000,
  scriptTransferBytesMax: 777_000,
};
