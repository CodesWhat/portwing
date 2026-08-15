import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  measurePage,
  verifyPageBudgets,
  verifyProductionPageBudgets,
} from "./page-weight-budget.mjs";

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

test("page measurement resolves root-relative assets below the configured mount", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "portwing-mounted-page-weight-"));
  try {
    const docsRoot = path.join(root, "docs-out");
    const deploymentRoot = path.join(root, "website-out");
    fs.mkdirSync(path.join(docsRoot, "assets"), { recursive: true });
    fs.mkdirSync(path.join(docsRoot, "other"), { recursive: true });
    fs.mkdirSync(deploymentRoot, { recursive: true });
    const html = '<script src="/docs/assets/app.js"></script><img src="/portwing.png">';
    fs.writeFileSync(path.join(docsRoot, "index.html"), html);
    fs.writeFileSync(path.join(docsRoot, "assets", "app.js"), "12345");
    fs.writeFileSync(path.join(deploymentRoot, "portwing.png"), "logo");
    assert.deepEqual(
      measurePage(docsRoot, "index.html", {
        mountPath: "/docs",
        rootOutputRoot: deploymentRoot,
      }),
      {
        totalBytes: html.length + 9,
        scriptBytes: 5,
        assetCount: 3,
      },
    );

    fs.writeFileSync(path.join(docsRoot, "index.html"), '<script src="/other/app.js"></script>');
    fs.writeFileSync(path.join(docsRoot, "other", "app.js"), "coincidental-docs-file");
    assert.throws(
      () =>
        measurePage(docsRoot, "index.html", {
          mountPath: "/docs",
          rootOutputRoot: deploymentRoot,
        }),
      /missing local asset/,
    );
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

test("production budgets measure each site's own output root", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "portwing-production-page-weight-"));
  try {
    const marketing = path.join(root, "website", "out");
    const docs = path.join(root, "docs", "out");
    fs.mkdirSync(marketing, { recursive: true });
    fs.mkdirSync(docs, { recursive: true });
    fs.writeFileSync(path.join(marketing, "index.html"), "marketing");
    fs.writeFileSync(path.join(docs, "index.html"), "docs");

    const results = verifyProductionPageBudgets(root);
    assert.deepEqual(
      results.map(({ site, route, totalBytes }) => ({ site, route, totalBytes })),
      [
        { site: "marketing", route: "index.html", totalBytes: 9 },
        { site: "docs", route: "index.html", totalBytes: 4 },
      ],
    );
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});
