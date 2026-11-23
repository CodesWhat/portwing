// SPDX-License-Identifier: MIT

import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { after, before, describe, it } from "node:test";
import { fileURLToPath } from "node:url";

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const selector = resolve(repoRoot, "scripts", "ci", "select-fuzzers.sh");
const MODULE = "example.com/m";

// leaf <- mid <- top; other imports nothing.
const LIST = [
  { name: "FuzzLeaf", pkg: "./internal/leaf/" },
  { name: "FuzzMid", pkg: "./internal/mid/" },
  { name: "FuzzTop", pkg: "./internal/top/" },
  { name: "FuzzOther", pkg: "./internal/other/" },
];
const names = (result) => JSON.parse(result.stdout).map((entry) => entry.name);
const ALL = ["FuzzLeaf", "FuzzMid", "FuzzTop", "FuzzOther"];

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
    ["-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false", ...args],
    { cwd, env: cleanEnv(), encoding: "utf8" },
  );
  assert.equal(result.status, 0, `git ${args.join(" ")}: ${result.stderr}`);
}

function put(dir, file, body) {
  mkdirSync(dirname(join(dir, file)), { recursive: true });
  writeFileSync(join(dir, file), body);
}

describe("select-fuzzers.sh", () => {
  let dir;
  let listPath;

  before(() => {
    dir = mkdtempSync(join(tmpdir(), "select-fuzzers-"));
    listPath = join(dir, "list.json");
    writeFileSync(listPath, JSON.stringify(LIST));
    put(dir, "go.mod", `module ${MODULE}\n\ngo 1.26\n`);
    put(dir, "go.sum", "\n");
    put(dir, "internal/leaf/a.go", "package leaf\n");
    put(dir, "internal/mid/b.go", `package mid\n\nimport "${MODULE}/internal/leaf"\n`);
    put(
      dir,
      "internal/top/c.go",
      `package top\n\nimport (\n\t"fmt"\n\tm "${MODULE}/internal/mid"\n)\n`,
    );
    put(dir, "internal/other/d.go", "package other\n");
    put(dir, "docs/readme.md", "x\n");
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
    return spawnSync("bash", [selector, base, list], { cwd: dir, env: cleanEnv(), encoding: "utf8" });
  }

  it("selects a package's own fuzzers and those of everything that imports it", () => {
    const result = select(["internal/leaf/a.go"]);
    assert.equal(result.status, 0, result.stderr);
    assert.deepEqual(names(result), ["FuzzLeaf", "FuzzMid", "FuzzTop"]);
    assert.equal(result.stdout.trim().split("\n").length, 1, "output must be one line");
  });

  it("follows imports transitively and ignores unrelated packages", () => {
    assert.deepEqual(names(select(["internal/mid/b.go"])), ["FuzzMid", "FuzzTop"]);
    assert.deepEqual(names(select(["internal/top/c.go"])), ["FuzzTop"]);
    assert.deepEqual(names(select(["internal/other/d.go"])), ["FuzzOther"]);
  });

  it("maps testdata and corpus files to their owning package", () => {
    const result = select(["internal/leaf/testdata/fuzz/FuzzLeaf/seed"]);
    assert.deepEqual(names(result), ["FuzzLeaf", "FuzzMid", "FuzzTop"]);
  });

  it("selects nothing for docs and files outside every package", () => {
    const result = select(["docs/readme.md", "README.md", "website/x.tsx"]);
    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.stdout.trim(), "[]");
  });

  it("does not match a sibling directory that shares a name prefix", () => {
    assert.equal(select(["internal/leafy/e.go"]).stdout.trim(), "[]");
  });

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
    const result = select(["internal/other/d.go"], { base: "origin/does-not-exist" });
    assert.equal(result.status, 0, result.stderr);
    assert.deepEqual(names(result), ALL);
    assert.match(result.stderr, /::warning::/u);
  });

  it("runs everything when the diff is empty", () => {
    assert.deepEqual(names(select([])), ALL);
  });

  it("runs everything when a listed package is missing from the module", () => {
    const bad = join(dir, "bad.json");
    writeFileSync(bad, JSON.stringify([{ name: "FuzzGone", pkg: "./internal/gone/" }]));
    const result = select(["internal/other/d.go"], { list: bad });
    assert.equal(result.status, 0, result.stderr);
    assert.deepEqual(names(result), ["FuzzGone"]);
    assert.match(result.stderr, /could not build the import graph/u);
  });

  it("fails loudly when the list is unreadable", () => {
    const result = select(["internal/leaf/a.go"], { list: join(dir, "missing.json") });
    assert.notEqual(result.status, 0);
  });
});

describe("select-fuzzers.sh against the real module", () => {
  const real = resolve(repoRoot, ".github", "ci-fuzzers.json");

  function selectReal(files) {
    const python = spawnSync(
      "python3",
      ["-I", resolve(repoRoot, "scripts", "ci", "select-fuzzers.py"), real, repoRoot],
      { input: `${files.join("\n")}\n`, encoding: "utf8" },
    );
    assert.equal(python.status, 0, python.stderr);
    return JSON.parse(python.stdout).map((entry) => entry.name);
  }

  it("selects nothing for a docs-only change", () => {
    assert.deepEqual(selectReal(["CHANGELOG.md", "docs/content/docs/index.mdx"]), []);
  });

  it("selects the edge fuzzer for an edge-only change", () => {
    const selected = selectReal(["internal/edge/dispatch.go"]);
    assert.ok(selected.includes("FuzzEdgeRequestPath"));
    assert.ok(selected.length < 11, "an edge change must not fuzz everything");
  });
});

describe("the changes job wiring", () => {
  it("lists every fuzzer once and the go-ci caller reads the selection", async () => {
    const { readFileSync } = await import("node:fs");
    const list = JSON.parse(readFileSync(resolve(repoRoot, ".github", "ci-fuzzers.json"), "utf8"));
    assert.equal(new Set(list.map((entry) => entry.name)).size, list.length);
    const workflow = readFileSync(resolve(repoRoot, ".github/workflows/ci-verify.yml"), "utf8");
    assert.match(workflow, /fuzzers_json: \$\{\{ steps\.fuzzers\.outputs\.fuzzers_json \}\}/u);
    assert.match(workflow, /bash scripts\/ci\/select-fuzzers\.sh "origin\/\$\{BASE_REF\}"/u);
  });
});
