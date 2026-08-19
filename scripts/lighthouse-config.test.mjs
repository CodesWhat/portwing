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

test("each Lighthouse budget serves its own build output", async () => {
  const marketing = (await import("../lighthouse/marketing.cjs")).default;
  const docs = (await import("../lighthouse/docs.cjs")).default;
  assert.equal(marketing.outputRoot, "website/out");
  assert.equal(marketing.mountPath, "/");
  assert.equal(docs.outputRoot, "docs/out");
  assert.equal(docs.mountPath, "/docs");
});

// These two guard against tuning the budgets to whatever this machine happens
// to measure until CI goes red. The numbers below are what Node CI / Web
// Contract actually reported on a GitHub runner, re-recorded 2026-08-19 from
// run 32287185302 after the oversized logos came out (#166, #168). The runner
// and a local checkout do not measure the same page: marketing total byte
// weight is ~144 KB lower there, and both performance scores are lower. Spell
// each set out literally rather than spreading config.baseline, so that a
// change to the config cannot quietly move the goalposts these tests defend.
test("marketing Lighthouse budget admits the recorded GitHub runner baseline", async () => {
  const config = (await import("../lighthouse/marketing.cjs")).default;
  const githubRunnerBaseline = {
    performance: 0.71,
    totalByteWeight: 1_403_680,
    scriptTransferBytes: 682_605,
  };
  assert.doesNotThrow(() =>
    verifyLighthouseRuns(config, Array(config.numberOfRuns).fill(githubRunnerBaseline)),
  );
});

test("docs Lighthouse budget admits the recorded GitHub runner baseline", async () => {
  const config = (await import("../lighthouse/docs.cjs")).default;
  const githubRunnerBaseline = {
    performance: 0.7,
    totalByteWeight: 1_444_340,
    scriptTransferBytes: 1_157_003,
  };
  assert.doesNotThrow(() =>
    verifyLighthouseRuns(config, Array(config.numberOfRuns).fill(githubRunnerBaseline)),
  );
});
