import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

const SCRIPT = path.resolve(import.meta.dirname, "docs-export-contract.mjs");

// The contract finds the repo root from its own location, so a copy placed in a
// temp tree checks that tree instead of the real one.
function tree({ content, out }) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "docs-export-contract-test-"));
  fs.mkdirSync(path.join(root, "scripts"));
  fs.copyFileSync(SCRIPT, path.join(root, "scripts", "docs-export-contract.mjs"));
  for (const file of content) touch(root, `docs/content/docs/${file}`);
  for (const file of out) touch(root, `docs/out/${file}`);
  return root;
}

function touch(root, rel) {
  const file = path.join(root, rel);
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, "");
}

function run(root) {
  const result = spawnSync("node", [path.join(root, "scripts", "docs-export-contract.mjs")], {
    encoding: "utf8",
  });
  return { status: result.status, out: `${result.stdout}${result.stderr}` };
}

const CURRENT = ["current/index.mdx", "current/auth.mdx", "current/guide/deep.mdx"];
const CURRENT_OUT = ["index.html", "auth.html", "guide/deep.html", "404.html", "_next/x.html"];
const ARCHIVE = ["v0.8/index.mdx", "v0.8/auth.mdx", "v0.8/guide/deep.mdx"];
const ARCHIVE_OUT = ["v0.8.html", "v0.8/auth.html", "v0.8/guide/deep.html"];

test("passes with zero archives and with a complete archive", () => {
  assert.equal(run(tree({ content: CURRENT, out: CURRENT_OUT })).status, 0);
  const withArchive = run(
    tree({ content: [...CURRENT, ...ARCHIVE], out: [...CURRENT_OUT, ...ARCHIVE_OUT] }),
  );
  assert.equal(withArchive.status, 0, withArchive.out);
});

test("an archive's index exports as vX.Y.html, not vX.Y/index.html", () => {
  const result = run(
    tree({
      content: [...CURRENT, ...ARCHIVE],
      out: [...CURRENT_OUT, "v0.8/index.html", "v0.8/auth.html", "v0.8/guide/deep.html"],
    }),
  );
  assert.notEqual(result.status, 0);
  assert.ok(result.out.includes("v0.8/index.mdx must export as docs/out/v0.8.html"), result.out);
  assert.ok(result.out.includes("docs/out/v0.8/index.html has no page"), result.out);
});

test("fails when an archive page is missing from the export", () => {
  const result = run(
    tree({
      content: [...CURRENT, ...ARCHIVE],
      out: [...CURRENT_OUT, "v0.8.html", "v0.8/guide/deep.html"],
    }),
  );
  assert.notEqual(result.status, 0);
  assert.ok(
    result.out.includes("v0.8/auth.mdx must export as docs/out/v0.8/auth.html"),
    result.out,
  );
});

test("fails on exported versions that have no archive", () => {
  const result = run(
    tree({ content: CURRENT, out: [...CURRENT_OUT, "v0.8.html", "v0.8/auth.html"] }),
  );
  assert.notEqual(result.status, 0);
  assert.ok(result.out.includes("docs/out/v0.8.html has no page"), result.out);
  assert.ok(result.out.includes("docs/out/v0.8/auth.html has no page"), result.out);
});

test("fails when an archive has no index page", () => {
  const result = run(
    tree({
      content: [...CURRENT, "v0.8/auth.mdx"],
      out: [...CURRENT_OUT, "v0.8/auth.html"],
    }),
  );
  assert.notEqual(result.status, 0);
  assert.ok(result.out.includes("v0.8/index.mdx is missing"), result.out);
});

test("still catches a leaked current/ directory", () => {
  const result = run(tree({ content: CURRENT, out: [...CURRENT_OUT, "current/auth.html"] }));
  assert.notEqual(result.status, 0);
  assert.ok(result.out.includes("docs/out/current/auth.html has no page"), result.out);
});
