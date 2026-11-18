// SPDX-License-Identifier: MIT

import assert from "node:assert/strict";
import test from "node:test";

import { verifyLighthouseRuns } from "./lighthouse-budget.mjs";

for (const site of ["marketing", "docs"]) {
  test(`${site} Lighthouse budget uses a five-run median`, async () => {
    const config = (await import(`../lighthouse/${site}.cjs`)).default;
    assert.equal(config.numberOfRuns, 5);
    assert.match(config.url, /^http:\/\/127\.0\.0\.1:\{PORT\}\//);
    assert.ok(config.performanceMin >= config.baseline.performance - 0.05);
    assert.equal(config.accessibilityMin, 0.9);
    assert.ok(config.baseline.accessibility >= config.accessibilityMin);
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
// to measure until CI goes red. The numbers below are the worst case for a
// GitHub runner: the 2026-08-19 Node CI / Web Contract reading (run
// 32287185302) carried forward by the offsets it showed against a local run,
// applied to the 2026-11-18 local measurements. The runner and a local
// checkout do not measure the same page: byte weight is lower there and the
// performance scores are lower. Replace these with Web Contract's own numbers
// once it reports for this build. Spell each set out literally rather than
// spreading config.baseline, so that a change to the config cannot quietly
// move the goalposts these tests defend.
test("marketing Lighthouse budget admits the recorded GitHub runner baseline", async () => {
  const config = (await import("../lighthouse/marketing.cjs")).default;
  const githubRunnerBaseline = {
    performance: 0.74,
    accessibility: 1,
    totalByteWeight: 1_098_156,
    scriptTransferBytes: 518_672,
  };
  assert.doesNotThrow(() =>
    verifyLighthouseRuns(config, Array(config.numberOfRuns).fill(githubRunnerBaseline)),
  );
});

test("docs Lighthouse budget admits the recorded GitHub runner baseline", async () => {
  const config = (await import("../lighthouse/docs.cjs")).default;
  const githubRunnerBaseline = {
    performance: 0.7,
    accessibility: 1,
    totalByteWeight: 1_013_960,
    scriptTransferBytes: 726_595,
  };
  assert.doesNotThrow(() =>
    verifyLighthouseRuns(config, Array(config.numberOfRuns).fill(githubRunnerBaseline)),
  );
});

for (const site of ["marketing", "docs"]) {
  test(`${site} Lighthouse budget fails an accessibility regression`, async () => {
    const config = (await import(`../lighthouse/${site}.cjs`)).default;
    const regressed = Array(config.numberOfRuns).fill({
      performance: config.baseline.performance,
      accessibility: 0.89,
      totalByteWeight: config.baseline.totalByteWeight,
      scriptTransferBytes: config.baseline.scriptTransferBytes,
    });
    assert.throws(() => verifyLighthouseRuns(config, regressed), /accessibility budget exceeded/);
  });
}
