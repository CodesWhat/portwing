module.exports = {
  site: "docs",
  outputRoot: "docs/out",
  mountPath: "/docs",
  url: "http://127.0.0.1:{PORT}/docs/",
  consoleRoute: "/docs/installation.html",
  numberOfRuns: 5,
  // Re-recorded 2026-08-19 after the oversized logos came out (#168). The docs
  // site was carrying the same ~980 KB as marketing while sitting just under
  // this ceiling, so nothing here ever went red — the budget was loose enough
  // to hide a megabyte. Ceiling keeps the ~7% margin over baseline.
  //
  // As with marketing, each field is the worst case across the two
  // environments this gate runs in. Byte weight here happens to be identical
  // locally and on the runner; the performance score is not (0.70 on the
  // runner vs 0.74 locally), so the recorded score is the runner's.
  baseline: {
    performance: 0.70,
    totalByteWeight: 1_444_340,
    scriptTransferBytes: 1_157_003,
  },
  // performanceMin tracks the baseline within 0.05, asserted by
  // scripts/lighthouse-config.test.mjs.
  performanceMin: 0.65,
  totalByteWeightMax: 1_545_000,
  scriptTransferBytesMax: 1_240_000,
};
