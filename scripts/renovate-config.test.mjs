import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";

const ROOT = path.resolve(import.meta.dirname, "..");

function readConfig() {
  return JSON.parse(fs.readFileSync(path.join(ROOT, "renovate.json"), "utf8"));
}

function readPackage() {
  return JSON.parse(fs.readFileSync(path.join(ROOT, "package.json"), "utf8"));
}

function readLock() {
  return JSON.parse(fs.readFileSync(path.join(ROOT, "package-lock.json"), "utf8"));
}

function assertScheduledLockMaintenanceDisabled(config) {
  assert.deepEqual(config.lockFileMaintenance, { enabled: false });
}

function assertPackageVersion(lock, name, version) {
  assert.ok(lock.packages[name], `${name} is missing`);
  assert.equal(lock.packages[name].version, version, name);
}

test("Renovate disables scheduled lock maintenance while retaining dependency updates", () => {
  const config = readConfig();
  assertScheduledLockMaintenanceDisabled(config);

  const oldConfig = { ...config };
  delete oldConfig.lockFileMaintenance;
  assert.throws(() => assertScheduledLockMaintenanceDisabled(oldConfig));
  assert.ok(config.packageRules.length > 0);
});

test("the refreshed Next dependency keeps Sharp and every optional platform package current", () => {
  const packageJson = readPackage();
  const lock = readLock();
  assert.equal(packageJson.overrides.next.sharp, "^0.35.4");

  const sharpPackages = Object.entries(lock.packages).filter(
    ([name]) => name === "node_modules/sharp" || name.includes("/@img/sharp-"),
  );
  assert.ok(sharpPackages.length > 0);
  for (const [name, packageData] of sharpPackages) {
    assert.equal(packageData.version, name.includes("sharp-libvips") ? "1.3.3" : "0.35.4", name);
  }

  for (const name of [
    "node_modules/@img/sharp-darwin-arm64",
    "node_modules/@img/sharp-linux-x64",
  ]) {
    assertPackageVersion(lock, name, "0.35.4");
  }
  for (const name of [
    "node_modules/@img/sharp-libvips-darwin-arm64",
    "node_modules/@img/sharp-libvips-linux-x64",
  ]) {
    assertPackageVersion(lock, name, "1.3.3");
  }
  for (const name of [
    "node_modules/lightningcss-darwin-arm64",
    "node_modules/lightningcss-linux-x64-gnu",
  ]) {
    assertPackageVersion(lock, name, "1.32.0");
  }

  const lockWithoutDarwin = { packages: { ...lock.packages } };
  delete lockWithoutDarwin.packages["node_modules/@img/sharp-darwin-arm64"];
  assert.throws(() =>
    assertPackageVersion(lockWithoutDarwin, "node_modules/@img/sharp-darwin-arm64", "0.35.4"),
  );

  const oldConfig = {
    ...packageJson,
    overrides: {
      ...packageJson.overrides,
      next: { ...packageJson.overrides.next, sharp: "^0.35.3" },
    },
  };
  assert.notEqual(oldConfig.overrides.next.sharp, packageJson.overrides.next.sharp);
});
