import assert from "node:assert/strict";
import test from "node:test";

import { median, verifyLighthouseRuns } from "./lighthouse-budget.mjs";

test("median is stable for unsorted odd and even inputs", () => {
  assert.equal(median([5, 1, 3]), 3);
  assert.equal(median([4, 1, 3, 2]), 2.5);
  assert.throws(() => median([]), /at least one value/);
});

test("Lighthouse budgets use the median and fail closed", () => {
  const config = {
    site: "fixture",
    performanceMin: 0.9,
    totalByteWeightMax: 1_000,
    scriptTransferBytesMax: 500,
  };
  const good = [
    { performance: 0.91, totalByteWeight: 900, scriptTransferBytes: 400 },
    { performance: 0.93, totalByteWeight: 800, scriptTransferBytes: 300 },
    { performance: 0.89, totalByteWeight: 1_200, scriptTransferBytes: 600 },
  ];
  assert.deepEqual(verifyLighthouseRuns(config, good), {
    performance: 0.91,
    totalByteWeight: 900,
    scriptTransferBytes: 400,
  });
  assert.throws(
    () => verifyLighthouseRuns(config, good.slice(0, 1).concat(good.slice(2))),
    /total byte weight budget exceeded/,
  );
  assert.throws(() => verifyLighthouseRuns(config, []), /expected Lighthouse runs/);
});
