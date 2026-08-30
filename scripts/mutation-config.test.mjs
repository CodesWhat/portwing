import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";

const ROOT = path.resolve(import.meta.dirname, "..");
const workflow = fs.readFileSync(
  path.join(ROOT, ".github/workflows/quality-mutation-monthly.yml"),
  "utf8",
);

const excludedProductionDirs = new Set(["internal/banner/gen", "internal/integration"]);
const expectedFloors = new Map([
  ["./cmd/portwing", [100, 100]],
  ["./internal/server", [77.88, 94.12]],
  ["./internal/adapter", [84.68, 93.28]],
  ["./internal/adapter/drydock", [83.72, 92.47]],
  ["./internal/audit", [88.31, 100]],
  ["./internal/auth", [79.17, 97.96]],
  ["./internal/banner", [87.5, 16]],
  ["./internal/config", [82.22, 100]],
  ["./internal/docker", [100, 78.08]],
  ["./internal/edge", [85.21, 96.02]],
  ["./internal/generic", [85, 100]],
  ["./internal/log", null],
  ["./internal/mcp", [79.49, 79.59]],
  ["./internal/metrics", [90, 80]],
  ["./internal/pool", [50, 80]],
  ["./internal/protocol", [100, 100]],
]);

function productionPackagePaths() {
  const packages = [];
  function visit(relativeDir) {
    const absoluteDir = path.join(ROOT, relativeDir);
    const entries = fs.readdirSync(absoluteDir, { withFileTypes: true });
    const hasProductionGo = entries.some(
      (entry) => entry.isFile() && entry.name.endsWith(".go") && !entry.name.endsWith("_test.go"),
    );
    if (hasProductionGo && !excludedProductionDirs.has(relativeDir)) packages.push(relativeDir);
    for (const entry of entries) {
      if (entry.isDirectory()) visit(path.join(relativeDir, entry.name));
    }
  }
  for (const root of ["cmd", "internal"]) visit(root);
  return packages.sort();
}

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
        const match = line.match(/^ {12}([a-z_]+): (.+)$/u);
        if (match) fields[match[1]] = match[2];
      }
      fields.name = lines[0];
      return fields;
    });
}

function assertMutationWorkflow(source, expectedPackages = productionPackagePaths()) {
  const entries = matrixEntries(source);
  const actualPackages = entries.map((entry) => entry.package).sort();
  const expectedPackagePaths = expectedPackages.map((packagePath) => `./${packagePath}`).sort();
  if (JSON.stringify(actualPackages) !== JSON.stringify(expectedPackagePaths)) {
    throw new Error("every real production package must appear exactly once");
  }
  for (const entry of entries) {
    const expectedName = entry.package.replace(/^\.\/(?:cmd|internal)\//u, "").replaceAll("/", "-");
    assert.equal(entry.name, expectedName);
    assert.ok(expectedFloors.has(entry.package), `unexpected mutation package ${entry.package}`);
    const expectedFloor = expectedFloors.get(entry.package);
    if (entry.zero_mutants === "true") {
      assert.equal(
        entry.name,
        "log",
        "only the measured zero-mutant package may use the exception",
      );
      assert.equal(expectedFloor, null);
      assert.equal(entry.efficacy, undefined);
      assert.equal(entry.mcover, undefined);
    } else {
      assert.notEqual(expectedFloor, null);
      for (const field of ["efficacy", "mcover"]) {
        assert.match(
          entry[field] ?? "",
          /^(?:\d+)(?:\.\d+)?$/u,
          `${entry.name} is missing ${field}`,
        );
        assert.ok(Number(entry[field]) >= 0 && Number(entry[field]) <= 100);
      }
      if (Number(entry.efficacy) !== expectedFloor[0]) {
        throw new Error(`${entry.name} efficacy floor is weakened`);
      }
      if (Number(entry.mcover) !== expectedFloor[1]) {
        throw new Error(`${entry.name} mcover floor is weakened`);
      }
    }
    if (entry.name === "metrics") assert.equal(entry.workers, "1");
  }

  assert.doesNotMatch(source, /^ {4}continue-on-error:/mu, "mutation failures must block the job");
  const runStep = mutationRunStep(source);
  assert.doesNotMatch(
    runStep,
    /^ {8}continue-on-error:/mu,
    "Gremlins failures must not be ignored at step level",
  );
  assert.match(
    runStep,
    /set -euo pipefail/u,
    "the report pipeline must preserve Gremlins failures",
  );
  assert.match(
    runStep,
    /^ {10}if \[ "\$\{\{ matrix\.zero_mutants \|\| false \}\}" = "true" \]; then$/mu,
    "Gremlins script must preserve the zero-mutant branch",
  );
  assert.match(
    runStep,
    /^ {12}gremlins unleash --tags="" \\\n+(?:^ {14}[^\n#]*\\\n)*^ {14}--threshold-efficacy "\$\{\{ matrix\.efficacy \}\}" \\\n+^ {14}--threshold-mcover "\$\{\{ matrix\.mcover \}\}" \\\n+^ {14}"\$\{\{ matrix\.package \}\}" 2>&1 \| tee mutation-report\.txt$/mu,
    "Gremlins executable unleash command is malformed",
  );
  assert.match(runStep, /gremlins_args\+=\(--workers "\$\{\{ matrix\.workers \}\}"\)/u);
  assert.match(runStep, /\| tee mutation-report\.txt/u);
  assert.match(runStep, /grep -q "No results to report" mutation-report\.txt/u);
  assert.match(source, /name: Upload mutation report/u);
  assert.match(source, /path: mutation-report\.txt/u);
  assert.match(source, /internal\/banner\/gen is generator-only/u);
  assert.match(source, /internal\/integration is integration-test-only/u);
}

function mutationRunStep(source) {
  const start = source.indexOf("      - name: Run Gremlins mutation testing");
  const end = source.indexOf("\n      - name: ", start + 1);
  assert.notEqual(start, -1, "Gremlins step is missing");
  assert.notEqual(end, -1, "Gremlins step boundary is missing");
  return source.slice(start, end);
}

const matrixEfficacy = "$" + "{{ matrix.efficacy }}";
const matrixMcover = "$" + "{{ matrix.mcover }}";
const matrixZeroMutants = "$" + "{{ matrix.zero_mutants || false }}";
const expressionTrue = "$" + "{{ true }}";

function assertMutationFailure(source, expectedMessage, expectedPackages) {
  assert.throws(
    () => assertMutationWorkflow(source, expectedPackages),
    (error) => {
      assert.equal(error.message, expectedMessage);
      return true;
    },
  );
}

test("mutation workflow covers production packages with numeric ratchets", () => {
  assertMutationWorkflow(workflow);
});

test("mutation contract rejects a missing package", () => {
  assertMutationFailure(
    workflow.replace("./internal/pool", "./internal/missing"),
    "every real production package must appear exactly once",
  );
});

test("mutation contract rejects an undiscovered production package", () => {
  assertMutationFailure(workflow, "every real production package must appear exactly once", [
    ...productionPackagePaths(),
    "internal/future",
  ]);
});

test("mutation contract rejects a missing numeric floor", () => {
  assertMutationFailure(
    workflow.replace("            efficacy: 79.17\n", ""),
    "auth is missing efficacy",
  );
});

test("mutation contract rejects a weakened numeric floor", () => {
  assertMutationFailure(
    workflow.replace("            efficacy: 79.17\n", "            efficacy: 0\n"),
    "auth efficacy floor is weakened",
  );
});

test("mutation contract rejects swallowed Gremlins failures", () => {
  assertMutationFailure(
    workflow.replace("set -euo pipefail", "set -e"),
    "the report pipeline must preserve Gremlins failures",
  );
});

for (const value of ["true", expressionTrue]) {
  test(`mutation contract rejects step-level continue-on-error: ${value}`, () => {
    const source = workflow.replace(
      "      - name: Run Gremlins mutation testing\n        shell: bash",
      `      - name: Run Gremlins mutation testing\n        continue-on-error: ${value}\n        shell: bash`,
    );
    assertMutationFailure(source, "Gremlins failures must not be ignored at step level");
  });
}

test("mutation contract rejects a duplicate command package", () => {
  const source = workflow.replace(
    "          # internal/banner/gen is generator-only and has no mutation target.",
    "          - name: portwing-duplicate\n            package: ./cmd/portwing\n            efficacy: 100\n            mcover: 100\n          # internal/banner/gen is generator-only and has no mutation target.",
  );
  assertMutationFailure(source, "every real production package must appear exactly once");
});

test("mutation contract rejects threshold strings that only occur in comments", () => {
  const source = workflow
    .replace(
      `              --threshold-efficacy "${matrixEfficacy}"`,
      `              # --threshold-efficacy "${matrixEfficacy}"`,
    )
    .replace(
      `              --threshold-mcover "${matrixMcover}"`,
      `              # --threshold-mcover "${matrixMcover}"`,
    );
  assertMutationFailure(source, "Gremlins executable unleash command is malformed");
});

test("mutation contract rejects threshold strings in printf arguments", () => {
  const source = workflow
    .replace(
      `              --threshold-efficacy "${matrixEfficacy}" \\\n`,
      `              printf --threshold-efficacy "${matrixEfficacy}" \\\n`,
    )
    .replace(
      `              --threshold-mcover "${matrixMcover}" \\\n`,
      `              printf --threshold-mcover "${matrixMcover}" \\\n`,
    );
  assertMutationFailure(source, "Gremlins executable unleash command is malformed");
});

test("mutation contract rejects threshold strings that only occur in a later step", () => {
  const source = workflow
    .replace(`              --threshold-efficacy "${matrixEfficacy}" \\\n`, "")
    .replace(`              --threshold-mcover "${matrixMcover}" \\\n`, "")
    .replace(
      "      - name: Summarize\n        if: always()",
      `      - name: Summarize\n        run: echo --threshold-efficacy "${matrixEfficacy}" --threshold-mcover "${matrixMcover}"\n        if: always()`,
    );
  assertMutationFailure(source, "Gremlins executable unleash command is malformed");
});

test("mutation contract rejects a dead zero-mutant branch decoy", () => {
  const source = workflow.replace(
    `          if [ "${matrixZeroMutants}" = "true" ]; then`,
    "          if false; then",
  );
  assertMutationFailure(source, "Gremlins script must preserve the zero-mutant branch");
});
