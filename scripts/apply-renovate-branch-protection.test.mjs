import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const script = fileURLToPath(new URL("./apply-renovate-branch-protection.sh", import.meta.url));
const fakeGh = String.raw`#!/usr/bin/env node
const fs = require("node:fs");
const path = require("node:path");
const root = process.env.FAKE_RULESET_DIR;
const mode = process.env.FAKE_RULESET_CASE;
const args = process.argv.slice(2);
const route = args.find((a) => a.startsWith("repos/"));
const methodAt = args.indexOf("--method");
const method = methodAt < 0 ? "GET" : args[methodAt + 1];
fs.appendFileSync(path.join(root, "calls.jsonl"), JSON.stringify({ method, route }) + "\n");
const target = { id: 42, name: "Protect Renovate branches" };
if (method === "POST" || method === "PUT") {
  fs.writeFileSync(path.join(root, "payload.json"), fs.readFileSync(0));
  process.stdout.write("{}");
  process.exit(0);
}
if (route === "repos/fixture/project/rulesets") {
  const counter = path.join(root, "lookup-count");
  const count = fs.existsSync(counter) ? Number(fs.readFileSync(counter, "utf8")) + 1 : 1;
  fs.writeFileSync(counter, String(count));
  if (mode === "outage" || (mode === "verify-outage" && count > 1)) {
    process.stderr.write("fixture API unavailable\n");
    process.exit(23);
  }
  let pages = [[target]];
  if (mode === "missing" && count === 1) pages = [[]];
  if (mode === "paginated") pages = [[{ id: 11, name: "Other ruleset" }], [target]];
  if (mode === "duplicate" || (mode === "verify-duplicate" && count > 1)) pages = [[target], [{ ...target, id: 43 }]];
  if (mode === "invalid-id") pages = [[{ ...target, id: "not-an-id" }]];
  if (mode === "fractional-id") pages = [[{ ...target, id: 42.5 }]];
  if (mode === "verify-missing" && count > 1) pages = [[]];
  if (args.includes("--jq")) {
    const rows = args.includes("--paginate") ? pages.flat() : pages[0];
    process.stdout.write(rows.filter((r) => r.name === target.name).map((r) => String(r.id)).join("\n"));
  } else if (args.includes("--slurp")) {
    process.stdout.write(JSON.stringify(args.includes("--paginate") ? pages : [pages[0]]));
  } else {
    process.stdout.write(JSON.stringify(pages[0]));
  }
  process.exit(0);
}
if (route !== "repos/fixture/project/rulesets/42") {
  process.stderr.write("unexpected detail route: " + route);
  process.exit(24);
}
process.stdout.write(JSON.stringify(target));
`;

function runFixture(t, scenario) {
  const root = mkdtempSync(join(tmpdir(), "pw-ruleset-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const bin = join(root, "bin");
  mkdirSync(bin);
  writeFileSync(join(bin, "gh"), fakeGh, { mode: 0o755 });
  const result = spawnSync("bash", [script], {
    encoding: "utf8",
    timeout: 10_000,
    env: {
      ...process.env,
      PATH: `${bin}:${process.env.PATH}`,
      REPO: "fixture/project",
      RENOVATE_APP_ID: "2740",
      FAKE_RULESET_DIR: root,
      FAKE_RULESET_CASE: scenario,
    },
  });
  assert.ifError(result.error);
  const calls = readFileSync(join(root, "calls.jsonl"), "utf8").trim().split("\n").map(JSON.parse);
  const payload = existsSync(join(root, "payload.json"))
    ? JSON.parse(readFileSync(join(root, "payload.json"), "utf8"))
    : null;
  return { ...result, calls, payload };
}

function writes(result) {
  return result.calls.filter((call) => call.method === "POST" || call.method === "PUT");
}

for (const scenario of ["outage", "duplicate", "invalid-id", "fractional-id"]) {
  test(`ruleset lookup ${scenario} aborts before changing protection`, (t) => {
    const result = runFixture(t, scenario);
    assert.notEqual(result.status, 0, result.stdout + result.stderr);
    assert.deepEqual(writes(result), [], result.stdout + result.stderr);
  });
}

for (const scenario of ["existing", "missing", "paginated"]) {
  test(`ruleset ${scenario} preserves the policy and selects one ID`, (t) => {
    const result = runFixture(t, scenario);
    assert.equal(result.status, 0, result.stdout + result.stderr);
    assert.deepEqual(writes(result), [
      {
        method: scenario === "missing" ? "POST" : "PUT",
        route: `repos/fixture/project/rulesets${scenario === "missing" ? "" : "/42"}`,
      },
    ]);
    assert.equal(result.calls.at(-1).route, "repos/fixture/project/rulesets/42");
    assert.equal(result.payload.enforcement, "active");
    assert.deepEqual(result.payload.rules, [{ type: "deletion" }]);
    assert.deepEqual(result.payload.conditions.ref_name, {
      include: ["refs/heads/renovate/**"],
      exclude: [],
    });
    assert.deepEqual(result.payload.bypass_actors, [
      { actor_id: 2740, actor_type: "Integration", bypass_mode: "always" },
    ]);
  });
}

for (const scenario of ["verify-outage", "verify-duplicate", "verify-missing"]) {
  test(`ruleset ${scenario} cannot report successful verification`, (t) => {
    const result = runFixture(t, scenario);
    assert.notEqual(result.status, 0, result.stdout + result.stderr);
    assert.equal(writes(result).length, 1);
    assert.doesNotMatch(result.stdout, /Applied[.] Effective ruleset/);
    assert.equal(
      result.calls.filter(
        (call) => call.method === "GET" && call.route !== "repos/fixture/project/rulesets",
      ).length,
      0,
    );
  });
}
