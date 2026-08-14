module.exports = {
  site: "marketing",
  outputRoot: "website/out",
  mountPath: "/",
  url: "http://127.0.0.1:{PORT}/",
  numberOfRuns: 5,
  baseline: {
    performance: 0.67,
    totalByteWeight: 2_039_127,
    scriptTransferBytes: 690_429,
  },
  performanceMin: 0.64,
  totalByteWeightMax: 2_180_000,
  scriptTransferBytesMax: 730_000,
};
