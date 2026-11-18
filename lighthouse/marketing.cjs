// SPDX-License-Identifier: MIT

module.exports = {
  site: "marketing",
  outputRoot: "website/out",
  mountPath: "/",
  url: "http://127.0.0.1:{PORT}/",
  numberOfRuns: 5,
  // Re-recorded 2026-11-18 after the site-fit pass: PostHog (~158 KB of
  // script) now loads after the load event instead of racing the first paint,
  // and the 25 KB icon sprite is a static /icons.svg instead of sitting in the
  // HTML and again in the RSC payload (-53 KB of document). Ceilings keep the
  // ~7% margin over baseline they always had.
  //
  // This build has no PostHog project key, so PostHog never initializes here
  // and its chunk is never requested: the numbers below do not include it.
  // In production it loads once the page is idle, after the paint these
  // metrics are about.
  //
  // Why 0.90 is not the bar for performance: the score is dominated by the
  // simulated 4x CPU / slow 4G run downloading React and the Next client
  // runtime (about 400 KB of script) before the LCP. The same page with every
  // script stripped scores 0.88-0.93, so there is no 0.90 left to find without
  // dropping the framework; the content, fonts and CSS are already near that
  // floor. The remaining spend is framework weight, not product content.
  //
  // Each field records the WORST CASE across the two environments this gate
  // runs in, because it has to stay green in both: the lefthook pre-push hook
  // locally and Node CI / Web Contract on a GitHub runner. They do not agree:
  // byte weight is higher locally (script bytes are identical, the delta is
  // non-script assets), while the performance score is lower on the runner.
  // Total byte weight moved between 1,027,188 and 1,098,156 across runs of the
  // same build (the badge and star-chart images are third-party), so the
  // higher one is recorded.
  // The last runner reading was 0.71 against 0.72 locally; this pass measured
  // 0.77 locally, so the recorded 0.74 keeps that offset and then some. It is
  // an estimate until Web Contract reports its own number for this build.
  // Recording only one environment is how the old budget ended up passing CI
  // while failing the local hook.
  baseline: {
    performance: 0.74,
    accessibility: 1,
    totalByteWeight: 1_098_156,
    scriptTransferBytes: 518_672,
  },
  // performanceMin tracks the baseline within 0.05, asserted by
  // scripts/lighthouse-config.test.mjs. That is more headroom than the old
  // 0.67/0.64 pair carried, and the score is a five-run median, so the noise
  // in it is already damped.
  performanceMin: 0.69,
  // The accessibility gate is 0.90 regardless of where the page measures. The
  // page scores 1.00; the gate stays at the standard bar so a regression has
  // room to show up before it fails.
  accessibilityMin: 0.9,
  totalByteWeightMax: 1_175_000,
  scriptTransferBytesMax: 555_000,
};
