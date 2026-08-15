import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

const SCRIPT_FILE = fileURLToPath(import.meta.url);
const ROOT = path.resolve(path.dirname(SCRIPT_FILE), "..");

export function assertSupportedNodeVersion(currentVersion, pin) {
  const normalizedPin = pin.trim();
  if (!/^\d+$/u.test(normalizedPin)) throw new Error(`invalid Node.js pin: ${normalizedPin}`);
  const match = currentVersion.match(/^v?(\d+)(?:\.|$)/u);
  if (!match) throw new Error(`invalid Node.js version: ${currentVersion}`);
  const currentMajor = Number(match[1]);
  const minimumMajor = Number(normalizedPin);
  if (currentMajor < minimumMajor) {
    throw new Error(
      `Portwing web checks require Node.js >=${minimumMajor}. Current: ${currentVersion}`,
    );
  }
  return currentMajor;
}

function main() {
  const pin = fs.readFileSync(path.join(ROOT, ".node-version"), "utf8");
  assertSupportedNodeVersion(process.version, pin);
}

if (process.argv[1] && path.resolve(process.argv[1]) === SCRIPT_FILE) main();
