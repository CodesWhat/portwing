import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";

const ROOT = path.resolve(import.meta.dirname, "..");
const workflow = fs.readFileSync(path.join(ROOT, ".github/workflows/ci-verify.yml"), "utf8");
const lefthook = fs.readFileSync(path.join(ROOT, "lefthook.yml"), "utf8");
const packageJson = JSON.parse(fs.readFileSync(path.join(ROOT, "package.json"), "utf8"));
const nodeAdapter = fs.readFileSync(path.join(ROOT, "scripts/ci/node-test.sh"), "utf8");

function jobSection(source, jobId) {
  const lines = source.split("\n");
  const start = lines.indexOf(`  ${jobId}:`);
  if (start === -1) return "";
  let end = lines.length;
  for (let index = start + 1; index < lines.length; index += 1) {
    if (/^ {2}[a-z][a-z0-9-]*:$/u.test(lines[index])) {
      end = index;
      break;
    }
  }
  return lines.slice(start, end).join("\n");
}

function assertRootWebLane(source) {
  assert.equal((source.match(/^ {2}node-ci:\s*$/gm) ?? []).length, 1);
  assert.equal((source.match(/^ {2}web:\s*$/gm) ?? []).length, 0);
  const nodeJob = jobSection(source, "node-ci");
  assert.ok(nodeJob, "missing node-ci reusable caller");
  assert.match(nodeJob, /test-check-name: Web Contract/);
  assert.match(nodeJob, /run-test: true/);
  assert.match(nodeJob, /node-version: 24/);
}

test("CI has one fail-closed root web lane", () => {
  assertRootWebLane(workflow);
  assert.match(nodeAdapter, /^#!\/usr\/bin\/env bash\nset -euo pipefail\n/u);
  assert.match(nodeAdapter, /^npm ci$/mu);
  assert.match(nodeAdapter, /^npm run check:web$/mu);
});

test("root web inputs cannot be satisfied by an unrelated job", () => {
  let mutated = workflow;
  for (const input of ["test-check-name: Web Contract", "run-test: true", "node-version: 24"]) {
    mutated = mutated.replace(`      ${input}\n`, "");
  }
  mutated +=
    "\n  decoy-node-inputs:\n    test-check-name: Web Contract\n    run-test: true\n    node-version: 24\n";
  assert.throws(() => assertRootWebLane(mutated));
});

test("pre-push mirrors the root web lane", () => {
  assert.equal((lefthook.match(/^ {4}web:\s*$/gm) ?? []).length, 1);
  assert.match(lefthook, /run: npm run check:web/);
  assert.equal(fs.readFileSync(path.join(ROOT, ".node-version"), "utf8").trim(), "24");
  assert.match(packageJson.scripts["check:web"], /^node scripts\/node-version-contract\.mjs && /);
  assert.equal(packageJson.engines.node, ">=24.0.0");
});
