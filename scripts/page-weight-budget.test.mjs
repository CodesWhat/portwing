import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { measurePage, verifyPageBudgets } from "./page-weight-budget.mjs";

function fixture() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "portwing-page-weight-"));
  fs.mkdirSync(path.join(root, "assets", "fonts"), { recursive: true });
  fs.mkdirSync(path.join(root, "assets", "images"), { recursive: true });
  fs.writeFileSync(
    path.join(root, "index.html"),
    [
      '<script src="/assets/app.js?v=1"></script>',
      '<link href="/assets/app.css" rel="stylesheet">',
      '<a href="/docs">Documentation</a>',
      '<style>.hero { background: url("/assets/images/inline.png") }</style>',
    ].join(""),
  );
  fs.writeFileSync(path.join(root, "assets", "app.js"), "12345");
  fs.writeFileSync(
    path.join(root, "assets", "app.css"),
    '@import "theme.css"; @font-face { src: url("./fonts/site.woff2") }',
  );
  fs.writeFileSync(
    path.join(root, "assets", "theme.css"),
    '.logo { background: url("./images/logo.png") }',
  );
  fs.writeFileSync(path.join(root, "assets", "fonts", "site.woff2"), "font");
  fs.writeFileSync(path.join(root, "assets", "images", "logo.png"), "logo");
  fs.writeFileSync(path.join(root, "assets", "images", "inline.png"), "inline");
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
    assert.equal(result.assetCount, 7);
    assert.equal(result.scriptBytes, 5);
    const assetBytes = [
      "assets/app.js",
      "assets/app.css",
      "assets/theme.css",
      "assets/fonts/site.woff2",
      "assets/images/logo.png",
      "assets/images/inline.png",
    ].reduce((sum, file) => sum + fs.statSync(path.join(root, file)).size, 0);
    assert.equal(result.totalBytes, fs.statSync(path.join(root, "index.html")).size + assetBytes);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});

test("page measurement ignores local navigation links", () => {
  const root = fixture();
  try {
    assert.doesNotThrow(() => measurePage(root, "index.html"));
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
