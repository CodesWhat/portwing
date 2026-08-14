import assert from "node:assert/strict";
import test from "node:test";

import { median, verifyLighthouseRuns, withLighthouseResources } from "./lighthouse-budget.mjs";

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

test("Lighthouse resource cleanup closes the server across setup failures", async () => {
  let serverCloseCount = 0;
  const server = {
    close(callback) {
      serverCloseCount += 1;
      callback();
    },
  };

  await assert.rejects(
    withLighthouseResources({
      startServer: async () => server,
      startChrome: async () => {
        throw new Error("fixture Chrome launch failed");
      },
      run: async () => assert.fail("runs must not start after Chrome launch fails"),
    }),
    /fixture Chrome launch failed/,
  );
  assert.equal(serverCloseCount, 1);
});

test("Lighthouse resource cleanup closes the server when Chrome cleanup fails", async () => {
  let serverCloseCount = 0;
  const server = {
    close(callback) {
      serverCloseCount += 1;
      callback();
    },
  };

  await assert.rejects(
    withLighthouseResources({
      startServer: async () => server,
      startChrome: async () => ({
        kill: async () => {
          throw new Error("fixture Chrome cleanup failed");
        },
      }),
      run: async () => {
        throw new Error("fixture report setup failed");
      },
    }),
    /fixture Chrome cleanup failed/,
  );
  assert.equal(serverCloseCount, 1);
});
