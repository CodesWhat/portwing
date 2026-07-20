import assert from "node:assert/strict";
import test from "node:test";

import { headersForHTML, routeForOutputPath } from "./security-headers.mjs";

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
