// SPDX-License-Identifier: MIT
//
// The toolchain-free cases of scripts/ci/select-fuzzers.sh: every path here
// returns before the import graph is built, so no Go is needed and
// `npm run test:web` can run them. The `go list` cases are
// scripts/ci/select-fuzzers.go.test.mjs.

import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { after, before, describe, it } from "node:test";
import { fileURLToPath } from "node:url";

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const selector = resolve(repoRoot, "scripts", "ci", "select-fuzzers.sh");
const LIST = [
  { name: "FuzzA", pkg: "./internal/a/" },
  { name: "FuzzB", pkg: "./internal/b/" },
];
const ALL = LIST.map((entry) => entry.name);
const names = (result) => JSON.parse(result.stdout).map((entry) => entry.name);

// lefthook pre-push exports GIT_DIR and friends; inherited into a fixture repo
// they would point git at the real checkout.
function cleanEnv() {
  const env = {};
  for (const [key, value] of Object.entries(process.env)) {
    if (!key.startsWith("GIT_")) env[key] = value;
  }
  env.GIT_CONFIG_NOSYSTEM = "1";
  env.GIT_CONFIG_GLOBAL = "/dev/null";
  return env;
}

function git(cwd, ...args) {
  const result = spawnSync(
    "git",
    [
      "-c",
      "user.name=t",
      "-c",
      "user.email=t@example.invalid",
      "-c",
      "commit.gpgsign=false",
      ...args,
    ],
    { cwd, env: cleanEnv(), encoding: "utf8" },
  );
  assert.equal(result.status, 0, `git ${args.join(" ")}: ${result.stderr}`);
}

function put(dir, file, body) {
  mkdirSync(dirname(join(dir, file)), { recursive: true });
  writeFileSync(join(dir, file), body);
}

describe("select-fuzzers.sh without a toolchain", () => {
  let dir;
  let listPath;

  before(() => {
    dir = mkdtempSync(join(tmpdir(), "select-fuzzers-"));
    listPath = join(dir, "list.json");
    writeFileSync(listPath, JSON.stringify(LIST));
    put(dir, "go.mod", "module example.com/m\n\ngo 1.26\n");
    put(dir, "internal/a/a.go", "package a\n");
    git(dir, "init", "-q", "-b", "base");
    git(dir, "add", ".");
    git(dir, "commit", "-q", "-m", "base");
  });

  after(() => rmSync(dir, { recursive: true, force: true }));

  function select(files, { base = "base", list = listPath } = {}) {
    git(dir, "checkout", "-q", "-B", "pr", "base");
    for (const file of files) put(dir, file, `${Math.random()}\n`);
    if (files.length > 0) {
      git(dir, "add", ".");
      git(dir, "commit", "-q", "-m", "change");
    }
    return spawnSync("bash", [selector, base, list], {
      cwd: dir,
      env: cleanEnv(),
      encoding: "utf8",
    });
  }

  it("runs everything when go.mod, go.sum or the CI wiring changes", () => {
    for (const file of [
      "go.mod",
      "go.sum",
      ".github/workflows/ci-verify.yml",
      ".github/ci-fuzzers.json",
      "scripts/ci/select-fuzzers.sh",
      "scripts/ci/go-fuzz.sh",
    ]) {
      const result = select([file]);
      assert.equal(result.status, 0, result.stderr);
      assert.deepEqual(names(result), ALL, file);
    }
  });

  it("runs everything when the base ref cannot be read", () => {
    const result = select(["internal/a/a.go"], { base: "origin/does-not-exist" });
    assert.equal(result.status, 0, result.stderr);
    assert.deepEqual(names(result), ALL);
    assert.match(result.stderr, /::warning::/u);
  });

  it("runs everything when the diff is empty", () => {
    assert.deepEqual(names(select([])), ALL);
  });

  it("fails loudly when the list is unreadable", () => {
    const result = select(["internal/a/a.go"], { list: join(dir, "missing.json") });
    assert.notEqual(result.status, 0);
  });
});

describe("the changes job wiring", () => {
  it("lists every fuzzer once and the changes job runs the selector and its go tests", () => {
    const list = JSON.parse(readFileSync(resolve(repoRoot, ".github", "ci-fuzzers.json"), "utf8"));
    assert.equal(new Set(list.map((entry) => entry.name)).size, list.length);
    const workflow = readFileSync(resolve(repoRoot, ".github/workflows/ci-verify.yml"), "utf8");
    assert.match(workflow, /fuzzers_json: \$\{\{ steps\.fuzzers\.outputs\.fuzzers_json \}\}/u);
    assert.match(workflow, /bash scripts\/ci\/select-fuzzers\.sh "origin\/\$\{BASE_REF\}"/u);
    const setupGo = workflow.indexOf(
      "      - name: Setup Go\n        if: github.event_name == 'pull_request'",
    );
    const goTests = workflow.indexOf("node --test scripts/ci/select-fuzzers.go.test.mjs");
    const select = workflow.indexOf("      - name: Select fuzzers");
    assert.ok(setupGo > 0 && setupGo < goTests && goTests < select, "go tests run after setup-go");
  });
});
