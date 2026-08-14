import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { measurePage, verifyPageBudgets } from "./page-weight-budget.mjs";

function fixture() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "portwing-page-weight-"));
  fs.mkdirSync(path.join(root, "assets"));
  fs.writeFileSync(
    path.join(root, "index.html"),
    '<script src="/assets/app.js?v=1"></script><link href="/assets/app.css" rel="stylesheet">',
  );
  fs.writeFileSync(path.join(root, "assets", "app.js"), "12345");
  fs.writeFileSync(path.join(root, "assets", "app.css"), "123");
  return root;
}

test("page measurement counts unique local render assets and excludes remote URLs", () => {
  const root = fixture();
  try {
    fs.appendFileSync(
      path.join(root, "index.html"),
      '<script src="/assets/app.js"></script><img src="https://example.com/remote.png">',
    );
    const result = measurePage(root, "index.html");
    assert.equal(result.assetCount, 3);
    assert.equal(result.scriptBytes, 5);
    assert.equal(result.totalBytes, fs.statSync(path.join(root, "index.html")).size + 8);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});

test("page budgets fail closed for missing routes and oversized scripts", () => {
  const root = fixture();
  try {
    assert.throws(
      () => verifyPageBudgets(root, [{ route: "missing.html", totalBytes: 10, scriptBytes: 10 }]),
      /missing page/,
    );
    assert.throws(
      () => verifyPageBudgets(root, [{ route: "index.html", totalBytes: 1_000, scriptBytes: 4 }]),
      /script budget exceeded/,
    );
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});
