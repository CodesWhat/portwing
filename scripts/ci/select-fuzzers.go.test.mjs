// SPDX-License-Identifier: MIT
//
// Cases that run a real `go list`. They need the module's Go toolchain, so they
// live outside the scripts/*.test.mjs glob that `npm run test:web` runs (Node CI
// has no Go). The changes job runs this file on PRs, after setup-go. The
// toolchain-free cases are scripts/select-fuzzers.test.mjs.

import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import {
  appendFileSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { after, before, describe, it } from "node:test";
import { fileURLToPath } from "node:url";

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..", "..");
const selector = resolve(repoRoot, "scripts", "ci", "select-fuzzers.sh");
const MODULE = "example.com/m";

// leaf <- mid <- top; alias imports leaf under an alias on one line; testonly
// imports other from a _test.go file; tagged imports other behind a build tag.
const LIST = [
  { name: "FuzzLeaf", pkg: "./internal/leaf/" },
  { name: "FuzzMid", pkg: "./internal/mid/" },
  { name: "FuzzTop", pkg: "./internal/top/" },
  { name: "FuzzOther", pkg: "./internal/other/" },
  { name: "FuzzAlias", pkg: "./internal/alias/" },
  { name: "FuzzTestonly", pkg: "./internal/testonly/" },
  { name: "FuzzTagged", pkg: "./internal/tagged/" },
];
const names = (result) => JSON.parse(result.stdout).map((entry) => entry.name);
const ALL = LIST.map((entry) => entry.name);

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
    put(
      dir,
      "internal/alias/f.go",
      `package alias\n\nimport dep "${MODULE}/internal/leaf"\n\nvar _ = dep.X\n`,
    );
    put(dir, "internal/leaf/a.go", "package leaf\n\nvar X = 1\n");
    put(dir, "internal/testonly/t.go", "package testonly\n");
    put(
      dir,
      "internal/testonly/t_test.go",
      `package testonly\n\nimport _ "${MODULE}/internal/other"\n`,
    );
    put(dir, "internal/tagged/x.go", "package tagged\n");
    put(
      dir,
      "internal/tagged/i.go",
      `//go:build integration\n\npackage tagged\n\nimport _ "${MODULE}/internal/other"\n`,
    );
    put(dir, "docs/readme.md", "x\n");
    git(dir, "init", "-q", "-b", "base");
    git(dir, "add", ".");
    git(dir, "commit", "-q", "-m", "base");
  });

  after(() => rmSync(dir, { recursive: true, force: true }));

  function select(files, { base = "base", list = listPath } = {}) {
    git(dir, "checkout", "-q", "-B", "pr", "base");
    for (const entry of files) {
      const [file, body] = Array.isArray(entry) ? entry : [entry, undefined];
      if (body !== undefined) put(dir, file, body);
      else if (file.endsWith(".go") && existsSync(join(dir, file))) {
        appendFileSync(join(dir, file), `// ${Math.random()}\n`);
      } else if (file.endsWith(".go")) {
        put(dir, file, `package ${dirname(file).split("/").pop()}\n// ${Math.random()}\n`);
      } else put(dir, file, `${Math.random()}\n`);
    }
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

  it("selects a package's own fuzzers and those of everything that imports it", () => {
    const result = select(["internal/leaf/a.go"]);
    assert.equal(result.status, 0, result.stderr);
    assert.deepEqual(names(result), ["FuzzLeaf", "FuzzMid", "FuzzTop", "FuzzAlias"]);
    assert.equal(result.stdout.trim().split("\n").length, 1, "output must be one line");
  });

  it("follows imports transitively and ignores unrelated packages", () => {
    assert.deepEqual(names(select(["internal/mid/b.go"])), ["FuzzMid", "FuzzTop"]);
    assert.deepEqual(names(select(["internal/top/c.go"])), ["FuzzTop"]);
    assert.deepEqual(names(select(["internal/other/d.go"])), [
      "FuzzOther",
      "FuzzTestonly",
      "FuzzTagged",
    ]);
  });

  it("maps testdata and corpus files to their owning package", () => {
    const result = select(["internal/leaf/testdata/fuzz/FuzzLeaf/seed"]);
    assert.deepEqual(names(result), ["FuzzLeaf", "FuzzMid", "FuzzTop", "FuzzAlias"]);
  });

  it("resolves an aliased single-line import", () => {
    assert.ok(names(select(["internal/leaf/a.go"])).includes("FuzzAlias"));
    assert.deepEqual(names(select(["internal/alias/f.go"])), ["FuzzAlias"]);
  });

  it("follows an import that only a _test.go file makes", () => {
    assert.ok(names(select(["internal/other/d.go"])).includes("FuzzTestonly"));
  });

  it("follows an import behind a build tag the default build would miss", () => {
    assert.ok(names(select(["internal/other/d.go"])).includes("FuzzTagged"));
  });

  it("runs everything when a Go file is excluded in every configuration", () => {
    const result = select([["internal/other/zz.go", "//go:build neverset\n\npackage other\n"]]);
    assert.equal(result.status, 0, result.stderr);
    assert.deepEqual(names(result), ALL);
    assert.match(result.stderr, /could not build the import graph/u);
  });

  it("selects by a non-ASCII changed file name (git -z, not quoted paths)", () => {
    const result = select(["internal/leaf/testdata/fuzz/FuzzLeaf/s\u00e9ed-\u00e9\u4e2d"]);
    assert.equal(result.status, 0, result.stderr);
    assert.deepEqual(names(result), ["FuzzLeaf", "FuzzMid", "FuzzTop", "FuzzAlias"]);
  });

  it("runs everything when a package has a parse error, which go list -e reports with exit 0", () => {
    const result = select([["internal/other/broken.go", 'package other\n\nimport (\n\t"fmt"\n']]);
    assert.equal(result.status, 0, result.stderr);
    assert.deepEqual(names(result), ALL);
    assert.match(result.stderr, /could not build the import graph/u);
  });

  it("runs everything when a package is broken only on linux, the GOOS CI and production build", () => {
    const result = select([
      [
        "internal/other/broken_linux.go",
        '//go:build linux\n\npackage other\n\nimport (\n\t"fmt"\n',
      ],
    ]);
    assert.equal(result.status, 0, result.stderr);
    assert.deepEqual(names(result), ALL);
    assert.match(result.stderr, /could not build the import graph/u);
  });

  it("selects nothing for docs and files outside every package", () => {
    const result = select(["docs/readme.md", "README.md", "website/x.tsx"]);
    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.stdout.trim(), "[]");
  });

  it("does not match a sibling directory that shares a name prefix", () => {
    assert.equal(select(["internal/leafy/e.go"]).stdout.trim(), "[]");
  });

  it("runs everything when a listed package is missing from the module", () => {
    const bad = join(dir, "bad.json");
    writeFileSync(bad, JSON.stringify([{ name: "FuzzGone", pkg: "./internal/gone/" }]));
    const result = select(["internal/other/d.go"], { list: bad });
    assert.equal(result.status, 0, result.stderr);
    assert.deepEqual(names(result), ["FuzzGone"]);
    assert.match(result.stderr, /could not build the import graph/u);
  });
});

describe("select-fuzzers against the real module", () => {
  const real = resolve(repoRoot, ".github", "ci-fuzzers.json");
  const fuzzers = JSON.parse(readFileSync(real, "utf8"));

  function selectReal(files, root = repoRoot) {
    const python = spawnSync(
      "python3",
      ["-I", resolve(repoRoot, "scripts", "ci", "select-fuzzers.py"), real, root],
      { input: Buffer.from(files.map((file) => `${file}\0`).join("")), encoding: "utf8" },
    );
    return python;
  }
  const namesOf = (python) => {
    assert.equal(python.status, 0, python.stderr);
    return JSON.parse(python.stdout).map((entry) => entry.name);
  };

  it("selects nothing for a docs-only change", () => {
    assert.deepEqual(namesOf(selectReal(["CHANGELOG.md", "docs/content/docs/index.mdx"])), []);
  });

  it("selects the edge fuzzer for an edge-only change", () => {
    assert.deepEqual(namesOf(selectReal(["internal/edge/dispatch.go"])), ["FuzzEdgeRequestPath"]);
  });

  it("selects FuzzVerifyRequest for a non-ASCII seed corpus file under internal/auth", () => {
    const selected = namesOf(
      selectReal(["internal/auth/testdata/fuzz/FuzzVerifyRequest/s\u00e9ed-\u00e9"]),
    );
    assert.ok(selected.includes("FuzzVerifyRequest"));
  });

  it("always selects a fuzzer for testdata under its own package", () => {
    for (const { name, pkg } of fuzzers) {
      const file = `${pkg.replace(/^\.\//u, "")}testdata/fuzz/${name}/seed`;
      assert.ok(namesOf(selectReal([file])).includes(name), `${name} via ${file}`);
    }
  });

  it("exits non-zero when go list fails, so the caller runs everything", () => {
    const empty = mkdtempSync(join(tmpdir(), "select-fuzzers-nomod-"));
    try {
      assert.notEqual(selectReal(["internal/edge/x.go"], empty).status, 0);
    } finally {
      rmSync(empty, { recursive: true, force: true });
    }
  });
});
