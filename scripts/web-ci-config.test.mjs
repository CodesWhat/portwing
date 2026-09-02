import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";

const ROOT = path.resolve(import.meta.dirname, "..");
const workflow = fs.readFileSync(path.join(ROOT, ".github/workflows/ci-verify.yml"), "utf8");
const lefthook = fs.readFileSync(path.join(ROOT, "lefthook.yml"), "utf8");
const packageJson = JSON.parse(fs.readFileSync(path.join(ROOT, "package.json"), "utf8"));
const vercelConfigPath = path.join(ROOT, "vercel.json");
const vercelConfig = fs.existsSync(vercelConfigPath)
  ? JSON.parse(fs.readFileSync(vercelConfigPath, "utf8"))
  : {};
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

function mappingSection(source, mappingName) {
  const lines = source.split("\n");
  const start = lines.indexOf(`    ${mappingName}:`);
  if (start === -1) return "";
  let end = lines.length;
  for (let index = start + 1; index < lines.length; index += 1) {
    if (/^ {4}[a-z][a-z0-9-]*:/u.test(lines[index])) {
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
  const nodeWith = mappingSection(nodeJob, "with");
  assert.ok(nodeWith, "node-ci is missing its with mapping");
  const nodeWithLines = new Set(nodeWith.split("\n"));
  assert.ok(nodeWithLines.has("      test-check-name: Web Contract"));
  assert.ok(nodeWithLines.has("      run-test: true"));
  assert.ok(nodeWithLines.has("      node-version: 24"));
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

test("root web inputs must be exact YAML lines", () => {
  for (const input of ["test-check-name: Web Contract", "run-test: true", "node-version: 24"]) {
    assert.throws(() => assertRootWebLane(workflow.replace(`      ${input}`, `      # ${input}`)));
    assert.throws(() =>
      assertRootWebLane(workflow.replace(`      ${input}`, `      decoy-${input}`)),
    );
    const nodeJob = jobSection(workflow, "node-ci");
    const blockScalarDecoy = nodeJob
      .replace(`      ${input}\n`, "")
      .replace("    with:\n", `    decoy: |-\n      ${input}\n    with:\n`);
    assert.throws(() => assertRootWebLane(workflow.replace(nodeJob, blockScalarDecoy)));
  }
});

test("pre-push mirrors the root web lane", () => {
  assert.equal((lefthook.match(/^ {4}web:\s*$/gm) ?? []).length, 1);
  assert.match(lefthook, /run: npm run check:web/);
  assert.equal(fs.readFileSync(path.join(ROOT, ".node-version"), "utf8").trim(), "24");
  assert.match(packageJson.scripts["check:web"], /^node scripts\/node-version-contract\.mjs && /);
  assert.equal(packageJson.engines.node, ">=24.0.0");
});

test("check:web runs knip so dead-code coverage travels with the same gate", () => {
  assert.equal(packageJson.scripts.knip, "knip");
  assert.match(packageJson.scripts["check:web"], /npm run knip &&/);
  assert.ok(packageJson.devDependencies.knip, "knip must be a pinned devDependency");

  const knipConfig = JSON.parse(fs.readFileSync(path.join(ROOT, "knip.json"), "utf8"));
  for (const workspace of ["website", "docs", "analytics"]) {
    assert.ok(
      Object.hasOwn(knipConfig.workspaces ?? {}, workspace),
      `knip.json must cover the ${workspace} workspace`,
    );
  }
});

test("Vercel deploys main while disabling automatic preview deployments", () => {
  const deploymentEnabled = vercelConfig.git?.deploymentEnabled;
  assert.deepEqual(deploymentEnabled, { "**": false, main: true });

  function settingForBranch(branch, rules = deploymentEnabled) {
    const matchingSettings = Object.entries(rules)
      .filter(([pattern]) => path.matchesGlob(branch, pattern))
      .map(([, enabled]) => enabled);
    if (matchingSettings.includes(true)) return true;
    if (matchingSettings.includes(false)) return false;
    return true;
  }

  assert.equal(settingForBranch("feature/preview/change"), false);
  assert.equal(settingForBranch("main"), true);
  assert.equal(settingForBranch("main", { main: true, "**": false }), true);
  assert.equal(settingForBranch("unspecified", { main: false }), true);
});
