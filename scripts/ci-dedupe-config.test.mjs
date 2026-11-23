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

test("required direct jobs never skip at the job level, so they report success", () => {
  for (const id of ["codeql", "website-browser-checks"]) {
    const job = jobSection(workflow, id);
    assert.doesNotMatch(jobLevelIf(job), /push_covered/u, `${id} job-level skip would report SKIPPED`);
    assert.match(jobLevelIf(job), /!cancelled\(\)/u, `${id} must still run if changes fails`);
    assert.match(job, /^ {8}if: needs\.changes\.outputs\.push_covered != 'true'$/mu, id);
  }
  for (const id of ["secrets", "dependency-review"]) {
    assert.doesNotMatch(jobSection(workflow, id), /push_covered/u, `${id} stays cheap and unconditional`);
  }
  assert.equal(
    jobLevelIf(jobSection(workflow, "release-contract")),
    "",
    "Release Contract carries no if: condition",
  );
});
