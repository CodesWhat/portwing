import assert from "node:assert/strict";
import test from "node:test";

import { assertSupportedNodeVersion } from "./node-version-contract.mjs";

test("Node version contract accepts the pinned major and newer runtimes", () => {
  assert.equal(assertSupportedNodeVersion("v24.15.0", "24"), 24);
  assert.equal(assertSupportedNodeVersion("v26.7.0", "24\n"), 26);
});

test("Node version contract rejects older and malformed runtimes and pins", () => {
  assert.throws(() => assertSupportedNodeVersion("v23.11.1", "24"), /require Node.js >=24/);
  assert.throws(() => assertSupportedNodeVersion("not-node", "24"), /invalid Node.js version/);
  assert.throws(() => assertSupportedNodeVersion("v24.15.0", "24.x"), /invalid Node.js pin/);
});
