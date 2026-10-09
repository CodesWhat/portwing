import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

const SCRIPT = path.resolve(import.meta.dirname, "docs-archive-cut.mjs");
const DOCS = "docs/content/docs";

// A git hook exports GIT_DIR and GIT_INDEX_FILE, and lefthook runs this suite
// from pre-push. With those inherited, every git call below would land in the
// real repository instead of the fixture, so no GIT_* variable is passed on.
const ENV = Object.fromEntries(
  Object.entries(process.env).filter(([name]) => !name.startsWith("GIT_")),
);

function git(cwd, ...args) {
  return execFileSync(
    "git",
    [
      "-c",
      "user.email=test@example.com",
      "-c",
      "user.name=test",
      "-c",
      "commit.gpgsign=false",
      "-c",
      "tag.gpgsign=false",
      ...args,
    ],
    { cwd, encoding: "utf8", env: ENV },
  ).trim();
}

function write(root, rel, content) {
  const file = path.join(root, rel);
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, content);
}

function setCurrent(root, line, pages) {
  fs.rmSync(path.join(root, DOCS, "current"), { recursive: true, force: true });
  write(root, `${DOCS}/current/meta.json`, JSON.stringify({ title: line, root: true }));
  for (const [name, body] of Object.entries(pages)) {
    write(root, `${DOCS}/current/${name}`, body);
  }
}

function commit(root, message, tag) {
  git(root, "add", "-A");
  git(root, "commit", "-q", "-m", message);
  if (tag) git(root, "tag", tag);
}

// History: a flat-layout tag, v0.7.0, v0.8.0 and v0.8.1 (whose docs differ from
// the working tree), then HEAD documenting v0.9 with a v0.9.0 tag.
function fixture(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "docs-archive-cut-test-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  git(root, "init", "-q", "-b", "main");
  // Never commit or tag unless git is really working inside the fixture.
  assert.equal(
    fs.realpathSync(git(root, "rev-parse", "--absolute-git-dir")),
    path.join(fs.realpathSync(root), ".git"),
  );
  write(root, `${DOCS}/meta.json`, JSON.stringify({ pages: ["current"] }));
  write(root, `${DOCS}/index.mdx`, "flat layout\n");
  commit(root, "flat docs", "v0.1.0");
  fs.rmSync(path.join(root, DOCS, "index.mdx"));
  setCurrent(root, "v0.7", { "index.mdx": "seven index\n", "setup.mdx": "seven setup\n" });
  commit(root, "v0.7 docs", "v0.7.0");
  setCurrent(root, "v0.8", {
    "index.mdx": "eight index\n",
    "guide/deep.mdx": "eight deep\n",
    "zero.mdx": "",
  });
  commit(root, "v0.8 docs", "v0.8.0");
  write(root, `${DOCS}/current/index.mdx`, "eight index, revised\n");
  commit(root, "v0.8.1 docs", "v0.8.1");
  setCurrent(root, "v0.9", { "index.mdx": "nine index\n" });
  commit(root, "v0.9 docs", "v0.9.0");
  return root;
}

function cut(root, ...args) {
  const result = spawnSync("node", [SCRIPT, "--root", root, ...args], {
    encoding: "utf8",
    env: ENV,
  });
  return { status: result.status, out: `${result.stdout}${result.stderr}` };
}

function snapshot(root) {
  return git(root, "status", "--porcelain", "--untracked-files=all");
}

function readJson(root, rel) {
  return JSON.parse(fs.readFileSync(path.join(root, rel), "utf8"));
}

test("cuts the archive from the tag, not the working tree", (t) => {
  const root = fixture(t);
  const result = cut(root, "v0.8.1");
  assert.equal(result.status, 0, result.out);

  const archive = path.join(root, DOCS, "v0.8");
  assert.equal(fs.readFileSync(path.join(archive, "index.mdx"), "utf8"), "eight index, revised\n");
  assert.equal(fs.readFileSync(path.join(archive, "guide/deep.mdx"), "utf8"), "eight deep\n");
  assert.equal(fs.readFileSync(path.join(archive, "zero.mdx"), "utf8"), "");
  assert.equal(readJson(root, `${DOCS}/v0.8/meta.json`).title, "v0.8");
  // The working tree's current/ was not touched.
  assert.equal(
    fs.readFileSync(path.join(root, DOCS, "current", "index.mdx"), "utf8"),
    "nine index\n",
  );

  const commitSha = git(root, "rev-list", "-n", "1", "v0.8.1");
  assert.deepEqual(readJson(root, "docs/content/archive-provenance.json"), {
    "v0.8": {
      sourceTag: "v0.8.1",
      sourceCommit: commitSha,
      sourcePath: `${DOCS}/current`,
      sourceTree: git(root, "rev-parse", `v0.8.1:${DOCS}/current`),
    },
  });
  assert.deepEqual(readJson(root, `${DOCS}/meta.json`), { pages: ["current", "v0.8"] });

  // Provenance is a tree-hash equality: the committed archive is the tag's tree.
  git(root, "add", "-A");
  const staged = git(root, "write-tree");
  assert.equal(
    git(root, "rev-parse", `${staged}:${DOCS}/v0.8`),
    git(root, "rev-parse", `v0.8.1:${DOCS}/current`),
  );
});

test("lists archives newest line first, current first of all", (t) => {
  const root = fixture(t);
  assert.equal(cut(root, "v0.7.0").status, 0);
  assert.equal(cut(root, "v0.8.1").status, 0);
  assert.deepEqual(readJson(root, `${DOCS}/meta.json`).pages, ["current", "v0.8", "v0.7"]);
  assert.deepEqual(Object.keys(readJson(root, "docs/content/archive-provenance.json")), [
    "v0.8",
    "v0.7",
  ]);
});

test("refuses without changing anything", (t) => {
  const root = fixture(t);
  const before = snapshot(root);
  const cases = [
    [["0.8.1"], "not a release tag"],
    [["v0.8"], "not a release tag"],
    [["v0.8.1-rc.1"], "not a release tag"],
    [["v0.8.9"], "does not exist"],
    [["v0.8.0"], "not the last tag of v0.8"],
    [["v0.9.0"], "line current/ documents"],
    [["v0.1.0"], "tags from before the current/ move"],
    [[], "usage"],
    [["v0.8.1", "extra"], "unexpected argument"],
  ];
  for (const [args, message] of cases) {
    const result = cut(root, ...args);
    assert.notEqual(result.status, 0, `${args.join(" ")} should fail`);
    assert.ok(result.out.includes(message), `${args.join(" ")}: ${result.out}`);
    assert.equal(snapshot(root), before, `${args.join(" ")} left changes behind`);
  }
});

test("refuses to overwrite an existing archive or provenance entry", (t) => {
  const root = fixture(t);
  assert.equal(cut(root, "v0.8.1").status, 0);
  const before = snapshot(root);
  const again = cut(root, "v0.8.1");
  assert.notEqual(again.status, 0);
  assert.ok(again.out.includes("already exists"), again.out);

  fs.rmSync(path.join(root, DOCS, "v0.8"), { recursive: true });
  const orphan = cut(root, "v0.8.1");
  assert.notEqual(orphan.status, 0);
  assert.ok(orphan.out.includes("already has an entry"), orphan.out);
  assert.notEqual(before, "");
});

test("an invalid root meta.json leaves no archive and no provenance change", (t) => {
  const root = fixture(t);
  write(root, "docs/content/archive-provenance.json", "{}\n");
  fs.writeFileSync(path.join(root, DOCS, "meta.json"), "{ not json");
  const before = snapshot(root);
  const provenanceBefore = fs.readFileSync(
    path.join(root, "docs/content/archive-provenance.json"),
    "utf8",
  );

  const failed = cut(root, "v0.8.1");
  assert.notEqual(failed.status, 0);
  assert.ok(failed.out.includes("meta.json"), failed.out);
  assert.equal(fs.existsSync(path.join(root, DOCS, "v0.8")), false);
  assert.equal(
    fs.readFileSync(path.join(root, "docs/content/archive-provenance.json"), "utf8"),
    provenanceBefore,
  );
  assert.equal(snapshot(root), before);

  // Fixing the file makes the retry succeed instead of hitting "immutable".
  write(root, `${DOCS}/meta.json`, JSON.stringify({ pages: ["current"] }));
  const retry = cut(root, "v0.8.1");
  assert.equal(retry.status, 0, retry.out);
});

test("a write that fails midway undoes what the run created", (t) => {
  const root = fixture(t);
  // A directory where the provenance file must go makes the second write fail
  // after the archive has already been copied.
  fs.mkdirSync(path.join(root, "docs/content/archive-provenance.json"), { recursive: true });
  const failed = cut(root, "v0.8.1");
  assert.notEqual(failed.status, 0);
  assert.equal(fs.existsSync(path.join(root, DOCS, "v0.8")), false);
  assert.deepEqual(readJson(root, `${DOCS}/meta.json`), { pages: ["current"] });
});

test("the replacement tag is the numerically newest patch", (t) => {
  const root = fixture(t);
  for (const name of ["v0.8.9", "v0.8.10"]) {
    git(root, "commit", "-q", "--allow-empty", "-m", name);
    git(root, "tag", name);
  }
  const result = cut(root, "v0.8.1");
  assert.notEqual(result.status, 0);
  assert.ok(result.out.includes("cut from v0.8.10 instead"), result.out);
});
