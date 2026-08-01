import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  generateBuildOutput,
  headersForHTML,
  routeForOutputPath,
} from "./security-headers.mjs";

test("CSP hashes inline scripts and ignores external scripts", () => {
  const headers = headersForHTML(
    '<script>alert(1)</script><script src="/_next/app.js"></script>',
  );
  const csp = headers.find((header) => header.key === "Content-Security-Policy")?.value;
  assert.ok(csp);
  assert.match(csp, /script-src 'self' 'sha256-bhHHL3z2vDgxUt0W3dWQOrprscmda2Y5pLsLg4GF\+pI='/);
  assert.doesNotMatch(csp.match(/script-src[^;]+/)?.[0] ?? "", /unsafe-inline/);
  assert.match(csp, /frame-ancestors 'none'/);
});

test("static output paths map to clean public routes", () => {
  assert.equal(routeForOutputPath("out/index.html"), "/");
  assert.equal(routeForOutputPath("out/compare.html"), "/compare");
  assert.equal(routeForOutputPath("out/docs/index.html"), "/docs");
  assert.equal(routeForOutputPath("out/docs/security-model.html"), "/docs/security-model");
});

test("CSP permits only image origins present in the rendered page", () => {
  const headers = headersForHTML(
    '<img src="https://pkg.go.dev/badge.svg"><a href="https://unrelated.example">link</a>',
  );
  const csp = headers.find((header) => header.key === "Content-Security-Policy")?.value;
  assert.match(csp ?? "", /img-src 'self' data: https:\/\/pkg\.go\.dev/);
  assert.doesNotMatch(csp ?? "", /unrelated\.example/);
});

test("build output packages the exact rendered files and per-page CSP", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "portwing-bop-"));
  const source = path.join(root, "source");
  const target = path.join(root, ".vercel", "output");
  try {
    fs.mkdirSync(path.join(source, "docs"), { recursive: true });
    fs.writeFileSync(path.join(source, "index.html"), "<script>root()</script>");
    fs.writeFileSync(path.join(source, "docs", "guide.html"), "<script>guide()</script>");
    fs.writeFileSync(path.join(source, "asset.txt"), "asset");

    generateBuildOutput(source, target);

    assert.equal(fs.readFileSync(path.join(target, "static", "asset.txt"), "utf8"), "asset");
    const config = JSON.parse(fs.readFileSync(path.join(target, "config.json"), "utf8"));
    assert.equal(config.version, 3);
    assert.equal(config.overrides["index.html"], undefined);
    assert.deepEqual(config.overrides["docs/guide.html"], { path: "docs/guide" });

    const rootRoute = config.routes.find((route) => route.src === "^/$");
    const guideRoute = config.routes.find((route) => route.src === "^/docs/guide/?$");
    const commonRoute = config.routes.find((route) => route.src === "^/.*$");
    assert.equal(rootRoute.continue, true);
    assert.match(rootRoute.headers["Content-Security-Policy"], /script-src 'self' 'sha256-/);
    assert.equal(guideRoute.continue, true);
    assert.notEqual(
      guideRoute.headers["Content-Security-Policy"],
      rootRoute.headers["Content-Security-Policy"],
    );
    assert.equal(commonRoute.headers["X-Frame-Options"], "DENY");
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});
