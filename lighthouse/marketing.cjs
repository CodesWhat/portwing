module.exports = {
  site: "marketing",
  outputRoot: "website/out",
  mountPath: "/",
  url: "http://127.0.0.1:{PORT}/",
  numberOfRuns: 5,
  // Re-recorded 2026-08-19 after the oversized logos came out (#166, #168).
  // The old baseline described a page carrying ~1 MB of images rendered at a
  // fraction of their source size, so it could not detect a regression back to
  // that state. Ceilings keep the ~7% margin over baseline they always had.
  //
  // Each field records the WORST CASE across the two environments this gate
  // runs in, because it has to stay green in both: the lefthook pre-push hook
  // locally and Node CI / Web Contract on a GitHub runner. They do not agree.
  // Byte weight is higher locally (1548017 vs 1403680 on the runner; script
  // bytes are identical, so the 144 KB delta is non-script assets), while the
  // performance score is lower on the runner (0.71 vs 0.72). Recording only
  // one environment is how the old budget ended up passing CI while failing
  // the local hook.
  baseline: {
    performance: 0.71,
    totalByteWeight: 1_548_017,
    scriptTransferBytes: 682_605,
  },
  // performanceMin tracks the baseline within 0.05, asserted by
  // scripts/lighthouse-config.test.mjs. That is more headroom than the old
  // 0.67/0.64 pair carried, and the score is a five-run median, so the noise
  // in it is already damped.
  performanceMin: 0.66,
  totalByteWeightMax: 1_656_000,
  scriptTransferBytesMax: 730_000,
};
