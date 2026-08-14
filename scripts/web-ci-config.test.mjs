import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";

const ROOT = path.resolve(import.meta.dirname, "..");
const workflow = fs.readFileSync(path.join(ROOT, ".github/workflows/ci-verify.yml"), "utf8");
const lefthook = fs.readFileSync(path.join(ROOT, "lefthook.yml"), "utf8");

test("CI has one fail-closed root web lane", () => {
  assert.equal((workflow.match(/^ {2}web:\s*$/gm) ?? []).length, 1);
  assert.match(workflow, /name: "Web Contract"/);
  assert.match(workflow, /node-version: 24/);
  assert.match(workflow, /run: npm ci/);
  assert.match(workflow, /run: npm run check:web/);
});

test("pre-push mirrors the root web lane", () => {
  assert.equal((lefthook.match(/^ {4}web:\s*$/gm) ?? []).length, 1);
  assert.match(lefthook, /run: npm run check:web/);
});
