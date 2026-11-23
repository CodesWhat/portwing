// SPDX-License-Identifier: MIT

import assert from "node:assert/strict";
import fs from "node:fs";
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
  assert.match(workflow, /TODO\(ci-tree-receipt\)/u);
});

test("callers with no required nested names skip at the job level", () => {
  for (const id of ["go-ci", "node-ci", "codecov"]) {
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
  assert.equal(jobLevelIf(jobSection(workflow, "website-browser-checks")), "github.event_name != 'schedule'");
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
  assert.deepEqual(standDown.sort(), ["codecov", "go-ci", "node-ci"]);
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
    assert.equal(fuzzersJson({ result: "success", output: "", event, ref }), full, `${event} empty`);
    assert.equal(fuzzersJson({ result: "success", output: selected, event, ref }), selected);
    assert.equal(fuzzersJson({ result: "success", output: "[]", event, ref }), "[]");
  }
  assert.equal(
    fuzzersJson({ result: "failure", output: "", event: "push", ref: "refs/heads/dev/v1.0" }),
    "[]",
    "a dev push fuzzes nothing even when changes failed",
  );
});
