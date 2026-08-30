import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";

const ROOT = path.resolve(import.meta.dirname, "..");

function readConfig() {
  return JSON.parse(fs.readFileSync(path.join(ROOT, "renovate.json"), "utf8"));
}

function assertScheduledLockMaintenanceDisabled(config) {
  assert.deepEqual(config.lockFileMaintenance, { enabled: false });
}

test("Renovate disables scheduled lock maintenance while retaining dependency updates", () => {
  const config = readConfig();
  assertScheduledLockMaintenanceDisabled(config);

  const oldConfig = { ...config };
  delete oldConfig.lockFileMaintenance;
  assert.throws(() => assertScheduledLockMaintenanceDisabled(oldConfig));
  assert.ok(config.packageRules.length > 0);
});
