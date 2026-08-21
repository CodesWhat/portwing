import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { generateBuildOutput, headersForHTML, routeForOutputPath } from "./security-headers.mjs";

function cspOrigins(csp) {
  return [...new Set((csp ?? "").match(/https?:\/\/[^\s;]+/gu) ?? [])].sort();
}

test("CSP hashes inline scripts and ignores external scripts", () => {
  const headers = headersForHTML('<script>alert(1)</script><script src="/_next/app.js"></script>');
  const csp = headers.find((header) => header.key === "Content-Security-Policy")?.value;
  assert.ok(csp);
  assert.match(csp, /script-src 'self'[^;]*'sha256-bhHHL3z2vDgxUt0W3dWQOrprscmda2Y5pLsLg4GF\+pI='/);
  assert.doesNotMatch(csp.match(/script-src[^;]+/)?.[0] ?? "", /unsafe-inline/);
  assert.match(csp, /frame-ancestors 'none'/);
  assert.match(csp, /script-src[^;]*https:\/\/e\.codeswhat\.com/);
  assert.match(csp, /connect-src 'self' https:\/\/e\.codeswhat\.com/);
  assert.doesNotMatch(csp, /us\.i\.posthog\.com|us\.posthog\.com|\*\.posthog\.com/);
});

test("inline scripts are hashed for every tag form an HTML parser accepts", () => {
  const cspFor = (html) =>
    headersForHTML(html).find((header) => header.key === "Content-Security-Policy")?.value ?? "";
  const canonical = cspFor("<script>alert(1)</script>").match(/'sha256-[^']+'/u)?.[0];
  assert.ok(canonical, "the canonical form must produce a hash to compare against");

  // Each of these opens and closes a script element as far as a browser is
  // concerned, so each must contribute the same hash. A form the scanner misses
  // ships a CSP that blocks a script the page needs.
  for (const html of [
    "<SCRIPT>alert(1)</SCRIPT>",
    "<sCrIpT>alert(1)</ScRiPt>",
    "<script >alert(1)</script >",
    '<script>alert(1)</script foo="bar">',
    "<script\t\n>alert(1)</script\t\n bar>",
  ]) {
    assert.ok(cspFor(html).includes(canonical), `not hashed: ${JSON.stringify(html)}`);
  }

  // Still skipped by src, whatever case the attribute is written in.
  assert.doesNotMatch(cspFor('<SCRIPT SRC="/app.js"></SCRIPT>'), /'sha256-/u);
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

test("website source and runtime CSP contain no Go Report Card surface", () => {
  const source = fs.readFileSync(
    path.join(
      path.dirname(fileURLToPath(import.meta.url)),
      "..",
      "src",
      "components",
      "github-badges.tsx",
    ),
    "utf8",
  );
  assert.doesNotMatch(source, /goreportcard\.com|Go Report Card/i);

  const headers = headersForHTML('<img src="https://pkg.go.dev/badge.svg">');
  const csp = headers.find((header) => header.key === "Content-Security-Policy")?.value;
  // Assert the whole external allow-list rather than the absence of one host.
  // Naming a host in a deny check only catches the host you thought of, and a
  // substring or unanchored-regex check for one is itself the bypass pattern
  // CodeQL flags. Enumerating every origin the CSP grants catches Go Report
  // Card and anything else that shows up, and it fails loudly when the set
  // legitimately changes, which is when someone should be looking.
  assert.deepEqual(cspOrigins(csp), ["https://e.codeswhat.com", "https://pkg.go.dev"]);
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
    assert.match(rootRoute.headers["Content-Security-Policy"], /script-src[^;]*'sha256-/);
    assert.match(
      rootRoute.headers["Content-Security-Policy"],
      /connect-src 'self' https:\/\/e\.codeswhat\.com/,
    );
    assert.match(
      guideRoute.headers["Content-Security-Policy"],
      /connect-src 'self' https:\/\/e\.codeswhat\.com/,
    );
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
