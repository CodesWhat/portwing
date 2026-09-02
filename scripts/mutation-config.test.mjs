import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

const ROOT = path.resolve(import.meta.dirname, "..");
const workflow = fs.readFileSync(
  path.join(ROOT, ".github/workflows/quality-mutation-monthly.yml"),
  "utf8",
);
const GATE = path.join(ROOT, "scripts/ci/mutation-gate.sh");

const excludedProductionDirs = new Set(["internal/banner/gen", "internal/integration"]);
// PW-6.8: the 6 mutator types Gremlins disables by default. They are measured
// by the advisory job and must stay out of the gating matrix, whose floors were
// all measured without them.
const advisoryFlags = [
  "--invert-logical",
  "--invert-bitwise",
  "--invert-bwassign",
  "--invert-assignments",
  "--invert-loopctrl",
  "--remove-self-assignments",
];
const advisoryPackages = [
  "./internal/auth",
  "./internal/edge",
  "./internal/adapter",
  "./internal/server",
  "./internal/mcp",
];
const timeoutCoefficients = new Set(["pool", "protocol"]);
const expectedFloors = new Map([
  ["./cmd/portwing", [100, 100]],
  ["./internal/server", [77.88, 94.12]],
  ["./internal/adapter", [84.68, 93.28]],
  ["./internal/adapter/drydock", [82.5, 91.95]],
  ["./internal/audit", [88.31, 100]],
  ["./internal/auth", [79.17, 97.96]],
  ["./internal/banner", [76.92, 38.24]],
  ["./internal/config", [82.22, 100]],
  ["./internal/docker", [90.39, 78.08]],
  ["./internal/edge", [74.73, 91]],
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
    // Gremlins budgets each mutant at the coverage-gathering time times the
    // coefficient. Both of these packages measure well under a second, so the
    // default of 3 timed their mutants out and scored them 0.00/0.00 while
    // verifying nothing. See PW-6.2.
    if (timeoutCoefficients.has(entry.name) && entry.timeout_coefficient !== "40") {
      throw new Error(`${entry.name} needs its timeout coefficient`);
    }
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
    /^ {12}gremlins unleash --tags="" \\\n+^ {14}"\$\{gremlins_args\[@\]\+"\$\{gremlins_args\[@\]\}"\}" \\\n+^ {14}--output mutation-report\.json \\\n+^ {14}"\$\{\{ matrix\.package \}\}" 2>&1 \| tee mutation-report\.txt$/mu,
    "Gremlins executable unleash command is malformed",
  );
  // The floors are only real because mutation-gate.sh reads the report and
  // exits non-zero. It has to be the next command in the same step, on its
  // own, so nothing can swallow its status. See PW-6.1.
  assert.match(
    runStep,
    /^ {12}\.\/scripts\/ci\/mutation-gate\.sh mutation-report\.json "\$\{\{ matrix\.efficacy \}\}" "\$\{\{ matrix\.mcover \}\}"$/mu,
    "the mutation floors must be enforced by scripts/ci/mutation-gate.sh",
  );
  // Gremlins' own threshold flags parse and then do nothing: Viper returns a
  // bound float64 pflag as a string, so report.assess() reads 0 and never
  // fires. Anything that reintroduces them is claiming a gate that is not
  // there. See the header of scripts/ci/mutation-gate.sh.
  assert.doesNotMatch(
    runStep,
    /--threshold-(?:efficacy|mcover)/u,
    "Gremlins' own threshold flags are inert and must not be trusted",
  );
  assert.doesNotMatch(
    runStep,
    /^ {10}if false; then$/mu,
    "Gremlins script must preserve the zero-mutant branch",
  );
  assert.match(runStep, /gremlins_args\+=\(--workers "\$\{\{ matrix\.workers \}\}"\)/u);
  assert.match(
    runStep,
    /gremlins_args\+=\(--timeout-coefficient "\$\{\{ matrix\.timeout_coefficient \}\}"\)/u,
  );
  assert.match(runStep, /\| tee mutation-report\.txt/u);
  assert.match(runStep, /grep -q "No results to report" mutation-report\.txt/u);
  assert.match(source, /name: Upload mutation report/u);
  assert.match(source, /^ {12}mutation-report\.txt$/mu);
  assert.match(source, /^ {12}mutation-report\.json$/mu);
  assert.match(source, /internal\/banner\/gen is generator-only/u);
  assert.match(source, /internal\/integration is integration-test-only/u);
  assertCanaryJob(source);
  assertAdvisoryJob(source, entries);
}

// PW-6.8: the advisory job exists to measure the mutators the gating matrix
// deliberately leaves off. Two things have to stay true for that split to mean
// anything: the advisory job must never gate, and the gating jobs must never
// pick up the advisory mutators, because every floor in the matrix was measured
// without them.
function assertAdvisoryJob(source, entries) {
  const advisory = jobBlock(source, "mutation-advisory", "the advisory mutator job is missing");
  const runStep = mutationRunStep(source);

  assert.doesNotMatch(
    advisory,
    /mutation-gate\.sh/u,
    "the advisory job must not call the gate, or it stops being advisory",
  );
  for (const flag of advisoryFlags) {
    assert.match(
      advisory,
      new RegExp(`^ {12}${escapeForRegExp(flag)}$`, "mu"),
      `the advisory job must enable ${flag}`,
    );
    assert.doesNotMatch(
      runStep,
      new RegExp(escapeForRegExp(flag), "u"),
      `${flag} would invalidate every floor measured without it`,
    );
  }

  // The advisory table prints each package against the floor the matrix
  // measured, so the two have to be the same number.
  for (const packagePath of advisoryPackages) {
    const entry = entries.find((candidate) => candidate.package === packagePath);
    assert.ok(entry, `${packagePath} is advisory but not in the gating matrix`);
    assert.match(
      advisory,
      new RegExp(
        `^ {12}"${escapeForRegExp(packagePath)}\\|${entry.name}\\|${entry.efficacy}"$`,
        "mu",
      ),
      `${entry.name}'s advisory row must carry the efficacy floor the matrix measured`,
    );
  }

  // A flag Gremlins renamed would be swallowed by the per-package guard and
  // read as an unavailable package, and a run that discovers no advisory
  // mutants at all measured nothing. Both are the PW-6.1 failure, so both have
  // to be loud.
  assert.match(
    advisory,
    /^ {10}help_text="\$\(gremlins unleash --help\)"$/mu,
    "the advisory job must check the flag names against the binary",
  );
  assert.match(
    advisory,
    /^ {10}if \[ "\$\{advisory_mutants\}" -eq 0 \]; then$/mu,
    "the advisory job must fail when the advisory mutators produce nothing",
  );
  assert.match(advisory, /GITHUB_STEP_SUMMARY/u, "the advisory job must publish its table");
}

// Gremlins reads .gremlins.yaml from the Go module root and the working
// directory. One at the repo root would change the mutator set every gating job
// measures against, invalidating all 16 floors without touching the workflow.
// That is why the advisory mutators are command-line flags on one job.
function assertNoRootGremlinsConfig(exists) {
  for (const name of [".gremlins.yaml", ".gremlins.yml"]) {
    if (exists(name)) {
      throw new Error(`a repo-root ${name} would change every gating floor's mutator set`);
    }
  }
}

function escapeForRegExp(value) {
  return value.replaceAll(/[.*+?^${}()|[\]\\]/gu, "\\$&");
}

function jobBlock(source, jobName, missingMessage) {
  const start = source.indexOf(`  ${jobName}:\n`);
  assert.notEqual(start, -1, missingMessage);
  const rest = source.slice(start + 1);
  const next = rest.search(/^ {2}(?:[a-z][a-z0-9-]*:|#)/mu);
  return next === -1 ? source.slice(start) : source.slice(start, start + 1 + next);
}

// PW-6.1: a gate nobody proves can fail is the defect being fixed. The canary
// job runs the real binary and asserts both directions, so a future change
// that neuters the gate turns this workflow red instead of quietly green.
function assertCanaryJob(source) {
  const canary = canaryJob(source);
  assert.doesNotMatch(canary, /^ {4}continue-on-error:/mu, "the canary must block");
  assert.doesNotMatch(canary, /^ {8}continue-on-error:/mu, "the canary must block");
  assert.match(
    canary,
    /^ {10}if \.\/scripts\/ci\/mutation-gate\.sh canary-report\.json 101 101; then$/mu,
    "the canary must assert an unreachable floor is rejected",
  );
  assert.match(
    canary,
    /^ {10}\.\/scripts\/ci\/mutation-gate\.sh canary-report\.json 0 0$/mu,
    "the canary must assert a reachable floor is still accepted",
  );
  assert.match(
    canary,
    /^ {10}if \.\/scripts\/ci\/mutation-gate\.sh no-such-report\.json 0 0; then$/mu,
    "the canary must assert a missing report is rejected",
  );
  assert.match(
    canary,
    /^ {12}--timeout-coefficient 40 \\$/mu,
    "the canary runs pool, so it needs pool's timeout coefficient",
  );
}

function canaryJob(source) {
  const start = source.indexOf("  gate-canary:\n");
  assert.notEqual(start, -1, "the mutation gate canary job is missing");
  return source.slice(start);
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
const advisoryMutants = "$" + "{advisory_mutants}";
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

test("mutation contract rejects a machine-readable report the gate cannot read", () => {
  const source = workflow.replace(
    "              --output mutation-report.json \\\n",
    "              # --output mutation-report.json \\\n",
  );
  assertMutationFailure(source, "Gremlins executable unleash command is malformed");
});

test("mutation contract rejects a shell separator before the package argument", () => {
  const source = workflow.replace(
    '            gremlins unleash --tags="" \\\n',
    '            gremlins unleash --tags="" \\\n              ; printf "%s\\n" \\\n',
  );
  assertMutationFailure(source, "Gremlins executable unleash command is malformed");
});

// PW-6.1's actual defect: Gremlins' own --threshold-efficacy/--threshold-mcover
// exit 0 no matter what the package measured, so a workflow that passes them
// and nothing else is not gating. These four tests are what "the gate can
// never fail" looks like from the config side.
test("mutation contract rejects a commented-out gate invocation", () => {
  const source = workflow.replace(
    `            ./scripts/ci/mutation-gate.sh mutation-report.json "${matrixEfficacy}" "${matrixMcover}"`,
    `            # ./scripts/ci/mutation-gate.sh mutation-report.json "${matrixEfficacy}" "${matrixMcover}"`,
  );
  assertMutationFailure(
    source,
    "the mutation floors must be enforced by scripts/ci/mutation-gate.sh",
  );
});

test("mutation contract rejects a gate invocation whose status is swallowed", () => {
  const source = workflow.replace(
    `            ./scripts/ci/mutation-gate.sh mutation-report.json "${matrixEfficacy}" "${matrixMcover}"`,
    `            ./scripts/ci/mutation-gate.sh mutation-report.json "${matrixEfficacy}" "${matrixMcover}" || true`,
  );
  assertMutationFailure(
    source,
    "the mutation floors must be enforced by scripts/ci/mutation-gate.sh",
  );
});

test("mutation contract rejects a gate invocation moved to a later step", () => {
  const source = workflow
    .replace(
      `            ./scripts/ci/mutation-gate.sh mutation-report.json "${matrixEfficacy}" "${matrixMcover}"\n`,
      "",
    )
    .replace(
      "      - name: Summarize\n        if: always()",
      `      - name: Summarize\n        run: ./scripts/ci/mutation-gate.sh mutation-report.json "${matrixEfficacy}" "${matrixMcover}"\n        if: always()`,
    );
  assertMutationFailure(
    source,
    "the mutation floors must be enforced by scripts/ci/mutation-gate.sh",
  );
});

test("mutation contract rejects threshold flags inserted into the unleash command", () => {
  const source = workflow.replace(
    "              --output mutation-report.json \\\n",
    `              --output mutation-report.json \\\n              --threshold-efficacy "${matrixEfficacy}" \\\n              --threshold-mcover "${matrixMcover}" \\\n`,
  );
  assertMutationFailure(source, "Gremlins executable unleash command is malformed");
});

test("mutation contract rejects a return to Gremlins' inert threshold flags", () => {
  const source = workflow.replace(
    `          if [ "${matrixZeroMutants}" = "true" ]; then`,
    `          gremlins_args+=(--threshold-efficacy "${matrixEfficacy}")\n` +
      `          gremlins_args+=(--threshold-mcover "${matrixMcover}")\n` +
      `          if [ "${matrixZeroMutants}" = "true" ]; then`,
  );
  assertMutationFailure(source, "Gremlins' own threshold flags are inert and must not be trusted");
});

test("mutation contract rejects a dropped pool timeout coefficient", () => {
  const source = workflow.replace("            timeout_coefficient: 40\n", "");
  assertMutationFailure(source, "pool needs its timeout coefficient");
});

test("mutation contract rejects a dropped protocol timeout coefficient", () => {
  const source = workflow.replace(
    "            timeout_coefficient: 40\n            efficacy: 100\n",
    "            efficacy: 100\n",
  );
  assertMutationFailure(source, "protocol needs its timeout coefficient");
});

test("mutation contract rejects a canary that can flake on the timeout cliff", () => {
  const source = workflow.replace("            --timeout-coefficient 40 \\\n", "");
  assertMutationFailure(source, "the canary runs pool, so it needs pool's timeout coefficient");
});

test("mutation contract rejects a missing canary job", () => {
  const source = workflow.replace("  gate-canary:\n", "  gate-canary-disabled:\n");
  assertMutationFailure(source, "the mutation gate canary job is missing");
});

test("mutation contract rejects a canary that never proves the gate can fail", () => {
  const source = workflow.replace(
    "          if ./scripts/ci/mutation-gate.sh canary-report.json 101 101; then",
    "          if ./scripts/ci/mutation-gate.sh canary-report.json 0 0; then",
  );
  assertMutationFailure(source, "the canary must assert an unreachable floor is rejected");
});

test("mutation contract rejects a non-blocking canary", () => {
  const source = workflow.replace(
    "      - name: Prove the mutation gate still fails below a floor\n        shell: bash",
    "      - name: Prove the mutation gate still fails below a floor\n        continue-on-error: true\n        shell: bash",
  );
  assertMutationFailure(source, "the canary must block");
});

test("mutation contract rejects a dead zero-mutant branch decoy", () => {
  const source = workflow.replace(
    `          if [ "${matrixZeroMutants}" = "true" ]; then`,
    "          if false; then",
  );
  assertMutationFailure(source, "Gremlins script must preserve the zero-mutant branch");
});

test("mutation contract rejects an outer dead wrapper", () => {
  const source = workflow
    .replace(
      `          if [ "${matrixZeroMutants}" = "true" ]; then`,
      `          if false; then
            if [ "${matrixZeroMutants}" = "true" ]; then`,
    )
    .replace("          fi\n        env:", "          fi\n          fi\n        env:");
  assertMutationFailure(source, "Gremlins script must preserve the zero-mutant branch");
});

// PW-6.8. The advisory job is only worth having if it stays advisory and stays
// separate: a gate call in it would block on unratcheted mutators, and an
// advisory flag in the matrix would drop every floor measured without it.
test("mutation contract rejects a missing advisory job", () => {
  assertMutationFailure(
    workflow.replace("  mutation-advisory:\n", "  mutation-advisory-disabled:\n"),
    "the advisory mutator job is missing",
  );
});

test("mutation contract rejects an advisory job that gates", () => {
  const source = workflow.replace(
    `          echo "advisory: ${advisoryMutants} mutants came from the default-disabled mutators"`,
    "          ./scripts/ci/mutation-gate.sh mutation-advisory-auth.json 79.17 97.96",
  );
  assertMutationFailure(
    source,
    "the advisory job must not call the gate, or it stops being advisory",
  );
});

test("mutation contract rejects a dropped advisory mutator", () => {
  assertMutationFailure(
    workflow.replace("            --invert-logical\n", ""),
    "the advisory job must enable --invert-logical",
  );
});

test("mutation contract rejects an advisory mutator in the gating run", () => {
  const source = workflow.replace(
    `          if [ "${matrixZeroMutants}" = "true" ]; then`,
    `          gremlins_args+=(--invert-logical)\n          if [ "${matrixZeroMutants}" = "true" ]; then`,
  );
  assertMutationFailure(
    source,
    "--invert-logical would invalidate every floor measured without it",
  );
});

test("mutation contract rejects an advisory floor that drifts from the matrix", () => {
  assertMutationFailure(
    workflow.replace('"./internal/auth|auth|79.17"', '"./internal/auth|auth|79.16"'),
    "auth's advisory row must carry the efficacy floor the matrix measured",
  );
});

test("mutation contract rejects an advisory job that never checks the flag names", () => {
  assertMutationFailure(
    workflow.replace('          help_text="$(gremlins unleash --help)"\n', ""),
    "the advisory job must check the flag names against the binary",
  );
});

test("mutation contract rejects an advisory job that can pass having measured nothing", () => {
  assertMutationFailure(
    workflow.replace(
      `          if [ "${advisoryMutants}" -eq 0 ]; then`,
      "          if false; then",
    ),
    "the advisory job must fail when the advisory mutators produce nothing",
  );
});

test("mutation contract rejects a repo-root Gremlins config", () => {
  assertNoRootGremlinsConfig((name) => fs.existsSync(path.join(ROOT, name)));
  assert.throws(
    () => assertNoRootGremlinsConfig((name) => name === ".gremlins.yaml"),
    /would change every gating floor's mutator set/u,
  );
});

// The other half of PW-6.1. The workflow tests above prove the gate is wired
// in; these prove it actually rejects a below-floor measurement, which is the
// thing Gremlins' own threshold flags silently stopped doing.
function runGate(report, efficacyFloor, mcoverFloor) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "mutation-gate-"));
  try {
    const reportPath = path.join(dir, "report.json");
    if (report !== null) fs.writeFileSync(reportPath, report);
    return spawnSync("bash", [GATE, reportPath, String(efficacyFloor), String(mcoverFloor)], {
      encoding: "utf8",
    });
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

// auth's real numbers. Its floors were copied from Gremlins' two-decimal
// report, so a gate comparing full JSON precision would reject the very run
// the floors came from.
const authReport = JSON.stringify({
  test_efficacy: 79.16666666666666,
  mutations_coverage: 97.95918367346938,
  mutants_killed: 76,
  mutants_lived: 20,
  mutants_not_covered: 2,
});

test("mutation gate passes a package sitting exactly on its floor", () => {
  const result = runGate(authReport, 79.17, 97.96);
  assert.equal(result.status, 0, result.stderr);
});

test("mutation gate fails a package below its efficacy floor", () => {
  const result = runGate(authReport, 79.18, 97.96);
  assert.equal(result.status, 1);
  assert.match(result.stderr, /test efficacy 79\.17% is below its floor of 79\.18%/u);
});

test("mutation gate fails a package below its mutator coverage floor", () => {
  const result = runGate(authReport, 79.17, 97.97);
  assert.equal(result.status, 1);
  assert.match(result.stderr, /mutator coverage 97\.96% is below its floor of 97\.97%/u);
});

test("mutation gate fails when every mutant timed out", () => {
  // internal/protocol's shape before PW-6.2: five TIMED OUT mutants, which
  // Gremlins scores 0.00/0.00 as though it had measured something.
  const report = JSON.stringify({
    test_efficacy: 0,
    mutations_coverage: 0,
    mutants_killed: 0,
    mutants_lived: 0,
    mutants_not_covered: 0,
  });
  const result = runGate(report, 100, 100);
  assert.equal(result.status, 1);
  assert.match(result.stderr, /no mutant returned a KILLED or LIVED verdict/u);
});

test("mutation gate fails when Gremlins wrote no report", () => {
  const result = runGate(null, 0, 0);
  assert.equal(result.status, 1);
  assert.match(result.stderr, /Gremlins wrote no report/u);
});

test("mutation gate fails on an unreadable report rather than passing it", () => {
  const result = runGate("not json", 0, 0);
  assert.equal(result.status, 1);
  assert.match(result.stderr, /not a readable Gremlins JSON report/u);
});

test("mutation gate rejects a floor that is not a number", () => {
  const result = runGate(authReport, "notafloor", 0);
  assert.equal(result.status, 2);
  assert.match(result.stderr, /is not a plain decimal percentage/u);
});
