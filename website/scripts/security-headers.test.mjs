// SPDX-License-Identifier: MIT

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

test("src detection uses the opening tag and allows attribute whitespace", () => {
  const cspFor = (html) =>
    headersForHTML(html).find((header) => header.key === "Content-Security-Policy")?.value ?? "";
  assert.match(cspFor('<script>window.example = " src=/app.js";</script>'), /'sha256-/u);
});

test("external script attributes allow whitespace around the equals sign", () => {
  const cspFor = (html) =>
    headersForHTML(html).find((header) => header.key === "Content-Security-Policy")?.value ?? "";
  for (const opening of [
    '<script src = "/app.js">',
    '<script SRC\t= "/app.js">',
    '<script\n src\n=\n"/app.js">',
  ]) {
    assert.doesNotMatch(cspFor(`${opening}alert(1)</script>`), /'sha256-/u);
  }
});

test("static output paths map to clean public routes", () => {
  assert.equal(routeForOutputPath("out/index.html"), "/");
  assert.equal(routeForOutputPath("out/compare.html"), "/compare");
  assert.equal(routeForOutputPath("out/docs/index.html"), "/docs");
  assert.equal(routeForOutputPath("out/docs/security-model.html"), "/docs/security-model");
});

test("archived docs keep their dotted version segment in the public route", () => {
  // An archive's index exports as docs/v1.0.html, a sibling of its docs/v1.0/ pages.
  assert.equal(routeForOutputPath("out/docs/v1.0.html"), "/docs/v1.0");
  assert.equal(
    routeForOutputPath("out/docs/v1.0/authentication.html"),
    "/docs/v1.0/authentication",
  );
});

test("build output serves archived docs at their dotted routes with per-page CSP", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "portwing-bop-archive-"));
  const source = path.join(root, "source");
  const target = path.join(root, ".vercel", "output");
  try {
    fs.mkdirSync(path.join(source, "docs", "v1.0"), { recursive: true });
    fs.writeFileSync(path.join(source, "docs", "v1.0.html"), "<script>archiveIndex()</script>");
    fs.writeFileSync(
      path.join(source, "docs", "v1.0", "auth.html"),
      "<script>archiveAuth()</script>",
    );

    generateBuildOutput(source, target);

    const config = JSON.parse(fs.readFileSync(path.join(target, "config.json"), "utf8"));
    assert.deepEqual(config.overrides["docs/v1.0.html"], { path: "docs/v1.0" });
    assert.deepEqual(config.overrides["docs/v1.0/auth.html"], { path: "docs/v1.0/auth" });

    const indexRoute = config.routes.find((route) => route.src === "^/docs/v1\\.0/?$");
    const authRoute = config.routes.find((route) => route.src === "^/docs/v1\\.0/auth/?$");
    assert.ok(indexRoute, "the archive index needs its own route");
    assert.ok(authRoute, "an archive page needs its own route");
    assert.match(indexRoute.headers["Content-Security-Policy"], /script-src[^;]*'sha256-/);
    assert.notEqual(
      indexRoute.headers["Content-Security-Policy"],
      authRoute.headers["Content-Security-Policy"],
    );
    // The dot is escaped, so the route cannot match some other path with any character there.
    assert.equal(new RegExp(indexRoute.src).test("/docs/v1x0"), false);
    assert.equal(new RegExp(indexRoute.src).test("/docs/v1.0"), true);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
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
    path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "src", "app", "page.tsx"),
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

test("build output serves the not-found page on filesystem misses with its own CSP", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "portwing-bop-404-"));
  const source = path.join(root, "source");
  const target = path.join(root, ".vercel", "output");
  try {
    fs.mkdirSync(source, { recursive: true });
    fs.writeFileSync(path.join(source, "index.html"), "<script>root()</script>");
    fs.writeFileSync(path.join(source, "404.html"), "<script>missing()</script>");

    generateBuildOutput(source, target);

    const config = JSON.parse(fs.readFileSync(path.join(target, "config.json"), "utf8"));
    assert.deepEqual(config.overrides["404.html"], { path: "404" });
    const phases = config.routes.filter((route) => route.handle).map((route) => route.handle);
    assert.deepEqual(phases, ["filesystem"]);
    const filesystemAt = config.routes.findIndex((route) => route.handle === "filesystem");
    assert.ok(filesystemAt > 0, "header routes must come before the filesystem phase");
    assert.ok(
      config.routes.slice(0, filesystemAt).every((route) => route.continue === true),
      "everything before the filesystem phase only attaches headers or status",
    );
    assert.deepEqual(
      config.routes[filesystemAt - 1],
      { src: "^/404/?$", status: 404, continue: true },
      "a direct /404 answers 404, as @vercel/next emits",
    );
    assert.equal(filesystemAt, config.routes.length - 2, "the miss route is last");
    const notFound = config.routes[filesystemAt + 1];
    assert.equal(notFound.src, "^/.*$");
    assert.equal(notFound.status, 404);
    assert.equal(notFound.dest, "/404");
    assert.equal(notFound.continue, undefined);
    assert.equal(notFound.headers["X-Content-Type-Options"], "nosniff");
    assert.match(notFound.headers["Content-Security-Policy"], /script-src[^;]*'sha256-/);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
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

function imgSources(csp) {
  const directive = csp.split("; ").find((part) => part.startsWith("img-src "));
  return directive ? directive.split(" ").slice(1) : [];
}

test("the Star History chart origin lands only on the page that renders it", () => {
  const cspFor = (html) =>
    headersForHTML(html).find((header) => header.key === "Content-Security-Policy")?.value ?? "";
  const chart =
    '<a href="https://www.star-history.com/?repos=CodesWhat%2Fportwing"><img src="https://api.star-history.com/chart?repos=CodesWhat/portwing&amp;type=date&amp;legend=top-left" alt="Star history"><img src="https://api.star-history.com/chart?repos=CodesWhat/portwing&amp;type=date&amp;legend=top-left&amp;theme=dark" alt=""></a>';

  const withChart = cspFor(chart);
  assert.deepEqual(imgSources(withChart), ["'self'", "data:", "https://api.star-history.com"]);
  // The chart page link is navigation, not a fetch, so www.star-history.com gets no grant.
  assert.deepEqual(cspOrigins(withChart), [
    "https://api.star-history.com",
    "https://e.codeswhat.com",
  ]);
  assert.deepEqual(imgSources(cspFor("<p>no chart</p>")), ["'self'", "data:"]);

  const root = fs.mkdtempSync(path.join(os.tmpdir(), "portwing-bop-star-"));
  const target = path.join(root, ".vercel", "output");
  try {
    const source = path.join(root, "source");
    fs.mkdirSync(path.join(source, "docs"), { recursive: true });
    fs.writeFileSync(path.join(source, "index.html"), chart);
    fs.writeFileSync(path.join(source, "docs", "guide.html"), "<p>guide</p>");
    generateBuildOutput(source, target);
    const config = JSON.parse(fs.readFileSync(path.join(target, "config.json"), "utf8"));
    const csp = (src) =>
      config.routes.find((route) => route.src === src).headers["Content-Security-Policy"];
    assert.deepEqual(imgSources(csp("^/$")), ["'self'", "data:", "https://api.star-history.com"]);
    assert.deepEqual(imgSources(csp("^/docs/guide/?$")), ["'self'", "data:"]);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});

test("only the home page source references Star History", () => {
  const srcDir = path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "src");
  const hits = [];
  const walk = (dir) => {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      const target = path.join(dir, entry.name);
      if (entry.isDirectory()) walk(target);
      else if (
        /\.(tsx?|css|mdx?)$/u.test(entry.name) &&
        /star-history/iu.test(fs.readFileSync(target, "utf8"))
      ) {
        hits.push(path.relative(srcDir, target));
      }
    }
  };
  walk(srcDir);
  assert.deepEqual(hits, [path.join("app", "page.tsx")]);
});
