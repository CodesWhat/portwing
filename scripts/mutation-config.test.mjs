import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";

const ROOT = path.resolve(import.meta.dirname, "..");
const workflow = fs.readFileSync(
  path.join(ROOT, ".github/workflows/quality-mutation-monthly.yml"),
  "utf8",
);

const productionPackages = [
  "adapter",
  "adapter-drydock",
  "audit",
  "auth",
  "banner",
  "config",
  "docker",
  "edge",
  "generic",
  "log",
  "mcp",
  "metrics",
  "pool",
  "protocol",
  "server",
];

function matrixEntries(source) {
  const start = source.indexOf("      matrix:\n");
  const end = source.indexOf("\n    steps:", start);
  assert.notEqual(start, -1, "mutation matrix is missing");
  assert.notEqual(end, -1, "mutation steps are missing");
  return source
    .slice(start, end)
    .split("\n          - name: ")
    .slice(1)
    .map((entry) => {
      const lines = entry.split("\n");
      const fields = {};
      for (const line of lines) {
        const match = line.match(/^            ([a-z_]+): (.+)$/u);
        if (match) fields[match[1]] = match[2];
      }
      fields.name = lines[0];
      return fields;
    });
}

function assertMutationWorkflow(source) {
  const entries = matrixEntries(source);
  assert.deepEqual(
    entries.map((entry) => entry.name).sort(),
    productionPackages.slice().sort(),
    "every real production package must appear exactly once",
  );
  for (const entry of entries) {
    const expectedPackage =
      entry.name === "adapter-drydock" ? "./internal/adapter/drydock" : `./internal/${entry.name}`;
    assert.equal(entry.package, expectedPackage);
    if (entry.zero_mutants === "true") {
      assert.equal(entry.name, "log", "only the measured zero-mutant package may use the exception");
      assert.equal(entry.efficacy, undefined);
      assert.equal(entry.mcover, undefined);
    } else {
      for (const field of ["efficacy", "mcover"]) {
        assert.match(entry[field] ?? "", /^(?:\d+)(?:\.\d+)?$/u, `${entry.name} is missing ${field}`);
        assert.ok(Number(entry[field]) >= 0 && Number(entry[field]) <= 100);
      }
    }
  }

  assert.doesNotMatch(source, /^    continue-on-error:/mu, "mutation failures must block the job");
  const runStep = source.slice(source.indexOf("      - name: Run Gremlins mutation testing"));
  assert.match(runStep, /set -euo pipefail/u, "the report pipeline must preserve Gremlins failures");
  assert.match(runStep, /--threshold-efficacy "\$\{\{ matrix\.efficacy \}\}"/u);
  assert.match(runStep, /--threshold-mcover "\$\{\{ matrix\.mcover \}\}"/u);
  assert.match(runStep, /\| tee mutation-report\.txt/u);
  assert.match(runStep, /grep -q "No results to report" mutation-report\.txt/u);
  assert.match(source, /name: Upload mutation report/u);
  assert.match(source, /path: mutation-report\.txt/u);
  assert.match(source, /internal\/banner\/gen is generator-only/u);
  assert.match(source, /internal\/integration is integration-test-only/u);
}

test("mutation workflow covers production packages with numeric ratchets", () => {
  assertMutationWorkflow(workflow);
});

test("mutation contract rejects a missing package", () => {
  assert.throws(() => assertMutationWorkflow(workflow.replace("./internal/pool", "./internal/missing")));
});

test("mutation contract rejects a missing numeric floor", () => {
  assert.throws(() => assertMutationWorkflow(workflow.replace("            efficacy: 79.17\n", "")));
});

test("mutation contract rejects swallowed Gremlins failures", () => {
  assert.throws(() => assertMutationWorkflow(workflow.replace("set -euo pipefail", "set -e")));
});
