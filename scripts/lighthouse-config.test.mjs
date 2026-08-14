import assert from "node:assert/strict";
import test from "node:test";

import { verifyLighthouseRuns } from "./lighthouse-budget.mjs";

for (const site of ["marketing", "docs"]) {
  test(`${site} Lighthouse budget uses a five-run median`, async () => {
    const config = (await import(`../lighthouse/${site}.cjs`)).default;
    assert.equal(config.numberOfRuns, 5);
    assert.match(config.url, /^http:\/\/127\.0\.0\.1:\{PORT\}\//);
    assert.ok(config.performanceMin >= config.baseline.performance - 0.05);
    assert.ok(config.scriptTransferBytesMax <= config.baseline.scriptTransferBytes * 1.1);
    assert.ok(config.totalByteWeightMax <= config.baseline.totalByteWeight * 1.1);
  });
}

test("marketing Lighthouse budget admits the recorded GitHub runner baseline", async () => {
  const config = (await import("../lighthouse/marketing.cjs")).default;
  const githubRunnerBaseline = {
    performance: 0.67,
    totalByteWeight: 2_039_127,
    scriptTransferBytes: 690_429,
  };
  assert.doesNotThrow(() =>
    verifyLighthouseRuns(config, Array(config.numberOfRuns).fill(githubRunnerBaseline)),
  );
});
