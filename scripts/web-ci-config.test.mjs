import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";

const ROOT = path.resolve(import.meta.dirname, "..");
const workflow = fs.readFileSync(path.join(ROOT, ".github/workflows/ci-verify.yml"), "utf8");
const lefthook = fs.readFileSync(path.join(ROOT, "lefthook.yml"), "utf8");
const packageJson = JSON.parse(fs.readFileSync(path.join(ROOT, "package.json"), "utf8"));
const nodeAdapter = fs.readFileSync(path.join(ROOT, "scripts/ci/node-test.sh"), "utf8");

test("CI has one fail-closed root web lane", () => {
  assert.equal((workflow.match(/^ {2}node-ci:\s*$/gm) ?? []).length, 1);
  assert.equal((workflow.match(/^ {2}web:\s*$/gm) ?? []).length, 0);
  assert.match(workflow, /test-check-name: Web Contract/);
  assert.match(workflow, /run-test: true/);
  assert.match(workflow, /node-version: 24/);
  assert.match(nodeAdapter, /^#!\/usr\/bin\/env bash\nset -euo pipefail\n/u);
  assert.match(nodeAdapter, /^npm ci$/mu);
  assert.match(nodeAdapter, /^npm run check:web$/mu);
});

test("pre-push mirrors the root web lane", () => {
  assert.equal((lefthook.match(/^ {4}web:\s*$/gm) ?? []).length, 1);
  assert.match(lefthook, /run: npm run check:web/);
  assert.equal(fs.readFileSync(path.join(ROOT, ".node-version"), "utf8").trim(), "24");
  assert.match(packageJson.scripts["check:web"], /^node scripts\/node-version-contract\.mjs && /);
  assert.equal(packageJson.engines.node, ">=24.0.0");
});
