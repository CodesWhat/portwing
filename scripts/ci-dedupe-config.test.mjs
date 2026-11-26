// SPDX-License-Identifier: MIT

import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const workflow = fs.readFileSync(path.join(ROOT, ".github/workflows/ci-verify.yml"), "utf8");

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

const jobLevelIf = (job) => job.match(/^ {4}if: (.*)$/mu)?.[1] ?? "";

test("push and pull_request triggers are kept; the push run stands down per SHA", () => {
  assert.match(workflow, /^ {2}push:\n {4}branches: \[main, "dev\/\*\*"\]$/mu);
  const changes = jobSection(workflow, "changes");
  assert.ok(changes, "missing changes job");
  assert.match(changes, /push_covered: \$\{\{ steps\.covered\.outputs\.push_covered \}\}/u);
  assert.match(changes, /push:refs\/heads\/dev\/\*\)/u, "only dev/** pushes may stand down");
  const covered = changes.split("      - name: Checkout")[0];
  assert.doesNotMatch(covered, /refs\/heads\/main/u, "a push to main must never stand down");
  assert.match(changes, /\.state == "open" and \.head\.sha == \$sha/u);
  assert.match(changes, /\.base\.ref == "main" or/u, "the covering PR must target main or dev/**");
});

test("callers with no required nested names skip at the job level", () => {
  for (const id of ["go-ci", "go-ci-receipt", "node-ci", "codecov"]) {
    const job = jobSection(workflow, id);
    assert.match(jobLevelIf(job), /!cancelled\(\)/u, `${id} must still run if changes fails`);
    assert.match(jobLevelIf(job), /needs\.changes\.outputs\.push_covered != 'true'/u, id);
  }
});

test("required direct jobs run in full: no job-level or step-level standing down", () => {
  for (const id of ["codeql", "website-browser-checks", "secrets", "dependency-review"]) {
    const job = jobSection(workflow, id);
    assert.doesNotMatch(job, /push_covered/u, `${id} must not stand down on a covered push`);
    assert.doesNotMatch(job, /^ {4}needs:/mu, `${id} must not wait on the changes job`);
    assert.doesNotMatch(jobLevelIf(job), /cancelled/u, id);
  }
  assert.equal(
    jobLevelIf(jobSection(workflow, "website-browser-checks")),
    "github.event_name != 'schedule'",
  );
  assert.equal(
    jobLevelIf(jobSection(workflow, "release-contract")),
    "",
    "Release Contract carries no if: condition",
  );
});

test("only caller jobs that post no required nested name stand down", () => {
  const required = [
    "Go CI / Build & Test",
    "Go CI / Lint",
    "Go CI / Govulncheck",
    "Go CI / Workflow Security",
    "Go CI / Commit Message",
    "Go CI / GoReleaser Config",
    "Go CI / Qlty Check",
    "Security: Secrets",
    "Dependency Review",
    "CodeQL Analysis",
    "Security: Gosec SAST",
    "Security: Grype Dependency Scan (Go + npm)",
    "Release Contract",
    "Website Browser Checks",
    "codecov/patch",
  ];
  const standDown = [];
  for (const match of workflow.matchAll(/^ {2}([a-z][a-z0-9-]*):$/gmu)) {
    if (/push_covered != 'true'/u.test(jobLevelIf(jobSection(workflow, match[1])))) {
      standDown.push(match[1]);
    }
  }
  assert.deepEqual(standDown.sort(), ["codecov", "go-ci", "go-ci-receipt", "node-ci"]);
  // Their own names are not required contexts; a skipped caller posts only these.
  for (const name of ["Go CI", "Node CI", "Coverage: Codecov Upload"]) {
    assert.ok(!required.includes(name), `${name} must not be a required context`);
  }
});

function fuzzersJson({ result, output, event, ref }) {
  const body = workflow.match(/^ {6}fuzzers-json: >-\n {8}\$\{\{ (.*) \}\}$/mu)?.[1];
  assert.ok(body, "fuzzers-json expression not found");
  const evaluate = new Function("needs", "github", `return (${body});`);
  return evaluate(
    { changes: { result, outputs: { fuzzers_json: output } } },
    { event_name: event, ref },
  );
}

test("fuzzers-json fails toward the full list, never toward empty", () => {
  const full = JSON.stringify(
    JSON.parse(fs.readFileSync(path.join(ROOT, ".github/ci-fuzzers.json"), "utf8")),
  );
  const selected = '[{"name":"FuzzEnvelope","pkg":"./internal/protocol/"}]';
  const fuzzing = [
    ["pull_request", "refs/pull/1/merge"],
    ["schedule", "refs/heads/main"],
    ["push", "refs/heads/main"],
  ];
  for (const [event, ref] of fuzzing) {
    for (const result of ["failure", "cancelled", "skipped"]) {
      assert.equal(fuzzersJson({ result, output: "", event, ref }), full, `${event} ${result}`);
    }
    assert.equal(
      fuzzersJson({ result: "success", output: "", event, ref }),
      full,
      `${event} empty`,
    );
    assert.equal(fuzzersJson({ result: "success", output: selected, event, ref }), selected);
    assert.equal(fuzzersJson({ result: "success", output: "[]", event, ref }), "[]");
  }
  assert.equal(
    fuzzersJson({ result: "failure", output: "", event: "push", ref: "refs/heads/dev/v1.0" }),
    "[]",
    "a dev push fuzzes nothing even when changes failed",
  );
});

// Runs the real "covered" step script against a fake `gh`.
function runCoveredStep({
  event = "push",
  ref = "refs/heads/dev/v1.0",
  states,
  base = "main",
  openSha,
}) {
  const sha = "a".repeat(40);
  const section = jobSection(workflow, "changes");
  const script = section
    .split("      - name: Is this push covered by a pull request run\n")[1]
    ?.split("        run: |\n")[1]
    ?.split("\n      - name:")[0]
    .split("\n")
    .map((line) => line.slice(10))
    .join("\n");
  assert.ok(script, "covered step script not found");
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "covered-step-"));
  try {
    const list = JSON.stringify([
      { number: 7, state: "open", head: { sha: openSha ?? sha }, base: { ref: base } },
    ]);
    fs.writeFileSync(path.join(dir, "list.json"), list);
    fs.writeFileSync(path.join(dir, "states"), states.join("\n"));
    fs.mkdirSync(path.join(dir, "bin"));
    fs.writeFileSync(
      path.join(dir, "bin", "gh"),
      `#!/usr/bin/env bash
case "$2" in
  */commits/*/pulls) cat "${dir}/list.json" ;;
  */pulls/*)
    n=$(($(cat "${dir}/n" 2>/dev/null || echo 0) + 1)); echo "$n" > "${dir}/n"
    sed -n "$((n > $(wc -l < "${dir}/states") + 1 ? $(wc -l < "${dir}/states") + 1 : n))p" "${dir}/states" ;;
  *) exit 1 ;;
esac
`,
      { mode: 0o755 },
    );
    fs.writeFileSync(path.join(dir, "bin", "sleep"), "#!/usr/bin/env bash\nexit 0\n", {
      mode: 0o755,
    });
    fs.writeFileSync(path.join(dir, "out"), "");
    const result = spawnSync("bash", ["-c", script], {
      env: {
        PATH: `${path.join(dir, "bin")}:${process.env.PATH}`,
        GITHUB_OUTPUT: path.join(dir, "out"),
        GH_TOKEN: "x",
        EVENT_NAME: event,
        REF: ref,
        REPO: "o/r",
        SHA: sha,
      },
      encoding: "utf8",
    });
    assert.equal(result.status, 0, result.stderr);
    return fs.readFileSync(path.join(dir, "out"), "utf8").trim();
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

test("a push stands down only when the PR has a merge commit, so its PR run exists", () => {
  for (const state of ["clean", "unstable", "blocked", "behind"]) {
    assert.equal(runCoveredStep({ states: [state] }), "push_covered=true", state);
  }
  // A conflicting PR gets no pull_request run (see promotion-conflict-suppresses-pr-run).
  for (const state of ["dirty", "unknown", "null", "draft", "has_hooks", "", "something-new"]) {
    assert.equal(runCoveredStep({ states: [state, state, state] }), "push_covered=false", state);
  }
});

test("an unsettled mergeable_state is re-read before giving up", () => {
  assert.equal(runCoveredStep({ states: ["unknown", "null", "clean"] }), "push_covered=true");
  assert.equal(runCoveredStep({ states: ["unknown", "unknown", "unknown"] }), "push_covered=false");
});

test("only open PRs for this exact head into main or dev/** can cover, and only dev pushes", () => {
  assert.equal(runCoveredStep({ states: ["clean"], base: "feature/x" }), "push_covered=false");
  assert.equal(
    runCoveredStep({ states: ["clean"], openSha: "b".repeat(40) }),
    "push_covered=false",
  );
  assert.equal(runCoveredStep({ states: ["clean"], base: "dev/v2.0" }), "push_covered=true");
  assert.equal(runCoveredStep({ states: ["clean"], ref: "refs/heads/main" }), "push_covered=false");
  assert.equal(runCoveredStep({ states: ["clean"], event: "pull_request" }), "push_covered=false");
});

// ---- tree receipt ----------------------------------------------------------

const receiptWorkflow = fs.readFileSync(
  path.join(ROOT, ".github/workflows/ci-go-receipt.yml"),
  "utf8",
);

const GATES = [
  "Go CI / Build & Test",
  "Go CI / Govulncheck",
  "Go CI / Lint",
  "Go CI / Workflow Security",
  "Go CI / GoReleaser Config",
  "Go CI / Qlty Check",
];

const HEAD = "h".repeat(40);
const MERGE = "m".repeat(40);

// Runs the real "Tree receipt" step script against a fake `gh`.
// trees: sha -> tree sha. runs: workflow_runs payload. jobs: run id -> job list.
function runReceiptStep({
  event = "pull_request",
  base = "main",
  head = "dev/v1.0",
  headRepo = "o/r",
  trees = { [HEAD]: "t1", [MERGE]: "t1" },
  runs = [],
  jobs = {},
  failApi = false,
  failJobsFor = [],
}) {
  const section = jobSection(workflow, "changes");
  const script = section
    .split("      - name: Tree receipt\n")[1]
    ?.split("        run: |\n")[1]
    ?.split("\n      - name:")[0]
    .split("\n")
    .map((line) => line.slice(10))
    .join("\n");
  assert.ok(script, "tree receipt step script not found");
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "receipt-step-"));
  try {
    fs.writeFileSync(path.join(dir, "trees.json"), JSON.stringify(trees));
    fs.writeFileSync(path.join(dir, "runs.json"), JSON.stringify({ workflow_runs: runs }));
    fs.writeFileSync(path.join(dir, "jobs.json"), JSON.stringify(jobs));
    fs.mkdirSync(path.join(dir, "bin"));
    fs.writeFileSync(
      path.join(dir, "bin", "gh"),
      `#!/usr/bin/env bash
${failApi ? "exit 1" : ""}
case "$2" in
  */git/commits/*) sha="\${2##*/}"; jq -er --arg sha "$sha" '.[$sha]' "${dir}/trees.json" ;;
  */workflows/ci-verify.yml/runs*) cat "${dir}/runs.json" ;;
  */actions/runs/*/jobs*) id="$2"; id="\${id#*/runs/}"; id="\${id%%/*}"; case " ${failJobsFor.join(" ")} " in *" $id "*) exit 1 ;; esac; jq -c --arg id "$id" '{jobs: (.[$id] // [])}' "${dir}/jobs.json" ;;
  *) exit 1 ;;
esac
`,
      { mode: 0o755 },
    );
    fs.writeFileSync(path.join(dir, "out"), "");
    const result = spawnSync("bash", ["-c", script], {
      env: {
        PATH: `${path.join(dir, "bin")}:${process.env.PATH}`,
        GITHUB_OUTPUT: path.join(dir, "out"),
        GH_TOKEN: "x",
        EVENT_NAME: event,
        REPO: "o/r",
        BASE_REF: base,
        HEAD_REF: head,
        HEAD_REPO: headRepo,
        HEAD_SHA: HEAD,
        MERGE_SHA: MERGE,
      },
      encoding: "utf8",
    });
    assert.equal(result.status, 0, result.stderr);
    const out = Object.fromEntries(
      fs
        .readFileSync(path.join(dir, "out"), "utf8")
        .trim()
        .split("\n")
        .map((line) => line.split("=")),
    );
    return { ...out, log: result.stdout };
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

const greenJobs = GATES.map((name) => ({ name, conclusion: "success" }));
const goodRun = (id, tree = "t1", extra = {}) => ({
  id,
  head_sha: "d".repeat(40),
  conclusion: "success",
  created_at: `2026-11-2${id % 10}T00:00:00Z`,
  head_commit: { tree_id: tree },
  ...extra,
});

test("tree receipt: a green push run on an identical tree yields a receipt", () => {
  const out = runReceiptStep({ runs: [goodRun(5)], jobs: { 5: greenJobs } });
  assert.equal(out.receipt, "true");
  assert.equal(out.receipt_run, "5");
  assert.equal(out.receipt_sha, "d".repeat(40));
  assert.match(out.log, /Tree receipt: true/u);
});

test("tree receipt: every doubt means no receipt and the full run", () => {
  const check = (label, args) => {
    const out = runReceiptStep(args);
    assert.deepEqual(
      { receipt: out.receipt, receipt_run: out.receipt_run, receipt_sha: out.receipt_sha },
      { receipt: "false", receipt_run: "", receipt_sha: "" },
      label,
    );
  };
  const ok = { runs: [goodRun(5)], jobs: { 5: greenJobs } };
  check("push event", { ...ok, event: "push" });
  check("base is not main", { ...ok, base: "dev/v2.0" });
  check("head is not dev/**", { ...ok, head: "feature/x" });
  check("fork head", { ...ok, headRepo: "x/r" });
  check("merge tree differs from head tree", {
    ...ok,
    trees: { [HEAD]: "t1", [MERGE]: "t2" },
  });
  check("API failure", { ...ok, failApi: true });
  check("tree not verified", { runs: [goodRun(5, "other")], jobs: { 5: greenJobs } });
  check("failed push run", {
    runs: [goodRun(5, "t1", { conclusion: "failure" })],
    jobs: { 5: greenJobs },
  });
  check("push run with no jobs (stood down)", { runs: [goodRun(5)], jobs: {} });
  for (const gate of GATES) {
    check(`gate not green: ${gate}`, {
      runs: [goodRun(5)],
      jobs: {
        5: greenJobs.map((job) => (job.name === gate ? { ...job, conclusion: "skipped" } : job)),
      },
    });
    check(`gate missing: ${gate}`, {
      runs: [goodRun(5)],
      jobs: { 5: greenJobs.filter((job) => job.name !== gate) },
    });
  }
});

test("tree receipt: skips a stood-down newer run and uses an older full one", () => {
  const out = runReceiptStep({ runs: [goodRun(6), goodRun(4)], jobs: { 6: [], 4: greenJobs } });
  assert.equal(out.receipt, "true");
  assert.equal(out.receipt_run, "4");
});

test("tree receipt: a jobs API error on any candidate means no receipt, never an older run", () => {
  const out = runReceiptStep({
    runs: [goodRun(6), goodRun(4)],
    jobs: { 6: greenJobs, 4: greenJobs },
    failJobsFor: [6],
  });
  assert.equal(out.receipt, "false");
  assert.equal(out.receipt_run, "");
  assert.equal(out.receipt_sha, "");
});

test("go-ci and go-ci-receipt are mutually exclusive and share one caller name", () => {
  const goCi = jobSection(workflow, "go-ci");
  const receipt = jobSection(workflow, "go-ci-receipt");
  assert.match(goCi, /^ {4}name: "Go CI"$/mu);
  assert.match(receipt, /^ {4}name: "Go CI"$/mu);
  assert.match(jobLevelIf(goCi), /needs\.changes\.outputs\.receipt != 'true'/u);
  assert.match(jobLevelIf(receipt), /needs\.changes\.outputs\.receipt == 'true'/u);
  assert.match(jobLevelIf(receipt), /push_covered != 'true'/u);
  assert.match(jobLevelIf(receipt), /!cancelled\(\)/u);
  assert.match(receipt, /uses: \.\/\.github\/workflows\/ci-go-receipt\.yml(?: {2}# zizmor.*)?$/mu);
  assert.match(jobSection(workflow, "codecov"), /needs: \[changes, go-ci, go-ci-receipt\]/u);
  // The fuzz selection must be identical on both paths.
  const lists = [...workflow.matchAll(/^ {6}fuzzers-json: >-\n {8}(.*)$/gmu)].map((m) => m[1]);
  assert.equal(lists.length, 2);
  assert.equal(lists[0], lists[1]);
});

test("the receipt workflow posts the required Go CI names without skipping any", () => {
  const names = [...receiptWorkflow.matchAll(/^ {4}name: (.*)$/gmu)].map((m) =>
    m[1].replace(/^"|"$/gu, ""),
  );
  for (const gate of GATES.concat("Go CI / Commit Message")) {
    assert.ok(names.includes(gate.replace("Go CI / ", "")), `missing job named ${gate}`);
  }
  // A job-level if: on a gate would post SKIPPED. Only fuzzing (not required) may have one.
  for (const match of receiptWorkflow.matchAll(/^ {2}([a-z][a-z0-9-]*):$/gmu)) {
    if (match[1] === "fuzz") continue;
    assert.equal(
      jobLevelIf(jobSection(receiptWorkflow, match[1])),
      "",
      `${match[1]} must not have an if:`,
    );
  }
  assert.doesNotMatch(receiptWorkflow, /continue-on-error/u);
});

test("the receipt covers only tree-only gates; the rest stay real", () => {
  for (const id of ["lint", "workflow-security", "goreleaser"]) {
    const job = jobSection(receiptWorkflow, id);
    assert.match(job, /Report the tree receipt/u, id);
    assert.doesNotMatch(job, /uses: actions\/checkout/u, `${id} must not do the work`);
  }
  const test_ = jobSection(receiptWorkflow, "test");
  assert.match(test_, /\.\/scripts\/ci\/go-test\.sh/u);
  assert.match(test_, /name: go-test-\$\{\{ github\.run_id \}\}-\$\{\{ github\.run_attempt \}\}/u);
  assert.match(jobSection(receiptWorkflow, "govulncheck"), /go-govulncheck\.sh/u);
  assert.match(jobSection(receiptWorkflow, "commit-message"), /commit-message\.sh/u);
  assert.match(jobSection(receiptWorkflow, "fuzz"), /go-fuzz\.sh/u);
});

test("jobs that must always run in full stay free of receipt logic", () => {
  for (const id of [
    "codeql",
    "website-browser-checks",
    "secrets",
    "dependency-review",
    "release-contract",
  ]) {
    assert.doesNotMatch(jobSection(workflow, id), /receipt/u, id);
  }
});

const stepIfs = (job) => [...job.matchAll(/^ {8}if: (.*)$/gmu)].map((m) => m[1]);

test("the receipt workflow's gate steps always run; only artifact uploads carry an if", () => {
  for (const id of [
    "test",
    "govulncheck",
    "commit-message",
    "lint",
    "workflow-security",
    "goreleaser",
    "qlty",
  ]) {
    const ifs = stepIfs(jobSection(receiptWorkflow, id));
    for (const condition of ifs) {
      assert.equal(
        condition,
        "always()",
        `${id} step if must be only the artifact upload's always()`,
      );
    }
  }
  const test_ = jobSection(receiptWorkflow, "test");
  assert.deepEqual(stepIfs(test_), ["always()"]);
  assert.match(
    test_,
    /- name: Upload Go test artifacts\n {8}if: always\(\)/u,
    "the only guarded step in Build & Test is the artifact upload",
  );
  assert.deepEqual(stepIfs(jobSection(receiptWorkflow, "govulncheck")), []);
  assert.deepEqual(stepIfs(jobSection(receiptWorkflow, "commit-message")), []);
  const fuzz = jobSection(receiptWorkflow, "fuzz");
  assert.deepEqual(stepIfs(fuzz), ["always()"]);
  assert.match(fuzz, /- name: Upload Go fuzz artifacts\n {8}if: always\(\)/u);
});

test("CodeQL, codecov/patch and Browser Checks are never short-circuited by the receipt", () => {
  assert.deepEqual(stepIfs(jobSection(workflow, "codeql")), []);
  assert.deepEqual(stepIfs(jobSection(workflow, "website-browser-checks")), ["failure()"]);
  const codecov = jobSection(workflow, "codecov");
  assert.doesNotMatch(jobLevelIf(codecov), /receipt/u);
  // Only the chained outcome guards: resolve -> download -> validate -> upload.
  assert.deepEqual(stepIfs(codecov), [
    "steps.coverage-artifact.outcome == 'success'",
    "steps.coverage-download.outcome == 'success'",
    "steps.coverage-profile.outcome == 'success'",
  ]);
  assert.match(codecov, /needs: \[changes, go-ci, go-ci-receipt\]/u);
});

test("the receipt's Qlty Check re-runs osv-scanner on the Go lockfile with its egress hosts", () => {
  const qlty = jobSection(receiptWorkflow, "qlty");
  assert.match(qlty, /name: Qlty Check/u, "must keep the required context name");
  assert.match(qlty, /Report the tree receipt/u);
  assert.match(qlty, /run: \.\/scripts\/ci\/go-osv-receipt\.sh/u);
  assert.match(qlty, /egress-policy: block/u);
  for (const host of ["api.osv.dev:443", "osv-vulnerabilities.storage.googleapis.com:443"]) {
    assert.ok(qlty.includes(host), `receipt Qlty Check must allow ${host}`);
  }
  const script = fs.readFileSync(path.join(ROOT, "scripts/ci/go-osv-receipt.sh"), "utf8");
  assert.match(script, /osv-scanner\/v2\/cmd\/osv-scanner@v\d+\.\d+\.\d+ scan source/u);
  assert.match(script, /--lockfile go\.mod/u);
  assert.match(script, /--config \.github\/osv-scanner\.toml/u);
  const config = fs.readFileSync(path.join(ROOT, ".github/osv-scanner.toml"), "utf8");
  const qltyConfig = fs.readFileSync(path.join(ROOT, ".qlty/qlty.toml"), "utf8");
  for (const [, id] of qltyConfig.matchAll(/"osv-scanner:([A-Z0-9-]+)"/gu)) {
    assert.ok(config.includes(`id = "${id}"`), `${id} is ignored in Qlty but not for the receipt`);
  }
});
